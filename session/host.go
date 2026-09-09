package session

import (
	"encoding/json"
	"errors"
	"io"
	"net"
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

// inputBufferLimit bounds pending client input. Input is keystrokes; a peer
// flooding past this limit has its excess dropped rather than growing the
// host's memory.
const inputBufferLimit = 64 * 1024

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
type Host struct {
	opts   Options
	input  *inputPipe
	output *outputSwitch

	mu     sync.Mutex
	prog   *tea.Program
	client *clientConn
	closed bool
}

// New creates a detached host. Wire Input and Output into the Bubble Tea
// program, then hand the program back with SetProgram.
func New(opts Options) *Host {
	return &Host{
		opts:   opts,
		input:  newInputPipe(),
		output: &outputSwitch{},
	}
}

// Input is the program's terminal input. It blocks while no client is
// attached rather than reporting end of input, so one program serves every
// attach for the life of the process.
func (h *Host) Input() io.Reader { return h.input }

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

	h.mu.Lock()
	if h.closed || h.prog == nil {
		h.mu.Unlock()
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

	if previous != nil {
		previous.detach(reasonTakenOver)
	}

	result, err := json.Marshal(AttachResult{Attached: true, TookOver: previous != nil})
	if err != nil {
		h.clearClient(client)
		return
	}
	if err := ipc.WriteV2Response(conn, ipc.V2Response{ID: request.ID, OK: true, Result: result}); err != nil {
		h.clearClient(client)
		return
	}

	applog.Info("session: client attached (%dx%d, %s)", params.Width, params.Height, params.Client)
	h.input.reset()
	h.output.set(client)
	if h.opts.OnAttach != nil {
		h.opts.OnAttach(params.Width, params.Height)
	}
	prog.Send(tea.WindowSizeMsg{Width: params.Width, Height: params.Height})
	// The program has been rendering into a discarded stream, so its renderer
	// believes the client's terminal already shows the current frame. Force a
	// full repaint for the terminal that just arrived.
	prog.Send(tea.ClearScreen())
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
	defer h.clearClient(client)
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
			h.input.feed(payload)
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

// clearClient disconnects client if it is still the attached one.
func (h *Host) clearClient(client *clientConn) {
	h.mu.Lock()
	current := h.client == client
	if current {
		h.client = nil
	}
	h.mu.Unlock()
	h.output.clear(client)
	client.close()
	if !current {
		return
	}
	h.input.reset()
	if h.opts.OnDetach != nil {
		h.opts.OnDetach()
	}
}

// Detach hands the terminal back to the attached client and leaves the session
// running. It is what the player's detach key calls.
func (h *Host) Detach() {
	h.mu.Lock()
	client := h.client
	h.mu.Unlock()
	if client != nil {
		client.detach(reasonDetached)
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
	h.input.close()
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

// inputPipe is the program's terminal input across attaches. Read blocks
// while empty and reports end of input only after close, so the program's
// input reader survives every detach.
type inputPipe struct {
	mu     sync.Mutex
	wake   *sync.Cond
	buf    []byte
	closed bool
}

func newInputPipe() *inputPipe {
	pipe := &inputPipe{}
	pipe.wake = sync.NewCond(&pipe.mu)
	return pipe
}

func (p *inputPipe) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && !p.closed {
		p.wake.Wait()
	}
	if len(p.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(dst, p.buf)
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	return n, nil
}

func (p *inputPipe) feed(data []byte) {
	if len(data) == 0 {
		return
	}
	p.mu.Lock()
	if !p.closed && len(p.buf) < inputBufferLimit {
		room := inputBufferLimit - len(p.buf)
		if len(data) > room {
			data = data[:room]
		}
		p.buf = append(p.buf, data...)
		p.wake.Broadcast()
	}
	p.mu.Unlock()
}

// reset drops input that arrived before an attach or after a detach: those
// keystrokes belong to a terminal that is no longer watching.
func (p *inputPipe) reset() {
	p.mu.Lock()
	p.buf = nil
	p.mu.Unlock()
}

func (p *inputPipe) close() {
	p.mu.Lock()
	p.closed = true
	p.wake.Broadcast()
	p.mu.Unlock()
}
