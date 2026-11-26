package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSendV2AndSubscribeV2(t *testing.T) {
	broker := NewBroker()
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServerWithBroker(sock, broker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	server.SetV2Dispatcher(V2DispatcherFunc(func(context.Context, V2Request) (V2Result, *V2Error) {
		return V2Result{Snapshot: &RuntimeSnapshot{Revision: 2, State: "playing"}}, nil
	}))

	response, err := SendV2(sock, V2Request{ID: json.RawMessage(`"gui"`), Method: "state.get"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || string(response.ID) != `"gui"` || response.Snapshot == nil || response.Snapshot.Revision != 2 {
		t.Fatalf("response = %#v", response)
	}

	if err := broker.Publish("runtime.state", json.RawMessage(`{"revision":1}`), true); err != nil {
		t.Fatal(err)
	}
	stream, err := SubscribeV2(sock, json.RawMessage(`"events"`), []string{"runtime.state"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	retained, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !retained.Retained || string(retained.Data) != `{"revision":1}` {
		t.Fatalf("retained event = %#v", retained)
	}
	if err := broker.Publish("runtime.state", json.RawMessage(`{"revision":2}`), true); err != nil {
		t.Fatal(err)
	}
	eventCh := make(chan Event, 1)
	errCh := make(chan error, 1)
	go func() {
		event, err := stream.Next()
		if err != nil {
			errCh <- err
			return
		}
		eventCh <- event
	}()
	select {
	case event := <-eventCh:
		if event.Event != "runtime.state" || string(event.Data) != `{"revision":2}` {
			t.Fatalf("event = %#v", event)
		}
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for v2 event")
	}
}

func TestV2ClientRejectsMismatchedResponseID(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	listener, err := listenSocket(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := newFrameScanner(conn)
		if !scanner.Scan() {
			return
		}
		_ = writeJSONLine(conn, V2Response{ID: json.RawMessage(`"other"`), OK: true})
	}()

	_, err = SendV2(sock, V2Request{ID: json.RawMessage(`"expected"`), Method: "state.get"})
	if err == nil || err.Error() != "response ID does not match request" {
		t.Fatalf("SendV2() error = %v", err)
	}
}

func TestDialV2ReusesConnection(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	listener, err := listenSocket(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var accepts atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			go func(c net.Conn) {
				defer c.Close()
				scanner := newFrameScanner(c)
				for scanner.Scan() {
					req, protoErr := decodeV2Request(scanner.Bytes())
					if protoErr != nil {
						_ = writeJSONLine(c, V2Response{ID: req.ID, OK: false, Error: protoErr})
						continue
					}
					_ = writeJSONLine(c, V2Response{
						ID:       req.ID,
						OK:       true,
						Snapshot: &RuntimeSnapshot{State: req.Method},
					})
				}
			}(conn)
		}
	}()

	client, err := DialV2(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	requests := []V2Request{
		{ID: json.RawMessage(`"first"`), Method: "state.get"},
		{ID: json.RawMessage(`2`), Method: "capabilities"},
		{ID: json.RawMessage(`"third"`), Method: "job.get", JobID: "job-1"},
		{ID: json.RawMessage(`"fourth"`), Method: "state.get"},
	}
	for _, req := range requests {
		resp, err := client.Send(req)
		if err != nil {
			t.Fatalf("Send(%s) error = %v", req.Method, err)
		}
		if !resp.OK || string(resp.ID) != string(req.ID) || resp.Snapshot == nil || resp.Snapshot.State != req.Method {
			t.Fatalf("Send(%s) response = %#v", req.Method, resp)
		}
	}
	if got := accepts.Load(); got != 1 {
		t.Fatalf("accepted connections = %d, want 1", got)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := client.Send(V2Request{ID: json.RawMessage(`"after"`), Method: "state.get"}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Send() after Close error = %v, want net.ErrClosed", err)
	}
	_ = listener.Close()
	<-done
}

func TestDialV2Errors(t *testing.T) {
	tests := []struct {
		name      string
		serve     func(conn net.Conn)
		wantErrIs error
		wantSub   string
	}{
		{
			name:      "no listener",
			wantErrIs: ErrNotRunning,
		},
		{
			name: "server closes immediately",
			serve: func(conn net.Conn) {
				scanner := newFrameScanner(conn)
				_ = scanner.Scan()
			},
			wantErrIs: io.EOF,
		},
		{
			name: "mismatched response ID",
			serve: func(conn net.Conn) {
				scanner := newFrameScanner(conn)
				if scanner.Scan() {
					_ = writeJSONLine(conn, V2Response{ID: json.RawMessage(`"wrong"`), OK: true})
				}
			},
			wantSub: "response ID does not match request",
		},
		{
			name: "unexpected protocol version",
			serve: func(conn net.Conn) {
				scanner := newFrameScanner(conn)
				if scanner.Scan() {
					_, _ = conn.Write([]byte(`{"version":1,"id":"req","ok":true}` + "\n"))
				}
			},
			wantSub: "unexpected protocol version 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sock := filepath.Join(shortTempDir(t), "cliamp.sock")
			if tt.serve == nil {
				_, err := DialV2(sock)
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("DialV2() error = %v, want %v", err, tt.wantErrIs)
				}
				return
			}
			listener, err := listenSocket(sock)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				tt.serve(conn)
			}()

			client, err := DialV2(sock)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.Send(V2Request{ID: json.RawMessage(`"req"`), Method: "state.get"})
			if err == nil {
				t.Fatal("Send() succeeded, want error")
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("Send() error = %v, want %v", err, tt.wantErrIs)
			}
			if tt.wantSub != "" && !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("Send() error = %q, want substring %q", err.Error(), tt.wantSub)
			}
		})
	}
}
