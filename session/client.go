package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/bjarneo/cliamp/ipc"
)

// DetachKey is ctrl+\, handled by the client and never forwarded. Detaching is
// normally the player's own quit key, which needs the session to be answering
// its input; this one does not, so it stays as the way out of a wedged
// session. The keymap leaves it unused.
const DetachKey = 0x1C

// The host's renderer sets terminal modes against a terminal that was not
// ours, and by the time it learns this client is gone it can no longer write
// here. So the client sets up and restores its own terminal: it saves the
// window title and enables bracketed paste (what the URL and search overlays
// need to receive a paste as one event) on attach, and on detach it leaves the
// alternate screen, pops the keyboard protocols the renderer pushed, and puts
// the title back.
const (
	terminalSetup   = "\x1b[22;0t\x1b[?2004h"
	terminalRestore = "\x1b[?2004l\x1b[<u\x1b[>4;0m\x1b[?25h\x1b[0m\x1b[?1049l\x1b[23;0t"
)

// resizePollInterval is how often the client checks its terminal size. SIGWINCH
// does not exist on every platform cliamp builds for, and a poll is accurate
// enough for a resize a human just performed.
const resizePollInterval = 250 * time.Millisecond

// ErrNoSession reports that nothing is listening on the socket.
var ErrNoSession = ipc.ErrNotRunning

// ErrNotAttachable reports a running cliamp that owns a real terminal, and so
// has no virtual terminal to lend.
var ErrNotAttachable = errors.New("the running cliamp owns a terminal; start it with --daemon to attach")

// ClientOptions configure Attach.
type ClientOptions struct {
	// In and Out default to the process standard input and output.
	In  *os.File
	Out *os.File
	// Client identifies this build in the host log.
	Client string
}

// Attach lends the local terminal to the detached cliamp listening on
// sockPath and returns when the user detaches or the session ends.
func Attach(sockPath string, opts ClientOptions) error {
	in, out := opts.In, opts.Out
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	if !term.IsTerminal(in.Fd()) || !term.IsTerminal(out.Fd()) {
		return errors.New("attach needs a terminal on stdin and stdout")
	}
	width, height, err := term.GetSize(out.Fd())
	if err != nil {
		return fmt.Errorf("terminal size: %w", err)
	}

	params, err := marshalParams(AttachParams{Width: width, Height: height, Client: opts.Client})
	if err != nil {
		return err
	}
	conn, _, err := ipc.DialAttach(sockPath, ipc.V2Request{ID: []byte(`"attach"`), Params: params})
	if err != nil {
		var protocolErr *ipc.V2Error
		if errors.As(err, &protocolErr) && protocolErr.Code == ipc.V2ErrorCodeUnavailable {
			return ErrNotAttachable
		}
		return err
	}
	defer conn.Close()

	// Registered before the terminal restore so it prints after it: a client
	// that was displaced or whose session stopped should say so on a terminal
	// that is already back to normal.
	var reason string
	defer func() {
		if reason != "" {
			fmt.Fprintf(os.Stderr, "cliamp: %s\n", reason)
		}
	}()

	state, err := term.MakeRaw(in.Fd())
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer func() {
		_ = term.Restore(in.Fd(), state)
		_, _ = out.WriteString(terminalRestore)
	}()
	if err := keepNewlineTranslation(out.Fd()); err != nil {
		return fmt.Errorf("terminal newline mode: %w", err)
	}
	_, _ = out.WriteString(terminalSetup)

	client := &attachClient{
		conn:   conn,
		in:     in,
		out:    colorprofile.NewWriter(out, os.Environ()),
		outFd:  out.Fd(),
		width:  width,
		height: height,
	}
	err = client.run()
	reason = client.reason
	return err
}

type attachClient struct {
	conn   net.Conn
	in     *os.File
	out    io.Writer
	outFd  uintptr
	width  int
	height int

	// reason is what the host said when it ended the session, if anything.
	reason string

	writeMu sync.Mutex
	// leaving marks a detach this client asked for, so the read error that
	// follows closing the connection is the expected end of the session
	// rather than a failure to report.
	leaving atomic.Bool
}

// run pumps the session until either side ends it.
func (c *attachClient) run() error {
	done := make(chan error, 1)
	go func() { done <- c.forwardInput() }()
	go c.pollResize(done)

	for {
		kind, payload, err := readFrame(c.conn)
		if err != nil {
			if c.leaving.Load() || errors.Is(err, io.EOF) {
				return nil
			}
			select {
			case inputErr := <-done:
				if inputErr != nil {
					return inputErr
				}
				return nil
			default:
			}
			return fmt.Errorf("session ended: %w", err)
		}
		switch kind {
		case kindOutput:
			if _, err := c.out.Write(payload); err != nil {
				return fmt.Errorf("write terminal: %w", err)
			}
		case kindDetach:
			c.reason = string(payload)
			return nil
		}
	}
}

// forwardInput sends local keystrokes to the host, holding back the detach
// key. It returns once the user detaches.
func (c *attachClient) forwardInput() error {
	buf := make([]byte, 4096)
	for {
		n, err := c.in.Read(buf)
		if n > 0 {
			data := buf[:n]
			if index := bytes.IndexByte(data, DetachKey); index >= 0 {
				if index > 0 {
					_ = c.send(kindInput, data[:index])
				}
				c.leaving.Store(true)
				_ = c.send(kindDetach, nil)
				_ = c.conn.Close()
				return nil
			}
			if err := c.send(kindInput, data); err != nil {
				return nil
			}
		}
		if err != nil {
			c.leaving.Store(true)
			_ = c.conn.Close()
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read terminal: %w", err)
		}
	}
}

// pollResize reports terminal size changes for as long as the session lives.
func (c *attachClient) pollResize(done <-chan error) {
	ticker := time.NewTicker(resizePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			width, height, err := term.GetSize(c.outFd)
			if err != nil || width <= 0 || height <= 0 {
				continue
			}
			if width == c.width && height == c.height {
				continue
			}
			c.width, c.height = width, height
			if err := c.send(kindResize, encodeSize(width, height)); err != nil {
				return
			}
		}
	}
}

func (c *attachClient) send(kind byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return writeFrame(c.conn, kind, payload)
}

func marshalParams(params AttachParams) ([]byte, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal attach params: %w", err)
	}
	return data, nil
}
