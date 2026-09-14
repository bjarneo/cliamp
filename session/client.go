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

	// reason is what the host said when it ended the session, if anything,
	// and inputErr why this side ended it. Both are written before leaving
	// is set and read after it, so the atomic orders the handoff to run.
	reason   string
	inputErr error

	writeMu sync.Mutex
	// leaving marks a detach this client asked for, so the read error that
	// follows closing the connection is the expected end of the session
	// rather than a failure to report.
	leaving atomic.Bool
}

// run pumps the session until either side ends it.
func (c *attachClient) run() error {
	stop := make(chan struct{})
	defer close(stop)
	go c.forwardInput(stop)
	go c.pollResize(stop)

	for {
		kind, payload, err := readFrame(c.conn)
		if err != nil {
			// Ending the session on this side looks like a read error from
			// here, so the input pump's own reason comes first.
			if c.leaving.Load() {
				return c.inputErr
			}
			if errors.Is(err, io.EOF) {
				return nil
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

// leave ends the session on this side and unblocks run, which reports err.
// The atomic write is what publishes err to run's goroutine.
func (c *attachClient) leave(err error) {
	c.inputErr = err
	c.leaving.Store(true)
	_ = c.conn.Close()
}

// forwardInput sends local keystrokes to the host, holding back the detach
// key, and ends the session when it can no longer do that: a client that
// renders on with nothing reaching the player has no way out, detach key
// included.
func (c *attachClient) forwardInput(stop <-chan struct{}) {
	buf := make([]byte, 4096)
	for {
		n, err := c.in.Read(buf)
		select {
		case <-stop:
			return
		default:
		}
		if n > 0 {
			data := buf[:n]
			if index := bytes.IndexByte(data, DetachKey); index >= 0 {
				if index > 0 {
					_ = c.send(kindInput, data[:index])
				}
				c.leaving.Store(true)
				_ = c.send(kindDetach, nil)
				c.leave(nil)
				return
			}
			if sendErr := c.send(kindInput, data); sendErr != nil {
				c.leave(fmt.Errorf("forward input: %w", sendErr))
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.leave(nil)
				return
			}
			c.leave(fmt.Errorf("read terminal: %w", err))
			return
		}
	}
}

// pollResize reports terminal size changes for as long as the session lives.
func (c *attachClient) pollResize(stop <-chan struct{}) {
	ticker := time.NewTicker(resizePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
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

// send writes one frame to the host under a deadline.
func (c *attachClient) send(kind byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return writeFrame(c.conn, kind, payload)
}

// marshalParams encodes the handshake parameters for the attach request.
func marshalParams(params AttachParams) ([]byte, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal attach params: %w", err)
	}
	return data, nil
}
