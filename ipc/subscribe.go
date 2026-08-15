package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// EventStream is a client-side subscription to IPC events.
type EventStream struct {
	conn    net.Conn
	scanner *bufio.Scanner
}

// Subscribe opens a streaming-only IPC connection for exact topic names.
// The caller must close the stream when it is no longer needed.
func Subscribe(sockPath string, topics []string) (*EventStream, error) {
	conn, err := dialSocket(sockPath, 3*time.Second)
	if err != nil {
		if isSocketUnavailable(err) {
			return nil, fmt.Errorf("no socket at %s: %w", sockPath, ErrNotRunning)
		}
		return nil, fmt.Errorf("connect: %w", err)
	}
	fail := func(err error) (*EventStream, error) {
		_ = conn.Close()
		return nil, err
	}

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	request, err := json.Marshal(Request{Cmd: "subscribe", Topics: topics})
	if err != nil {
		return fail(fmt.Errorf("marshal subscribe request: %w", err))
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return fail(fmt.Errorf("write subscribe request: %w", err))
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fail(fmt.Errorf("read subscribe response: %w", err))
		}
		return fail(fmt.Errorf("no subscribe response from server"))
	}
	var response Response
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		return fail(fmt.Errorf("decode subscribe response: %w", err))
	}
	if !response.OK {
		return fail(fmt.Errorf("subscribe: %s", response.Error))
	}
	_ = conn.SetDeadline(time.Time{})
	return &EventStream{conn: conn, scanner: scanner}, nil
}

// Next blocks until the next event arrives. A returned error means the stream
// ended and should be closed and reconnected.
func (s *EventStream) Next() (Event, error) {
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

func (s *EventStream) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.Close()
}
