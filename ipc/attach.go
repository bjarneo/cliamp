package ipc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// maxHandshakeLine bounds the acknowledgment line an attach client reads
// byte by byte.
const maxHandshakeLine = 64 * 1024

// AttachHandler takes over a client connection after an attach request. The
// handler owns the connection for the life of the session; the server closes
// it once the handler returns.
type AttachHandler interface {
	HandleAttach(conn net.Conn, request V2Request)
}

// SetAttachHandler wires the terminal session host. Attach requests are
// refused as unavailable while it is nil, which is what a cliamp that owns a
// real terminal reports.
func (s *Server) SetAttachHandler(handler AttachHandler) {
	s.v2Mu.Lock()
	s.attach = handler
	s.v2Mu.Unlock()
}

func isV2Attach(req V2Request) bool {
	return strings.EqualFold(req.Method, "attach") || strings.EqualFold(req.Operation, "attach")
}

// handleV2Attach hands conn to the attach handler. An attach session is a
// long-lived, bidirectional stream, so it must not inherit the per-request
// read deadline handleConn sets.
func (s *Server) handleV2Attach(conn net.Conn, req V2Request) {
	s.v2Mu.RLock()
	handler := s.attach
	s.v2Mu.RUnlock()
	if handler == nil {
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = writeJSONLine(conn, V2Response{
			ID:    cloneRawMessage(req.ID),
			OK:    false,
			Error: v2Error(V2ErrorCodeUnavailable, V2MessageUnavailable),
		})
		return
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = writeJSONLine(conn, V2Response{
			ID:    cloneRawMessage(req.ID),
			OK:    false,
			Error: v2Error(V2ErrorCodeInternal, V2MessageInternal),
		})
		return
	}
	handler.HandleAttach(conn, req)
}

// WriteV2Response writes one response envelope as an NDJSON line. An attach
// handler uses it to acknowledge the handshake before the connection switches
// to its own frame protocol.
func WriteV2Response(w io.Writer, response V2Response) error {
	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

// DialAttach opens an attach session and returns the connection together with
// the handshake acknowledgment. The caller owns the connection and speaks its
// own protocol on it from there.
//
// The acknowledgment is read one byte at a time on purpose: buffering it would
// swallow the frames the host writes immediately afterwards.
func DialAttach(sockPath string, request V2Request) (net.Conn, V2Response, error) {
	conn, err := dialSocket(sockPath, 3*time.Second)
	if err != nil {
		if isSocketUnavailable(err) {
			return nil, V2Response{}, fmt.Errorf("no socket at %s: %w", sockPath, ErrNotRunning)
		}
		return nil, V2Response{}, fmt.Errorf("connect: %w", err)
	}
	fail := func(err error) (net.Conn, V2Response, error) {
		_ = conn.Close()
		return nil, V2Response{}, err
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fail(fmt.Errorf("set deadline: %w", err))
	}
	request.Method = "attach"
	data, err := json.Marshal(request)
	if err != nil {
		return fail(fmt.Errorf("marshal attach request: %w", err))
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return fail(fmt.Errorf("write attach request: %w", err))
	}
	line, err := readLineUnbuffered(conn)
	if err != nil {
		return fail(fmt.Errorf("read attach response: %w", err))
	}
	var response V2Response
	if err := json.Unmarshal(line, &response); err != nil {
		return fail(fmt.Errorf("decode attach response: %w", err))
	}
	if response.Version != protocolVersion2 {
		return fail(fmt.Errorf("unexpected protocol version %d", response.Version))
	}
	if !bytes.Equal(bytes.TrimSpace(request.ID), bytes.TrimSpace(response.ID)) {
		return fail(fmt.Errorf("response ID does not match request"))
	}
	if !response.OK {
		if response.Error == nil {
			return fail(fmt.Errorf("attach failed"))
		}
		return fail(&V2Error{Code: response.Error.Code, Message: response.Error.Message, Detail: response.Error.Detail})
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fail(fmt.Errorf("clear deadline: %w", err))
	}
	return conn, response, nil
}

func readLineUnbuffered(conn net.Conn) ([]byte, error) {
	line := make([]byte, 0, 256)
	var one [1]byte
	for {
		n, err := conn.Read(one[:])
		if n == 1 {
			if one[0] == '\n' {
				return line, nil
			}
			line = append(line, one[0])
			if len(line) > maxHandshakeLine {
				return nil, fmt.Errorf("response line too long")
			}
			continue
		}
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("no response from server")
			}
			return nil, err
		}
	}
}
