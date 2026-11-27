package ipc

import (
	"bytes"
	"context"
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

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/fileutil"
)

const (
	ipcRequestReadTimeout = 60 * time.Second
	ipcWriteTimeout       = 5 * time.Second
	maxConcurrentConns    = 64
	maxConsecutiveEmpty   = 16
)

// ServerOption configures a Server before it accepts connections.
type ServerOption func(*serverConfig)

type serverConfig struct {
	broker     *Broker
	dispatcher V2Dispatcher
	operations *OperationRegistry
	jobs       *JobStore
}

// WithJobStore configures the JobStore before the server accepts connections.
func WithJobStore(jobs *JobStore) ServerOption {
	return func(cfg *serverConfig) {
		cfg.jobs = jobs
	}
}

// WithBroker configures the Server to use broker. The caller keeps ownership of broker.
func WithBroker(broker *Broker) ServerOption {
	return func(cfg *serverConfig) {
		cfg.broker = broker
	}
}

// WithV2Dispatcher configures the V2Dispatcher before the server accepts connections.
func WithV2Dispatcher(dispatcher V2Dispatcher) ServerOption {
	return func(cfg *serverConfig) {
		cfg.dispatcher = dispatcher
	}
}

// WithOperationRegistry configures the OperationRegistry before the server accepts connections.
func WithOperationRegistry(registry *OperationRegistry) ServerOption {
	return func(cfg *serverConfig) {
		cfg.operations = registry
	}
}

// Server listens on a Unix socket and dispatches IPC commands.
type Server struct {
	listener    net.Listener
	sockPath    string
	socketInfo  os.FileInfo
	pidInfo     os.FileInfo
	broker      *Broker
	brokerOwned bool

	v2Mu       sync.RWMutex
	v2         V2Dispatcher
	operations *OperationRegistry
	jobs       *JobStore
	context    context.Context
	cancel     context.CancelFunc

	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error

	connMu sync.Mutex
	conns  map[net.Conn]struct{} // live connections, closed on shutdown
}

