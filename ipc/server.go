package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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
	done     chan struct{}
	wg       sync.WaitGroup
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

	ln, err := net.Listen("unix", sockPath)
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
	}

	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// Close shuts down the server, removes socket and PID file.
func (s *Server) Close() error {
	close(s.done)
	err := s.listener.Close()
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
				time.Sleep(100 * time.Millisecond)
				continue
			}
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

	// Set a deadline to prevent slow clients from tying up goroutines.
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	scanner := bufio.NewScanner(conn)

	for scanner.Scan() {
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
		select {
		case resp := <-reply:
			return resp
		case <-time.After(3 * time.Second):
			return Response{OK: false, Error: "load timeout"}
		case <-s.done:
			return Response{OK: false, Error: "server shutting down"}
		}

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
		select {
		case resp := <-reply:
			return resp
		case <-time.After(3 * time.Second):
			return Response{OK: false, Error: "theme timeout"}
		case <-s.done:
			return Response{OK: false, Error: "server shutting down"}
		}

	case "vis":
		if req.Name == "" {
			return Response{OK: false, Error: "vis requires a mode name"}
		}
		reply := make(chan Response, 1)
		s.disp.Send(VisMsg{Name: req.Name, Reply: reply})
		select {
		case resp := <-reply:
			return resp
		case <-time.After(3 * time.Second):
			return Response{OK: false, Error: "vis timeout"}
		case <-s.done:
			return Response{OK: false, Error: "server shutting down"}
		}

	case "shuffle":
		reply := make(chan Response, 1)
		s.disp.Send(ShuffleMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done)

	case "repeat":
		reply := make(chan Response, 1)
		s.disp.Send(RepeatMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done)

	case "mono":
		reply := make(chan Response, 1)
		s.disp.Send(MonoMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done)

	case "speed":
		if req.Value <= 0 {
			return Response{OK: false, Error: "speed must be positive"}
		}
		reply := make(chan Response, 1)
		s.disp.Send(SpeedMsg{Speed: req.Value, Reply: reply})
		return waitReply(reply, s.done)

	case "eq":
		reply := make(chan Response, 1)
		s.disp.Send(EQMsg{Name: req.Name, Band: req.Band, Value: req.Value, Reply: reply})
		return waitReply(reply, s.done)

	case "device":
		if req.Name == "" {
			return Response{OK: false, Error: "device requires a name (or 'list')"}
		}
		reply := make(chan Response, 1)
		s.disp.Send(DeviceMsg{Name: req.Name, Reply: reply})
		return waitReply(reply, s.done)

	case "status":
		return s.handleStatus()

	default:
		return Response{OK: false, Error: "unknown command: " + req.Cmd}
	}
}

// waitReply waits up to 3 seconds for a response on the reply channel.
func waitReply(reply chan Response, done chan struct{}) Response {
	select {
	case resp := <-reply:
		return resp
	case <-time.After(3 * time.Second):
		return Response{OK: false, Error: "timeout"}
	case <-done:
		return Response{OK: false, Error: "server shutting down"}
	}
}

// handleStatus sends a StatusRequestMsg to the TUI and waits for a response
// with a timeout.
func (s *Server) handleStatus() Response {
	reply := make(chan Response, 1)
	s.disp.Send(StatusRequestMsg{Reply: reply})

	select {
	case resp := <-reply:
		return resp
	case <-time.After(3 * time.Second):
		return Response{OK: false, Error: "status timeout"}
	case <-s.done:
		return Response{OK: false, Error: "server shutting down"}
	}
}

// writeResponse marshals a Response as JSON and writes it followed by a newline.
func writeResponse(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	data = append(data, '\n')
	conn.Write(data)
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

	proc, err := os.FindProcess(pid)
	if err != nil {
		// Can't find process — clean up.
		os.Remove(pidPath)
		os.Remove(sockPath)
		return nil
	}

	// Signal 0 checks if the process exists without actually sending a signal.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		// Process is dead — clean up stale files.
		os.Remove(pidPath)
		os.Remove(sockPath)
		return nil
	}

	return fmt.Errorf("ipc: cliamp is already running (pid %d)", pid)
}
