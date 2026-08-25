package ipc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// SendV2 sends one version 2 request and returns its correlated response.
func SendV2(sockPath string, request V2Request) (V2Response, error) {
	return SendV2WithDeadline(sockPath, request, 5*time.Second)
}

// SendV2WithDeadline is SendV2 with a caller-selected exchange deadline.
func SendV2WithDeadline(sockPath string, request V2Request, deadline time.Duration) (V2Response, error) {
	conn, err := dialSocket(sockPath, 3*time.Second)
	if err != nil {
		if isSocketUnavailable(err) {
			return V2Response{}, fmt.Errorf("no socket at %s: %w", sockPath, ErrNotRunning)
		}
		return V2Response{}, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(deadline)); err != nil {
		return V2Response{}, fmt.Errorf("set deadline: %w", err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return V2Response{}, fmt.Errorf("marshal request: %w", err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return V2Response{}, fmt.Errorf("write: %w", err)
	}
	scanner := newFrameScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return V2Response{}, fmt.Errorf("read response: %w", err)
		}
		return V2Response{}, fmt.Errorf("no response from server")
	}
	var response V2Response
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		return V2Response{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if response.Version != protocolVersion2 {
		return V2Response{}, fmt.Errorf("unexpected protocol version %d", response.Version)
	}
	if !bytes.Equal(bytes.TrimSpace(request.ID), bytes.TrimSpace(response.ID)) {
		return V2Response{}, fmt.Errorf("response ID does not match request")
	}
	return response, nil
}

// V2EventStream is a version 2 subscription. Events retain the existing
// envelope so plugin and runtime topics can be consumed uniformly.
type V2EventStream struct {
	conn    net.Conn
	scanner *bufio.Scanner
}

// SubscribeV2 opens a streaming-only V2 subscription for exact topic names.
func SubscribeV2(sockPath string, id json.RawMessage, topics []string) (*V2EventStream, error) {
	conn, err := dialSocket(sockPath, 3*time.Second)
	if err != nil {
		if isSocketUnavailable(err) {
			return nil, fmt.Errorf("no socket at %s: %w", sockPath, ErrNotRunning)
		}
		return nil, fmt.Errorf("connect: %w", err)
	}
	fail := func(err error) (*V2EventStream, error) {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fail(fmt.Errorf("set deadline: %w", err))
	}
	data, err := json.Marshal(V2Request{ID: id, Method: "subscribe", Topics: topics})
	if err != nil {
		return fail(fmt.Errorf("marshal subscribe request: %w", err))
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return fail(fmt.Errorf("write subscribe request: %w", err))
	}
	scanner := newFrameScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fail(fmt.Errorf("read subscribe response: %w", err))
		}
		return fail(fmt.Errorf("no subscribe response from server"))
	}
	var response V2Response
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		return fail(fmt.Errorf("decode subscribe response: %w", err))
	}
	if !response.OK {
		if response.Error == nil {
			return fail(fmt.Errorf("subscribe failed"))
		}
		return fail(fmt.Errorf("subscribe: %s", response.Error.Message))
	}
	if response.Version != protocolVersion2 {
		return fail(fmt.Errorf("unexpected protocol version %d", response.Version))
	}
	if !bytes.Equal(bytes.TrimSpace(id), bytes.TrimSpace(response.ID)) {
		return fail(fmt.Errorf("response ID does not match request"))
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fail(fmt.Errorf("clear deadline: %w", err))
	}
	return &V2EventStream{conn: conn, scanner: scanner}, nil
}

// Next blocks until the next runtime or plugin event arrives.
func (s *V2EventStream) Next() (Event, error) {
	if s == nil || s.conn == nil {
		return Event{}, fmt.Errorf("subscription is closed")
	}
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return Event{}, fmt.Errorf("read event: %w", err)
		}
		return Event{}, fmt.Errorf("event stream closed")
	}
	var event Event
	if err := json.Unmarshal(s.scanner.Bytes(), &event); err != nil {
		return Event{}, fmt.Errorf("decode event: %w", err)
	}
	return event, nil
}

// Close ends the subscription.
func (s *V2EventStream) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.Close()
}
