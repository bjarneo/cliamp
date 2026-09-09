package session

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/bjarneo/cliamp/ipc"
)

// hostedModel is a stand-in for the player UI: it renders what it was told and
// records the size it was given.
type hostedModel struct {
	key    string
	width  int
	height int
}

func (m hostedModel) Init() tea.Cmd { return nil }

func (m hostedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		m.key = msg.String()
		if m.key == "q" {
			return m, tea.Quit
		}
	}
	return m, nil
}

// View keeps the size and the last key on separate lines, and repeats the key
// so a repaint of the key line starts at its first column: the assertions in
// this file read the byte stream the client receives, not a terminal state.
func (m hostedModel) View() tea.View {
	key := m.key
	if key == "" {
		key = "-"
	}
	return tea.NewView(fmt.Sprintf("size=%dx%d\n%s", m.width, m.height, strings.Repeat(key, 5)))
}

// hostedProgram starts a host with a live program, as run() wires them.
func hostedProgram(t *testing.T, opts Options) (*Host, func()) {
	t.Helper()
	host, start, stop := hostedProgramDeferred(t, opts)
	start()
	return host, stop
}

// hostedProgramDeferred is hostedProgram with the program's start under the
// test's control, so a test can attach before the program is running.
func hostedProgramDeferred(t *testing.T, opts Options) (host *Host, start func(), stop func()) {
	t.Helper()
	host = New(opts)
	prog := tea.NewProgram(hostedModel{},
		tea.WithInput(host.Input()),
		tea.WithOutput(host.Output()),
		tea.WithColorProfile(colorprofile.Ascii),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithWindowSize(80, 24),
		tea.WithoutSignals(),
	)
	host.SetProgram(prog)
	done := make(chan struct{})
	start = func() {
		go func() {
			defer close(done)
			_, _ = prog.Run()
		}()
	}
	stop = func() {
		prog.Quit()
		host.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("program did not exit")
		}
	}
	return host, start, stop
}

// attachConn is the client half of an attach session.
type attachConn struct {
	conn   net.Conn
	frames chan frame
	err    chan error
}

type frame struct {
	kind    byte
	payload []byte
}

// attach performs the handshake and starts reading frames.
func attach(t *testing.T, host *Host, width, height int) (*attachConn, ipc.V2Response) {
	t.Helper()
	clientSide, hostSide := net.Pipe()
	params, err := json.Marshal(AttachParams{Width: width, Height: height, Client: "test"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	go host.HandleAttach(hostSide, ipc.V2Request{ID: json.RawMessage(`"a"`), Params: params})

	client := &attachConn{conn: clientSide, frames: make(chan frame, 256), err: make(chan error, 1)}
	_ = clientSide.SetDeadline(time.Now().Add(5 * time.Second))
	line, err := readLine(clientSide)
	if err != nil {
		t.Fatalf("read acknowledgment: %v", err)
	}
	var response ipc.V2Response
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode acknowledgment: %v", err)
	}
	_ = clientSide.SetDeadline(time.Time{})
	if response.OK {
		go client.read()
	}
	return client, response
}

func (c *attachConn) read() {
	for {
		kind, payload, err := readFrame(c.conn)
		if err != nil {
			c.err <- err
			close(c.frames)
			return
		}
		c.frames <- frame{kind: kind, payload: payload}
	}
}

// waitForOutput collects output frames until they contain want.
func (c *attachConn) waitForOutput(t *testing.T, want string) string {
	t.Helper()
	var rendered strings.Builder
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				t.Fatalf("session ended before rendering %q; got %q", want, rendered.String())
			}
			if f.kind == kindOutput {
				rendered.Write(f.payload)
				if strings.Contains(rendered.String(), want) {
					return rendered.String()
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q; got %q", want, rendered.String())
		}
	}
}

