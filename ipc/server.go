package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cliamp/applog"
	"cliamp/internal/playback"
)

// Dispatcher is how the server sends commands to the TUI.
// In main.go, this is wired to prog.Send().
type Dispatcher interface {
	Send(msg any)
}

// Server listens on a Unix socket and dispatches IPC commands.
type Server struct {
	listener net.Listener
	sockPath string
	disp     Dispatcher
	plugins  PluginDispatcher
	done     chan struct{}
	wg       sync.WaitGroup

	connMu sync.Mutex
	conns  map[net.Conn]struct{} // live connections, closed on shutdown
}

// addConn registers a live connection. It returns false if the server is
// already shutting down, in which case the caller must close the connection
// and return. The done check shares connMu with closeConns so a connection
// accepted during shutdown is always closed by exactly one of them.
func (s *Server) addConn(c net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	select {
	case <-s.done:
		return false
	default:
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) removeConn(c net.Conn) {
	s.connMu.Lock()
	delete(s.conns, c)
	s.connMu.Unlock()
}

func (s *Server) closeConns() {
	s.connMu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.connMu.Unlock()
}

// SetPluginDispatcher wires in the Lua plugin manager after the server starts.
// Plugin dispatch is optional — without it, plugin subcommands return an error.
func (s *Server) SetPluginDispatcher(p PluginDispatcher) {
	s.plugins = p
}

// NewServer creates and starts the IPC server. It cleans up stale sockets
// before binding. The socket is created with 0600 permissions (owner only).
func NewServer(sockPath string, disp Dispatcher) (*Server, error) {
	if err := cleanStaleSocket(sockPath); err != nil {
		return nil, err
	}

	// Ensure the parent directory exists.
	if err := os.MkdirAll(filepath.Dir(sockPath), 0700); err != nil {
		return nil, fmt.Errorf("ipc: mkdir: %w", err)
	}

	ln, err := listenSocket(sockPath)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen: %w", err)
	}

	// Restrict socket permissions to owner only.
	if err := os.Chmod(sockPath, 0600); err != nil {
		ln.Close()
		os.Remove(sockPath)
		return nil, fmt.Errorf("ipc: chmod: %w", err)
	}

	// Write PID file.
	pidPath := sockPath + ".pid"
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		ln.Close()
		os.Remove(sockPath)
		return nil, fmt.Errorf("ipc: write pid: %w", err)
	}

	s := &Server{
		listener: ln,
		sockPath: sockPath,
		disp:     disp,
		done:     make(chan struct{}),
		conns:    make(map[net.Conn]struct{}),
	}

	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// Close shuts down the server, removes socket and PID file.
func (s *Server) Close() error {
	close(s.done)
	err := s.listener.Close()
	// Close in-flight connections so their handleConn read loops unblock
	// immediately rather than waiting out the per-request read deadline.
	s.closeConns()
	s.wg.Wait()
	os.Remove(s.sockPath)
	os.Remove(s.sockPath + ".pid")
	return err
}

// acceptLoop accepts incoming connections until the server is closed.
func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			// A closed listener is permanent — stop instead of spinning.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Other errors may be transient (e.g. EMFILE); log and back off
			// rather than silently retrying.
			applog.Warn("ipc: accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

// handleConn reads newline-delimited JSON requests from a single connection,
// dispatches them, and writes JSON responses.
func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	if !s.addConn(conn) {
		return // server shutting down
	}
	defer s.removeConn(conn)

	scanner := bufio.NewScanner(conn)

	for {
		// Per-request deadline so long-lived streaming clients (e.g. vis bands
		// polling) aren't killed at a fixed wall clock, but idle clients still
		// time out.
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		if !scanner.Scan() {
			return
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			writeResponse(conn, Response{OK: false, Error: "invalid JSON: " + err.Error()})
			continue
		}

		resp := s.dispatch(req)
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		writeResponse(conn, resp)
	}
}

