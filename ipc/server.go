package ipc

import (
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
)

const ipcRequestReadTimeout = 60 * time.Second

// Server listens on a Unix socket and dispatches IPC commands.
type Server struct {
	listener    net.Listener
	sockPath    string
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
	return newServer(sockPath, NewBroker(), true)
}

// NewServerWithBroker creates and starts the IPC server using broker. The
// caller retains ownership of a supplied broker and it is not closed by Server.
func NewServerWithBroker(sockPath string, broker *Broker) (*Server, error) {
	owned := false
	if broker == nil {
		broker = NewBroker()
		owned = true
	}
	return newServer(sockPath, broker, owned)
}

func newServer(sockPath string, broker *Broker, brokerOwned bool) (*Server, error) {
	// Ensure the parent directory exists.
	if err := os.MkdirAll(filepath.Dir(sockPath), 0700); err != nil {
		return nil, fmt.Errorf("ipc: mkdir: %w", err)
	}

	ln, err := listenExclusive(sockPath)
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

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		listener:    ln,
		sockPath:    sockPath,
		broker:      broker,
		brokerOwned: brokerOwned,
		operations:  DefaultOperationRegistry(),
		jobs:        NewJobStore(),
		context:     ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		conns:       make(map[net.Conn]struct{}),
	}

	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// Close shuts down the server, removes socket and PID file.
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
		// Close in-flight connections so their handleConn read loops unblock
		// immediately rather than waiting out the per-request read deadline.
		s.closeConns()
		s.wg.Wait()
		if s.brokerOwned && s.broker != nil {
			s.broker.Close()
		}
		if s.sockPath != "" {
			_ = os.Remove(s.sockPath)
			_ = os.Remove(s.sockPath + ".pid")
		}
	})
	return s.closeErr
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

	scanner := newFrameScanner(conn)

	for {
		// Per-request deadline so long-lived streaming clients (e.g. vis bands
		// polling) aren't killed at a fixed wall clock, but idle clients still
		// time out.
		conn.SetReadDeadline(time.Now().Add(ipcRequestReadTimeout))
		if !scanner.Scan() {
			return
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		version, id, versioned, err := parseProtocolVersion(line)
		if err != nil {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = writeJSONLine(conn, V2Response{ID: id, OK: false, Error: invalidV2Request()})
			continue
		}
		if !versioned {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = writeJSONLine(conn, V2Response{ID: id, OK: false, Error: v2Error(V2ErrorCodeInvalidVersion, V2MessageInvalidVersion)})
			continue
		}

		if version != protocolVersion2 {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = writeJSONLine(conn, V2Response{
				ID:    id,
				OK:    false,
				Error: v2Error(V2ErrorCodeInvalidVersion, V2MessageInvalidVersion),
			})
			continue
		}

		var req V2Request
		if err := json.Unmarshal(line, &req); err != nil {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = writeJSONLine(conn, V2Response{ID: id, OK: false, Error: invalidV2Request()})
			continue
		}
		if isV2Subscribe(req) {
			s.streamV2Subscription(conn, req)
			return
		}

		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = writeJSONLine(conn, s.dispatchV2(req))
	}
}

func (s *Server) streamV2Subscription(conn net.Conn, req V2Request) {
	s.streamSubscription(conn, req.Topics, V2Response{ID: req.ID, OK: true})
}

func (s *Server) streamSubscription(conn net.Conn, topics []string, acknowledgement V2Response) {
	// handleConn sets a per-request read deadline. Subscriptions are idle,
	// server-to-client streams after the initial request, so they must not
	// inherit that deadline or they will be closed every request timeout.
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
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if !writeJSONLine(conn, acknowledgement) {
		return
	}

	// A subscription is server-to-client after its acknowledgment. Keep a read
	// pending solely to detect client disconnects even when no events publish.
	peerClosed := make(chan struct{})
	go func() {
		var one [1]byte
		_, _ = conn.Read(one[:])
		close(peerClosed)
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
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if !writeJSONLine(conn, event) {
				return
			}
		}
	}
}

func (s *Server) writeSubscriptionError(conn net.Conn, response V2Response) {
	response.OK = false
	response.Error = invalidV2Params()
	_ = writeJSONLine(conn, response)
}

// parseProtocolVersion reads the mandatory version field from a V2 envelope.
// Unversioned and malformed versions receive a structured invalid_version
// response rather than being interpreted as a different protocol.
func parseProtocolVersion(line []byte) (version int, id json.RawMessage, versioned bool, err error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(line, &envelope); err != nil {
		return 0, nil, false, err
	}
	versionRaw, ok := envelope["version"]
	if !ok {
		return 0, nil, false, nil
	}
	returnVersioned := true
	id = cloneRawMessage(envelope["id"])
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return 0, id, returnVersioned, nil
	}
	return version, id, returnVersioned, nil
}

func isV2Subscribe(req V2Request) bool {
	return strings.EqualFold(req.Method, "subscribe") || strings.EqualFold(req.Operation, "subscribe")
}

func (s *Server) dispatchV2(req V2Request) V2Response {
	response := V2Response{ID: cloneRawMessage(req.ID)}
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
		response.Error = cloneV2Error(err)
		if response.Error.Code == "" || response.Error.Message == "" {
			response.Error = v2Error(V2ErrorCodeInternal, V2MessageInternal)
		}
		return response
	}
	if err := validV2Result(result.Result); err != nil {
		response.Error = v2ErrorFromError(err)
		return response
	}
	response.OK = true
	response.Result = cloneRawMessage(result.Result)
	response.Snapshot = cloneSnapshot(result.Snapshot)
	if result.Job != nil {
		job := cloneJob(*result.Job)
		response.Job = &job
	}
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

// listenStaleSocketAttempts bounds the stale-socket retries in listenExclusive.
// One retry clears a socket inode left by a dead process; the extra attempt
// covers a competing starter that bound between our probe and our unlink.
const listenStaleSocketAttempts = 3

// listenExclusive binds sockPath, clearing a socket inode left behind by a
// process that died without cleaning up.
//
// The bind is the arbiter, not a probe performed beforehand. net.Listen fails
// with EADDRINUSE while any inode occupies the path, so the probe runs only to
// explain a bind that already failed: a path is removed only once the bind has
// failed *and* the probe confirms no server accepts connections on it.
//
// Deciding from a probe taken before the bind is what let two starting daemons
// delete each other's socket. Process A could probe "nothing listening" while
// process B bound and published its PID file, after which A removed B's live
// socket, leaving B running on an unlinked inode and unreachable.
//
// The PID file is not consulted at all. It records only a PID, and PIDs are
// reused, so "is this PID alive" does not imply "is this cliamp alive" — it only
// implies that some process holds the number. Gating on it stranded the daemon
// after a reboot, when a stale PID file kept matching whichever unrelated
// process inherited that PID. See #591.
func listenExclusive(sockPath string) (net.Listener, error) {
	for attempt := 0; attempt < listenStaleSocketAttempts; attempt++ {
		ln, err := listenSocket(sockPath)
		if err == nil {
			return ln, nil
		}
		if !isAddrInUse(err) {
			return nil, err
		}
		listening, probeErr := Listening(sockPath)
		if probeErr != nil {
			return nil, probeErr
		}
		if listening {
			return nil, fmt.Errorf("ipc: cliamp is already running")
		}
		// Nothing is serving this path, so the inode is stale.
		if rmErr := os.Remove(sockPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return nil, rmErr
		}
	}
	return nil, fmt.Errorf("ipc: could not acquire socket %s", sockPath)
}