// addConn registers a live connection. It returns false when the server is
// shutting down or at the concurrent connection limit.
func (s *Server) addConn(c net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	select {
	case <-s.done:
		return false
	default:
	}
	if len(s.conns) >= maxConcurrentConns {
		return false
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

// SetV2Dispatcher wires the runtime owner for version 2 requests. V2 remains
// available for capability discovery and subscriptions when it is nil.
func (s *Server) SetV2Dispatcher(dispatcher V2Dispatcher) {
	s.v2Mu.Lock()
	s.v2 = dispatcher
	s.v2Mu.Unlock()
}

// SetOperationRegistry replaces the advertised V2 capability set. It should
// be called during runtime setup before accepting client traffic.
func (s *Server) SetOperationRegistry(registry *OperationRegistry) {
	s.v2Mu.Lock()
	s.operations = registry
	s.v2Mu.Unlock()
}

// JobStore returns the server's in-memory V2 job store.
func (s *Server) JobStore() *JobStore {
	return s.jobs
}

// Broker returns the server event broker. Callers may publish runtime events
// but must not close a broker they do not own.
func (s *Server) Broker() *Broker {
	return s.broker
}

// Done closes when the server begins shutdown.
func (s *Server) Done() <-chan struct{} {
	return s.done
}

// NewServer creates and starts the IPC server with a new event broker.
func NewServer(sockPath string) (*Server, error) {
	return NewServerWithOptions(sockPath)
}

// NewServerWithBroker creates and starts the IPC server using broker. The
// caller retains ownership of a supplied broker and it is not closed by Server.
func NewServerWithBroker(sockPath string, broker *Broker) (*Server, error) {
	return NewServerWithOptions(sockPath, WithBroker(broker))
}

// NewServerWithOptions creates and starts the IPC server with options applied
// before the server accepts connections.
func NewServerWithOptions(sockPath string, options ...ServerOption) (*Server, error) {
	cfg := serverConfig{
		operations: DefaultOperationRegistry(),
	}
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	brokerOwned := false
	if cfg.broker == nil {
		cfg.broker = NewBroker()
		brokerOwned = true
	}
	return newServer(sockPath, cfg, brokerOwned)
}

func newServer(sockPath string, cfg serverConfig, brokerOwned bool) (*Server, error) {
	if err := checkSocketPath(sockPath); err != nil {
		return nil, fmt.Errorf("ipc: %w", err)
	}

	if err := secureSocketDir(filepath.Dir(sockPath)); err != nil {
		return nil, err
	}

	// Serialize the first bind, socket permissions, and PID publication.
	// Otherwise another starter could mistake a bound but not yet listening
	// socket for a stale path and unlink it.
	unlock, err := lockSocketStale(sockPath)
	if err != nil {
		return nil, fmt.Errorf("ipc: socket lock: %w", err)
	}
	defer func() { _ = unlock() }()

	ln, err := listenExclusive(sockPath)
	if err != nil {
		// Listening and removeStaleSocket already include the ipc: prefix.
		if strings.HasPrefix(err.Error(), "ipc: ") {
			return nil, err
		}
		return nil, fmt.Errorf("ipc: listen: %w", err)
	}
	// net.UnixListener.Close normally unlinks its path without checking
	// ownership. A different server may now own that pathname.
	if unixListener, ok := ln.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	socketInfo, err := os.Lstat(sockPath)
	if err != nil {
		_ = ln.Close()
		_ = removeStaleSocket(sockPath)
		return nil, fmt.Errorf("ipc: stat socket: %w", err)
	}

	// Restrict socket permissions to owner only.
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = ln.Close()
		_ = removeStaleSocket(sockPath)
		return nil, fmt.Errorf("ipc: chmod: %w", err)
	}

	// Publish a new PID inode without following symlinks, so a previous
	// instance can distinguish its PID file from a replacement with the same
	// numeric PID.
	pidInfo, err := publishPID(sockPath + ".pid")
	if err != nil {
		_ = ln.Close()
		_ = removeStaleSocket(sockPath)
		return nil, fmt.Errorf("ipc: write pid: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	jobs := cfg.jobs
	if jobs == nil {
		jobs = NewJobStore()
	}
	s := &Server{
		listener:    ln,
		sockPath:    sockPath,
		socketInfo:  socketInfo,
		pidInfo:     pidInfo,
		broker:      cfg.broker,
		brokerOwned: brokerOwned,
		v2:          cfg.dispatcher,
		operations:  cfg.operations,
		jobs:        jobs,
		context:     ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		conns:       make(map[net.Conn]struct{}),
	}

	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// Close shuts down the server and removes only its own socket and PID file.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.jobs != nil {
			s.jobs.CancelAll()
		}
		if s.done != nil {
			close(s.done)
		}
		if s.listener != nil {
			s.closeErr = s.listener.Close()
		}
		// Close active connections so read loops exit without waiting for deadlines.
		s.closeConns()
		s.wg.Wait()
		if s.brokerOwned && s.broker != nil {
			s.broker.Close()
		}
		if s.sockPath != "" {
			removeOwnedSocket(s.sockPath, s.socketInfo, s.pidInfo)
		}
	})
	return s.closeErr
}

// publishPID atomically replaces the stale PID file with a new inode,
// refusing to overwrite symlinks or non-regular files at pidPath.
func publishPID(pidPath string) (os.FileInfo, error) {
	if info, err := os.Lstat(pidPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("pid file %s is not a regular file", pidPath)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(pidPath), ".cliamp-pid-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		_ = f.Close()
		return nil, err
	}
	pidInfo, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(f.Name(), pidPath); err != nil {
		return nil, err
	}
	return pidInfo, nil
}