// dispatch handles a single parsed request.
func (s *Server) dispatch(req Request) Response {
	switch strings.ToLower(req.Cmd) {
	case "play":
		s.disp.Send(PlayMsg{})
		return Response{OK: true}

	case "pause":
		s.disp.Send(PauseMsg{})
		return Response{OK: true}

	case "toggle":
		s.disp.Send(playback.PlayPauseMsg{})
		return Response{OK: true}

	case "stop":
		s.disp.Send(playback.StopMsg{})
		return Response{OK: true}

	case "next":
		s.disp.Send(playback.NextMsg{})
		return Response{OK: true}

	case "prev":
		s.disp.Send(playback.PrevMsg{})
		return Response{OK: true}

	case "volume":
		s.disp.Send(VolumeMsg{DB: req.Value})
		return Response{OK: true}

	case "seek":
		s.disp.Send(SeekMsg{Offset: time.Duration(req.Value * float64(time.Second))})
		return Response{OK: true}

	case "load":
		if req.Playlist == "" {
			return Response{OK: false, Error: "load requires a playlist name"}
		}
		reply := make(chan Response, 1)
		s.disp.Send(LoadMsg{Playlist: req.Playlist, Reply: reply})
		return waitReply(reply, s.done, "load", 3*time.Second)

	case "queue":
		if req.Path == "" {
			return Response{OK: false, Error: "queue requires a path"}
		}
		s.disp.Send(QueueMsg{Path: req.Path})
		return Response{OK: true}

	case "theme":
		if req.Name == "" {
			return Response{OK: false, Error: "theme requires a name"}
		}
		reply := make(chan Response, 1)
		s.disp.Send(ThemeMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done, "theme", 3*time.Second)

	case "vis":
		if req.Name == "" {
			return Response{OK: false, Error: "vis requires a mode name"}
		}
		reply := make(chan Response, 1)
		s.disp.Send(VisMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done, "vis", 3*time.Second)

	case "shuffle":
		reply := make(chan Response, 1)
		s.disp.Send(ShuffleMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done, "shuffle", 3*time.Second)

	case "repeat":
		reply := make(chan Response, 1)
		s.disp.Send(RepeatMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done, "repeat", 3*time.Second)

	case "mono":
		reply := make(chan Response, 1)
		s.disp.Send(MonoMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done, "mono", 3*time.Second)

	case "speed":
		if req.Value <= 0 {
			return Response{OK: false, Error: "speed must be positive"}
		}
		reply := make(chan Response, 1)
		s.disp.Send(SpeedMsg{Speed: req.Value, Reply: reply})
		return waitReply(reply, s.done, "speed", 3*time.Second)

	case "eq":
		reply := make(chan Response, 1)
		s.disp.Send(EQMsg{Name: req.Name, Band: req.Band, Value: req.Value, Reply: reply})
		return waitReply(reply, s.done, "eq", 3*time.Second)

	case "device":
		if req.Name == "" {
			return Response{OK: false, Error: "device requires a name (or 'list')"}
		}
		reply := make(chan Response, 1)
		s.disp.Send(DeviceMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done, "device", 3*time.Second)

	case "status":
		return s.handleStatus()

	case "bands":
		reply := make(chan Response, 1)
		s.disp.Send(BandsRequestMsg{Reply: reply})
		return waitReply(reply, s.done, "bands", 1*time.Second)

	case "plugin.call":
		if s.plugins == nil {
			return Response{OK: false, Error: "plugins not enabled"}
		}
		if req.Name == "" || req.Sub == "" {
			return Response{OK: false, Error: "plugin.call requires plugin name and subcommand"}
		}
		out, err := s.plugins.EmitCommand(req.Name, req.Sub, req.Args)
		if err != nil {
			return Response{OK: false, Error: err.Error()}
		}
		return Response{OK: true, Output: out}

	case "plugin.commands":
		if s.plugins == nil {
			return Response{OK: false, Error: "plugins not enabled"}
		}
		return Response{OK: true, Items: s.plugins.CommandList()}

	default:
		return Response{OK: false, Error: "unknown command: " + req.Cmd}
	}
}

// waitReply waits up to timeout for a response on the reply channel, returning
// a "<label> timeout" error if it elapses or a shutdown error if the server
// closes first.
func waitReply(reply chan Response, done chan struct{}, label string, timeout time.Duration) Response {
	select {
	case resp := <-reply:
		return resp
	case <-time.After(timeout):
		return Response{OK: false, Error: label + " timeout"}
	case <-done:
		return Response{OK: false, Error: "server shutting down"}
	}
}

// handleStatus sends a StatusRequestMsg to the TUI and waits for a response
// with a timeout.
func (s *Server) handleStatus() Response {
	reply := make(chan Response, 1)
	s.disp.Send(StatusRequestMsg{Reply: reply})
	return waitReply(reply, s.done, "status", 3*time.Second)
}

// writeResponse marshals a Response as JSON and writes it followed by a newline.
func writeResponse(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		applog.Warn("ipc: write response: %v", err)
	}
}

// cleanStaleSocket removes a leftover socket and PID file from a dead process.
// If the PID file exists and the process is still alive, it returns an error.
func cleanStaleSocket(sockPath string) error {
	pidPath := sockPath + ".pid"
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		// No PID file — remove socket if it exists (orphan from crash).
		os.Remove(sockPath)
		return nil
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		// Corrupt PID file — clean up.
		os.Remove(pidPath)
		os.Remove(sockPath)
		return nil
	}

	alive, err := processAlive(pid)
	if err != nil {
		return fmt.Errorf("checking process liveness for socket %s: %w", sockPath, err)
	}
	if !alive {
		// Process is dead — clean up stale files.
		os.Remove(pidPath)
		os.Remove(sockPath)
		return nil
	}

	return fmt.Errorf("ipc: cliamp is already running (pid %d)", pid)
}