func (c *attachConn) send(t *testing.T, kind byte, payload []byte) {
	t.Helper()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := writeFrame(c.conn, kind, payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func readLine(conn net.Conn) ([]byte, error) {
	line := make([]byte, 0, 256)
	var one [1]byte
	for {
		n, err := conn.Read(one[:])
		if n == 1 {
			if one[0] == '\n' {
				return line, nil
			}
			line = append(line, one[0])
			continue
		}
		if err != nil {
			return nil, err
		}
	}
}

// A client attaching gets a repaint at its own size, and its keystrokes reach
// the program.
func TestAttachRendersAndForwardsInput(t *testing.T) {
	var attached, detached int
	var mu sync.Mutex
	host, stop := hostedProgram(t, Options{
		OnAttach: func(int, int) { mu.Lock(); attached++; mu.Unlock() },
		OnDetach: func() { mu.Lock(); detached++; mu.Unlock() },
	})
	defer stop()

	client, response := attach(t, host, 90, 30)
	if !response.OK {
		t.Fatalf("attach failed: %v", response.Error)
	}
	var result AttachResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.Attached || result.TookOver {
		t.Fatalf("result = %+v, want a first attach", result)
	}

	client.waitForOutput(t, "size=90x30")
	client.send(t, kindInput, []byte("x"))
	client.waitForOutput(t, "xxxxx")

	// A resize reaches the program without a reattach.
	client.send(t, kindResize, encodeSize(70, 20))
	client.waitForOutput(t, "size=70x20")

	client.send(t, kindDetach, nil)
	waitFor(t, "detach", func() bool { return !host.Attached() })
	mu.Lock()
	defer mu.Unlock()
	if attached != 1 || detached != 1 {
		t.Errorf("attach/detach callbacks = %d/%d, want 1/1", attached, detached)
	}
}

// The program keeps running with nobody attached, and the next client gets a
// full repaint rather than the tail of a diff it never saw.
func TestReattachRepaintsFromScratch(t *testing.T) {
	host, stop := hostedProgram(t, Options{})
	defer stop()

	first, response := attach(t, host, 90, 30)
	if !response.OK {
		t.Fatalf("attach failed: %v", response.Error)
	}
	first.waitForOutput(t, "size=90x30")
	first.send(t, kindInput, []byte("z"))
	first.waitForOutput(t, "zzzzz")
	first.send(t, kindDetach, nil)
	waitFor(t, "detach", func() bool { return !host.Attached() })

	second, response := attach(t, host, 100, 40)
	if !response.OK {
		t.Fatalf("reattach failed: %v", response.Error)
	}
	// The state the first client produced survived the detach, and the new
	// terminal gets it in full rather than as the tail of a diff.
	rendered := second.waitForOutput(t, "zzzzz")
	if !strings.Contains(rendered, "size=100x40") {
		t.Errorf("repaint = %q, want the new terminal size", rendered)
	}
}

// One program has one renderer, so a second client takes the terminal over and
// the first is told the session moved.
func TestAttachTakeoverDetachesPreviousClient(t *testing.T) {
	host, stop := hostedProgram(t, Options{})
	defer stop()

	first, response := attach(t, host, 90, 30)
	if !response.OK {
		t.Fatalf("attach failed: %v", response.Error)
	}
	first.waitForOutput(t, "size=90x30")

	second, response := attach(t, host, 100, 40)
	if !response.OK {
		t.Fatalf("second attach failed: %v", response.Error)
	}
	var result AttachResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.TookOver {
		t.Error("second attach did not report the takeover")
	}
	second.waitForOutput(t, "size=100x40")

	// The displaced client is told why rather than just losing its terminal.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-first.frames:
			if !ok {
				t.Fatal("first client stream closed without a detach reason")
			}
			if f.kind != kindDetach {
				continue
			}
			if string(f.payload) != reasonTakenOver {
				t.Errorf("detach reason = %q, want %q", f.payload, reasonTakenOver)
			}
			return
		case <-deadline:
			t.Fatal("first client was not detached")
		}
	}
}

// Bubble Tea announces its own initial size asynchronously, so a client that
// attaches while the program is still starting can see that placeholder land
// after its own size. The session must still end up at the client's size.
func TestAttachDuringStartupKeepsClientSize(t *testing.T) {
	host, start, stop := hostedProgramDeferred(t, Options{})
	defer stop()

	attached := make(chan *attachConn, 1)
	go func() {
		client, response := attach(t, host, 90, 30)
		if !response.OK {
			t.Errorf("attach failed: %v", response.Error)
		}
		attached <- client
	}()
	// Let the handshake and its size message queue up ahead of the program.
	time.Sleep(50 * time.Millisecond)
	start()

	client := <-attached
	// The size lands as a patch of the line the placeholder frame already
	// drew, so the stream carries the new value rather than a whole line.
	client.waitForOutput(t, "90x30")
}

func TestAttachRejectsMissingSize(t *testing.T) {
	host, stop := hostedProgram(t, Options{})
	defer stop()

	clientSide, hostSide := net.Pipe()
	go host.HandleAttach(hostSide, ipc.V2Request{ID: json.RawMessage(`"a"`), Params: json.RawMessage(`{"width":0,"height":0}`)})
	_ = clientSide.SetDeadline(time.Now().Add(5 * time.Second))
	line, err := readLine(clientSide)
	if err != nil {
		t.Fatalf("read acknowledgment: %v", err)
	}
	var response ipc.V2Response
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode acknowledgment: %v", err)
	}
	if response.OK {
		t.Fatal("attach accepted a session with no terminal size")
	}
	if response.Error == nil || response.Error.Code != ipc.V2ErrorCodeInvalidParams {
		t.Errorf("error = %+v, want invalid_params", response.Error)
	}
	if host.Attached() {
		t.Error("host attached a rejected client")
	}
	_ = clientSide.Close()
}

func TestInputPipeBlocksUntilFedAndEndsOnClose(t *testing.T) {
	pipe := newInputPipe()

	read := make(chan string, 1)
	go func() {
		buf := make([]byte, 16)
		n, err := pipe.Read(buf)
		if err != nil {
			read <- "error: " + err.Error()
			return
		}
		read <- string(buf[:n])
	}()

	select {
	case got := <-read:
		t.Fatalf("Read returned %q before any input", got)
	case <-time.After(50 * time.Millisecond):
	}

	pipe.feed([]byte("hey"))
	select {
	case got := <-read:
		if got != "hey" {
			t.Errorf("Read = %q, want %q", got, "hey")
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not wake on input")
	}

	// Keystrokes typed at a terminal that has gone away belong to nobody.
	pipe.feed([]byte("stale"))
	pipe.reset()
	pipe.close()
	buf := make([]byte, 16)
	if _, err := pipe.Read(buf); err != io.EOF {
		t.Errorf("Read after close = %v, want EOF", err)
	}
}

func TestInputPipeDropsFloodBeyondLimit(t *testing.T) {
	pipe := newInputPipe()
	pipe.feed(make([]byte, inputBufferLimit+4096))

	total := 0
	buf := make([]byte, 8192)
	for total < inputBufferLimit {
		n, err := pipe.Read(buf)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		total += n
	}
	pipe.close()
	if n, err := pipe.Read(buf); n != 0 || err != io.EOF {
		t.Errorf("buffered %d bytes past the limit", total+n)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