// removeOwnedSocket uses the same stale-socket lock as startup, and only
// unlinks a socket whose file identity still belongs to this server. Socket
// ownership also gates removal of the PID file. The PID inode must match as
// well, since another process may have replaced it independently.
func removeOwnedSocket(sockPath string, socketInfo, pidInfo os.FileInfo) {
	if socketInfo == nil {
		return
	}
	unlock, err := lockSocketStale(sockPath)
	if err != nil {
		applog.Warn("ipc: lock socket cleanup: %v", err)
		return
	}
	defer func() { _ = unlock() }()

	current, err := os.Lstat(sockPath)
	if err != nil || !os.SameFile(current, socketInfo) {
		return
	}
	if err := os.Remove(sockPath); err != nil {
		applog.Warn("ipc: remove socket: %v", err)
		return
	}
	if pidInfo != nil {
		currentPID, err := os.Lstat(sockPath + ".pid")
		if err == nil && os.SameFile(currentPID, pidInfo) {
			if err := os.Remove(sockPath + ".pid"); err != nil {
				applog.Warn("ipc: remove pid file: %v", err)
			}
		}
	}
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
			// A closed listener is permanent. Stop instead of spinning.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			applog.Warn("ipc: accept: %v", err)
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-s.done:
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		if !s.addConn(conn) {
			_ = conn.Close()
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
	defer s.removeConn(conn)

	if err := verifyPeerCred(conn); err != nil {
		applog.Warn("ipc: reject peer: %v", err)
		return
	}

	scanner := newFrameScanner(conn)
	emptyLines := 0

	for {
		conn.SetReadDeadline(time.Now().Add(ipcRequestReadTimeout))
		if !scanner.Scan() {
			return
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			emptyLines++
			if emptyLines >= maxConsecutiveEmpty {
				return
			}
			continue
		}
		emptyLines = 0

		req, protoErr := decodeV2Request(line)
		if protoErr != nil {
			conn.SetWriteDeadline(time.Now().Add(ipcWriteTimeout))
			_ = writeJSONLine(conn, V2Response{ID: req.ID, OK: false, Error: protoErr})
			continue
		}
		if isV2Subscribe(req) {
			s.streamV2Subscription(conn, req)
			return
		}

		resp := s.dispatchV2(req)
		conn.SetWriteDeadline(time.Now().Add(ipcWriteTimeout))
		_ = writeV2Response(conn, resp)
	}
}

func (s *Server) streamV2Subscription(conn net.Conn, req V2Request) {
	s.streamSubscription(conn, req.Topics, V2Response{ID: req.ID, OK: true})
}

func (s *Server) streamSubscription(conn net.Conn, topics []string, acknowledgement V2Response) {
	// Clear the per-request read deadline so idle subscriptions stay open.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		s.writeSubscriptionError(conn, acknowledgement)
		return
	}

	if s.broker == nil {
		s.writeSubscriptionError(conn, acknowledgement)
		return
	}
	sub, err := s.broker.Subscribe(topics)
	if err != nil {
		s.writeSubscriptionError(conn, acknowledgement)
		return
	}
	defer sub.Close()
	conn.SetWriteDeadline(time.Now().Add(ipcWriteTimeout))
	if !writeJSONLine(conn, acknowledgement) {
		return
	}

	// Keep a pending read to detect peer disconnects when no events publish.
	peerClosed := make(chan struct{})
	go func() {
		defer close(peerClosed)
		var one [1]byte
		_, _ = conn.Read(one[:])
	}()
	defer func() {
		_ = conn.SetReadDeadline(time.Now())
		_ = conn.Close()
		<-peerClosed
	}()

	for {
		select {
		case <-s.done:
			return
		case <-peerClosed:
			return
		case event, ok := <-sub.Events():
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(ipcWriteTimeout))
			if !writeEventLine(conn, event) {
				return
			}
		}
	}
}

func (s *Server) writeSubscriptionError(conn net.Conn, response V2Response) {
	response.OK = false
	response.Error = invalidV2Params()
	_ = conn.SetWriteDeadline(time.Now().Add(ipcWriteTimeout))
	_ = writeJSONLine(conn, response)
}

type v2RequestEnvelope struct {
	Version   json.RawMessage `json:"version"`
	ID        json.RawMessage `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Operation string          `json:"operation,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	JobID     string          `json:"job_id,omitempty"`
	Topics    []string        `json:"topics,omitempty"`
}

// decodeV2Request decodes one incoming frame in a single pass and validates
// the protocol version field.
func decodeV2Request(line []byte) (V2Request, *V2Error) {
	var env v2RequestEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		if len(env.Version) > 0 && !isProtocolVersion2(env.Version) {
			return V2Request{ID: env.ID}, v2Error(V2ErrorCodeInvalidVersion, V2MessageInvalidVersion)
		}
		return V2Request{ID: env.ID}, invalidV2Request()
	}
	if len(env.Version) == 0 || !isProtocolVersion2(env.Version) {
		return V2Request{ID: env.ID}, v2Error(V2ErrorCodeInvalidVersion, V2MessageInvalidVersion)
	}
	return V2Request{
		Version:   protocolVersion2,
		ID:        env.ID,
		Method:    env.Method,
		Operation: env.Operation,
		Params:    env.Params,
		JobID:     env.JobID,
		Topics:    env.Topics,
	}, nil
}

