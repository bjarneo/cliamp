package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/ipc"
)

// writeTimeout bounds one output frame write. A client that stops reading
// stalls the program's render loop, so it is dropped instead of held.
const writeTimeout = 5 * time.Second

// startupSizeGrace is how long after an attach the host re-asserts the client's
// terminal size. Bubble Tea sends its own initial size -- the placeholder from
// WithWindowSize -- with `go p.Send(...)`, so for a client that attaches while
// the program is still starting, that message can land after ours and leave
// the session rendering at the placeholder size until the user resizes.
const startupSizeGrace = 250 * time.Millisecond

// Options configure a Host.
type Options struct {
	// OnAttach runs after a client's terminal is connected, OnDetach once it
	// is gone. They let the caller suspend and resume work that only matters
	// while somebody is watching.
	OnAttach func(width, height int)
	OnDetach func()
}

// Host is the virtual terminal a detached cliamp renders into. It satisfies
// ipc.AttachHandler: an attaching client lends its terminal to the running
// program, and detaching leaves the program running with nothing reading its
// output.
//
// Attach and detach go through Bubble Tea's own RestoreTerminal and
// ReleaseTerminal. That is what makes the handover clean: while no client is
// attached the renderer is stopped, so not one byte is written for a terminal
// that is not there, and it is restarted against the client's terminal with a
// full repaint. Writing to the switched output alone is not enough -- the
// renderer tracks a relative cursor in inline mode and would send a client
// cursor movements measured against a screen it never saw.
type Host struct {
	opts   Options
	inputR *os.File
	inputW *os.File
	output *outputSwitch

	// handover serializes whole attach and detach sequences against each
	// other: each one drives the program's terminal in and out, and half of
	// one interleaved with half of the other leaves the renderer either
	// stopped with a client attached or started twice. It is never held while
	// reading state the program's own loop asks for, so Detach (which the
	// player's quit key calls from that loop) cannot deadlock against it.
	handover sync.Mutex
	// restored tracks whether the program currently holds a terminal, so it
	// is released and restored exactly once per client.
	restored bool

	mu     sync.Mutex
	prog   *tea.Program
	client *clientConn
	closed bool
}

// New creates a detached host. Wire Input and Output into the Bubble Tea
// program, then hand the program back with SetProgram.
func New(opts Options) (*Host, error) {
	// A pipe, rather than an in-memory reader: Bubble Tea can only cancel a
	// blocking read on something with a file descriptor, and ReleaseTerminal
	// cancels the input reader. An in-memory reader would leave the old read
	// loop parked on it and a second one racing it after every attach.
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("session: input pipe: %w", err)
	}
	return &Host{
		opts:   opts,
		inputR: reader,
		inputW: writer,
		output: &outputSwitch{},
		// The program starts with its renderer running, drawing into the
		// discarded output. The first attach releases that before taking the
		// client's terminal.
		restored: true,
	}, nil
}

// Input is the program's terminal input: client keystrokes, and nothing at all
// while no client is attached.
func (h *Host) Input() io.Reader { return h.inputR }

// Output is the program's terminal output. Writes are discarded while no
// client is attached.
func (h *Host) Output() io.Writer { return h.output }

// SetProgram wires the program the host drives. It must be called before the
// first client attaches.
func (h *Host) SetProgram(prog *tea.Program) {
	h.mu.Lock()
	h.prog = prog
	h.mu.Unlock()
}

// Attached reports whether a client terminal is connected.
func (h *Host) Attached() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.client != nil
}

