package ipc

import (
	"context"
	"encoding/json"
	"path/filepath"
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