func isProtocolVersion2(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("2"))
}

func isV2Subscribe(req V2Request) bool {
	return strings.EqualFold(req.Method, "subscribe") || strings.EqualFold(req.Operation, "subscribe")
}

func (s *Server) dispatchV2(req V2Request) V2Response {
	response := V2Response{ID: req.ID}
	method := strings.ToLower(strings.TrimSpace(req.Method))
	s.v2Mu.RLock()
	operations := s.operations
	s.v2Mu.RUnlock()
	operation := strings.TrimSpace(req.Operation)
	if operation == "" && (method == "state.get" || method == "spectrum.get") {
		// The owner matches the canonical read method exactly.
		req.Method = method
		return s.dispatchV2ToOwner(response, req)
	}
	if operation == "" && operations != nil {
		if _, ok := operations.Lookup(req.Method); ok {
			operation = req.Method
		}
	}

	switch method {
	case "capabilities":
		if operation != "" {
			response.Error = invalidV2Request()
			return response
		}
		return s.v2Capabilities(response)
	case "job.get":
		return s.v2GetJob(response, req.JobID)
	case "job.cancel":
		return s.v2CancelJob(response, req.JobID)
	case "state.get", "spectrum.get":
		// A read method with an operation. The branch above serves the rest.
		response.Error = invalidV2Request()
		return response
	}
	if operation == "capabilities" {
		return s.v2Capabilities(response)
	}
	if operation == "runtime.snapshot" || operation == "runtime.status" {
		req.Method = "state.get"
		req.Operation = ""
		return s.dispatchV2ToOwner(response, req)
	}
	if operation == "" {
		response.Error = invalidV2Request()
		return response
	}
	if operations == nil {
		response.Error = v2Error(V2ErrorCodeUnavailable, V2MessageUnavailable)
		return response
	}
	if err := operations.Validate(operation, req.Params); err != nil {
		response.Error = err
		return response
	}
	// A method alias is normalized at the server boundary so runtime owners only
	// need to dispatch the canonical operation name.
	req.Operation = operation

	return s.dispatchV2ToOwner(response, req)
}

func (s *Server) dispatchV2ToOwner(response V2Response, req V2Request) V2Response {
	s.v2Mu.RLock()
	dispatcher := s.v2
	s.v2Mu.RUnlock()
	if dispatcher == nil {
		response.Error = v2Error(V2ErrorCodeUnavailable, V2MessageUnavailable)
		return response
	}
	ctx := s.context
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := dispatcher.DispatchV2(ctx, req)
	if err != nil {
		if err.Code == "" || err.Message == "" {
			response.Error = v2Error(V2ErrorCodeInternal, V2MessageInternal)
		} else {
			response.Error = err
		}
		return response
	}
	response.OK = true
	response.Result = result.Result
	response.Snapshot = result.Snapshot
	response.Job = result.Job
	return response
}

func (s *Server) v2Capabilities(response V2Response) V2Response {
	s.v2Mu.RLock()
	operations := s.operations
	s.v2Mu.RUnlock()
	if operations == nil {
		response.Error = v2Error(V2ErrorCodeUnavailable, V2MessageUnavailable)
		return response
	}
	result, err := json.Marshal(operations.Operations())
	if err != nil {
		response.Error = v2Error(V2ErrorCodeInternal, V2MessageInternal)
		return response
	}
	response.OK = true
	response.Result = result
	return response
}

func (s *Server) v2GetJob(response V2Response, jobID string) V2Response {
	if jobID == "" {
		response.Error = invalidV2Params()
		return response
	}
	if s.jobs == nil {
		response.Error = v2Error(V2ErrorCodeUnavailable, V2MessageUnavailable)
		return response
	}
	job, ok := s.jobs.Get(jobID)
	if !ok {
		response.Error = v2Error(V2ErrorCodeNotFound, V2MessageNotFound)
		return response
	}
	response.OK = true
	response.Job = &job
	return response
}