// HandleAttach serves one attach session and returns when it ends.
func (h *Host) HandleAttach(conn net.Conn, request ipc.V2Request) {
	var params AttachParams
	if len(request.Params) > 0 {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			_ = ipc.WriteV2Response(conn, ipc.V2Response{
				ID:    request.ID,
				OK:    false,
				Error: &ipc.V2Error{Code: ipc.V2ErrorCodeInvalidParams, Message: ipc.V2MessageInvalidParams},
			})
			return
		}
	}
	if params.Width <= 0 || params.Height <= 0 {
		_ = ipc.WriteV2Response(conn, ipc.V2Response{
			ID:    request.ID,
			OK:    false,
			Error: &ipc.V2Error{Code: ipc.V2ErrorCodeInvalidParams, Message: ipc.V2MessageInvalidParams, Detail: "attach needs a terminal size"},
		})
		return
	}

	h.handover.Lock()
	h.mu.Lock()
	if h.closed || h.prog == nil {
		h.mu.Unlock()
		h.handover.Unlock()
		_ = ipc.WriteV2Response(conn, ipc.V2Response{
			ID:    request.ID,
			OK:    false,
			Error: &ipc.V2Error{Code: ipc.V2ErrorCodeUnavailable, Message: ipc.V2MessageUnavailable},
		})
		return
	}
	// One program has one renderer, so a second client takes the terminal
	// over rather than sharing it. The previous client is told why.
	previous := h.client
	prog := h.prog
	client := &clientConn{conn: conn, width: params.Width, height: params.Height}
	h.client = client
	h.mu.Unlock()

	size := tea.WindowSizeMsg{Width: params.Width, Height: params.Height}
	// Send before touching the terminal: Send only returns once the program's
	// loop is running, and Run wires the input reader and the renderer that
	// Release and RestoreTerminal act on before that. A client attaching in
	// the moment between the socket binding and the program starting would
	// otherwise race that setup.
	prog.Send(size)

	// The program has to let go of the terminal it holds before it can take
	// this one. Releasing before the output switches is what sends the
	// renderer's teardown to the right place: the discarded output on the
	// first attach, and the client being displaced on a takeover.
	h.releaseTerminal(prog)
	if previous != nil {
		previous.detach(reasonTakenOver)
		h.output.clear(previous)
	}

	result, err := json.Marshal(AttachResult{Attached: true, TookOver: previous != nil})
	if err != nil {
		h.handover.Unlock()
		h.detachClient(client, "")
		return
	}
	if err := ipc.WriteV2Response(conn, ipc.V2Response{ID: request.ID, OK: true, Result: result}); err != nil {
		h.handover.Unlock()
		h.detachClient(client, "")
		return
	}

	applog.Info("session: client attached (%dx%d, %s)", params.Width, params.Height, params.Client)
	h.output.set(client)
	if h.opts.OnAttach != nil {
		h.opts.OnAttach(params.Width, params.Height)
	}
	// The loop takes the next message only once it has finished the previous
	// one, view included, so the send after the size is a barrier: when it
	// returns, the model has taken the size and rendered the full UI at it.
	// The renderer then restarts against a view that is already the right
	// shape and emits the alternate screen, colors, and modes for this
	// terminal. The barrier doubles as the repaint request.
	prog.Send(size)
	prog.Send(tea.ClearScreen())
	h.restoreTerminal(prog)
	h.handover.Unlock()

	go h.reassertSize(client, prog)
	h.serve(client, prog)
}

// reassertSize resends the client's terminal size once the program is
// certainly past startup. See startupSizeGrace.
func (h *Host) reassertSize(client *clientConn, prog *tea.Program) {
	timer := time.NewTimer(startupSizeGrace)
	defer timer.Stop()
	<-timer.C
	h.mu.Lock()
	current := h.client == client
	h.mu.Unlock()
	if !current {
		return
	}
	width, height := client.size()
	prog.Send(tea.WindowSizeMsg{Width: width, Height: height})
}

// serve reads frames from one client until the session ends.
func (h *Host) serve(client *clientConn, prog *tea.Program) {
	// The peer is gone by the time this returns -- it sent a detach frame or
	// its connection failed -- so there is no reason to send it.
	defer h.detachClient(client, "")
	for {
		kind, payload, err := readFrame(client.conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && !client.isDetached() {
				applog.Warn("session: client read: %v", err)
			}
			return
		}
		switch kind {
		case kindInput:
			if _, err := h.inputW.Write(payload); err != nil {
				applog.Warn("session: forward input: %v", err)
				return
			}
		case kindResize:
			width, height, ok := decodeSize(payload)
			if !ok || width <= 0 || height <= 0 {
				continue
			}
			client.setSize(width, height)
			prog.Send(tea.WindowSizeMsg{Width: width, Height: height})
		case kindDetach:
			applog.Info("session: client detached")
			return
		default:
			// Unknown kinds are ignored so the protocol can grow without
			// breaking an older host.
		}
	}
}