func (s *Server) v2CancelJob(response V2Response, jobID string) V2Response {
	if jobID == "" {
		response.Error = invalidV2Params()
		return response
	}
	if s.jobs == nil {
		response.Error = v2Error(V2ErrorCodeUnavailable, V2MessageUnavailable)
		return response
	}
	if err := s.jobs.Cancel(jobID); err != nil {
		switch {
		case errors.Is(err, ErrJobNotFound):
			response.Error = v2Error(V2ErrorCodeNotFound, V2MessageNotFound)
		case errors.Is(err, ErrInvalidJobState):
			response.Error = v2Error(V2ErrorCodeConflict, V2MessageConflict)
		default:
			response.Error = v2Error(V2ErrorCodeInternal, V2MessageInternal)
		}
		return response
	}
	job, _ := s.jobs.Get(jobID)
	response.OK = true
	response.Job = &job
	return response
}

func writeV2Response(conn net.Conn, response V2Response) bool {
	data, err := json.Marshal(response)
	if err != nil {
		fallback := V2Response{
			ID:    response.ID,
			OK:    false,
			Error: v2Error(V2ErrorCodeInternal, V2MessageInternal),
		}
		return writeJSONLine(conn, fallback)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		applog.Warn("ipc: write response: %v", err)
		return false
	}
	return true
}

func writeEventLine(conn net.Conn, event Event) bool {
	if len(event.encoded) > 0 {
		if _, err := conn.Write(event.encoded); err != nil {
			applog.Warn("ipc: write response: %v", err)
			return false
		}
		return true
	}
	return writeJSONLine(conn, event)
}

func writeJSONLine(conn net.Conn, value any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		applog.Warn("ipc: write response: %v", err)
		return false
	}
	return true
}

// secureSocketDir creates dir when missing, rejects symlinks and foreign owners,
// and enforces 0700 permissions.
func secureSocketDir(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("ipc: mkdir: %w", err)
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return fmt.Errorf("ipc: stat socket directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("ipc: socket directory %s is a symlink", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("ipc: socket directory %s is not a directory", dir)
	}
	if err := checkDirOwner(info); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("ipc: chmod socket directory: %w", err)
	}
	return nil
}

func peerUIDAllowed(uid uint32, euid int) error {
	if euid < 0 || uint64(uid) != uint64(euid) {
		return fmt.Errorf("ipc: peer uid %d does not match effective uid %d", uid, euid)
	}
	return nil
}

// removeStaleSocket removes sockPath only when os.Lstat reports a socket.
func removeStaleSocket(sockPath string) error {
	info, err := os.Lstat(sockPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ipc: stat socket %s: %w", sockPath, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("ipc: refusing to remove non-socket path %s", sockPath)
	}
	if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("ipc: remove stale socket %s: %w", sockPath, err)
	}
	return nil
}

// Listening reports whether a server accepts connections on sockPath. A
// missing socket, or one that refuses the connection, is not an error.
func Listening(sockPath string) (bool, error) {
	conn, err := dialSocket(sockPath, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}
	if isSocketUnavailable(err) {
		return false, nil
	}
	return false, fmt.Errorf("ipc: probe socket %s: %w", sockPath, err)
}

// listenExclusive binds sockPath, clearing an inode left by a dead process.
// The caller holds cliamp.sock.lock from before the first bind through
// socket permission changes and PID publication. A successful bind wins;
// an occupied path is probed, removed only if it is a stale socket with no
// listening server, and bound once more. A PID is never used to decide whether
// the server is live.
func listenExclusive(sockPath string) (net.Listener, error) {
	ln, err := listenSocket(sockPath)
	if err == nil || !isAddrInUse(err) {
		return ln, err
	}

	listening, err := Listening(sockPath)
	if err != nil {
		return nil, err
	}
	if listening {
		return nil, fmt.Errorf("ipc: cliamp is already running")
	}

	if err := removeStaleSocket(sockPath); err != nil {
		return nil, err
	}
	return listenSocket(sockPath)
}

// lockSocketStale serializes socket startup and shutdown with the same
// file lock used by the config writers.
func lockSocketStale(sockPath string) (func() error, error) {
	return fileutil.LockFile(sockPath + ".lock")
}