// detachClient ends the session for client. A reason is sent to the client
// before the connection closes, so it is set when this side is the one
// leaving; a client that already went away gets none.
func (h *Host) detachClient(client *clientConn, reason string) {
	h.handover.Lock()
	h.mu.Lock()
	current := h.client == client
	if current {
		h.client = nil
	}
	prog := h.prog
	h.mu.Unlock()

	if current {
		// Releasing while the connection is still open is what puts the
		// client's terminal back: the renderer's teardown -- leave the
		// alternate screen, reset the theme colors it set, show the cursor,
		// drop the window title -- is written to the client that is still
		// there. It also has to happen before the model hears about the
		// detach, because that teardown describes the view still on screen.
		h.releaseTerminal(prog)
	}
	if reason != "" {
		client.detach(reason)
	}
	h.output.clear(client)
	client.close()
	h.handover.Unlock()

	if !current {
		return
	}
	if h.opts.OnDetach != nil {
		h.opts.OnDetach()
	}
}

// releaseTerminal stops the renderer, writing its teardown to whatever output
// is attached. Callers hold handover.
func (h *Host) releaseTerminal(prog *tea.Program) {
	if prog == nil || !h.restored {
		return
	}
	h.restored = false
	if err := prog.ReleaseTerminal(); err != nil {
		applog.Warn("session: release terminal: %v", err)
	}
}

// restoreTerminal restarts the renderer against the attached client's
// terminal. Callers hold handover.
func (h *Host) restoreTerminal(prog *tea.Program) {
	if prog == nil || h.restored {
		return
	}
	h.restored = true
	if err := prog.RestoreTerminal(); err != nil {
		applog.Warn("session: restore terminal: %v", err)
	}
}

// Detach hands the terminal back to the attached client and leaves the session
// running. It is what the player's detach key calls.
//
// The work runs on its own goroutine because the caller is the program's own
// update loop: releasing the terminal waits on that program, and the loop
// cannot wait on itself.
func (h *Host) Detach() {
	h.mu.Lock()
	client := h.client
	h.mu.Unlock()
	if client != nil {
		go h.detachClient(client, reasonDetached)
	}
}

// Close ends any live session and unblocks the program's input reader.
func (h *Host) Close() {
	h.mu.Lock()
	h.closed = true
	client := h.client
	h.client = nil
	h.mu.Unlock()
	if client != nil {
		client.detach(reasonHostStopped)
	}
	// Closing the write end gives the program's input reader a clean end of
	// input. The read end is left alone on purpose: closing a file another
	// goroutine is blocked reading is a race, and a host only closes when the
	// process is going away.
	_ = h.inputW.Close()
}

// clientConn serializes frame writes to one attached client.
type clientConn struct {
	conn net.Conn

	mu       sync.Mutex
	closed   bool
	detached bool
	width    int
	height   int
}

func (c *clientConn) setSize(width, height int) {
	c.mu.Lock()
	c.width, c.height = width, height
	c.mu.Unlock()
}

func (c *clientConn) size() (width, height int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.width, c.height
}

// write sends one frame under a deadline.
func (c *clientConn) write(kind byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return writeFrame(c.conn, kind, payload)
}

// detach tells the client the session is over, and why, then closes the
// connection, which unblocks the host's read loop.
func (c *clientConn) detach(reason string) {
	c.mu.Lock()
	if !c.closed {
		c.detached = true
		_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		_ = writeFrame(c.conn, kindDetach, []byte(reason))
		c.closed = true
		_ = c.conn.Close()
	}
	c.mu.Unlock()
}

func (c *clientConn) close() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		_ = c.conn.Close()
	}
	c.mu.Unlock()
}

func (c *clientConn) isDetached() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.detached
}

// outputSwitch routes the program's terminal output to the attached client,
// or nowhere. Writes never fail: a render error would end the program, and
// the program must outlive every client.
type outputSwitch struct {
	mu     sync.Mutex
	client *clientConn
}

func (o *outputSwitch) set(client *clientConn) {
	o.mu.Lock()
	o.client = client
	o.mu.Unlock()
}

// clear drops client if it is still the routed one.
func (o *outputSwitch) clear(client *clientConn) {
	o.mu.Lock()
	if o.client == client {
		o.client = nil
	}
	o.mu.Unlock()
}

func (o *outputSwitch) Write(p []byte) (int, error) {
	o.mu.Lock()
	client := o.client
	o.mu.Unlock()
	if client == nil || len(p) == 0 {
		return len(p), nil
	}
	if err := client.write(kindOutput, p); err != nil {
		// A client that cannot take the frame is gone as far as rendering is
		// concerned. Closing it unblocks the read loop, which detaches it.
		o.clear(client)
		client.close()
	}
	return len(p), nil
}
