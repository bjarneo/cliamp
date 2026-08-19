package ipc

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestSubscribeStreamsRetainedAndLiveEvents(t *testing.T) {
	broker := NewBroker()
	if err := broker.Publish("plugin.test.playback", json.RawMessage(`{"state":"stopped"}`), true); err != nil {
		t.Fatal(err)
	}

	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServerWithBroker(sock, &captureDispatcher{}, broker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	stream, err := Subscribe(sock, []string{"plugin.test.playback"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	retained, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !retained.Retained || string(retained.Data) != `{"state":"stopped"}` {
		t.Fatalf("retained event = %#v", retained)
	}

	if err := broker.Publish("plugin.test.playback", json.RawMessage(`{"state":"playing"}`), true); err != nil {
		t.Fatal(err)
	}
	live, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if live.Retained || string(live.Data) != `{"state":"playing"}` || live.Sequence <= retained.Sequence {
		t.Fatalf("live event = %#v after %#v", live, retained)
	}
}

func TestSubscribeRejectsMissingTopics(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock, &captureDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	if _, err := Subscribe(sock, nil); err == nil {
		t.Fatal("expected subscription error")
	}
}

func TestSubscriptionClearsRequestReadDeadline(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	broker := NewBroker()
	server := &Server{broker: broker, done: make(chan struct{})}
	if err := serverConn.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		defer serverConn.Close()
		server.streamSubscription(serverConn, []string{"plugin.test.playback"})
	}()
	defer clientConn.Close()

	scanner := bufio.NewScanner(clientConn)
	if !scanner.Scan() {
		t.Fatalf("read acknowledgment: %v", scanner.Err())
	}
	var acknowledgment Response
	if err := json.Unmarshal(scanner.Bytes(), &acknowledgment); err != nil || !acknowledgment.OK {
		t.Fatalf("acknowledgment = %q, err=%v", scanner.Bytes(), err)
	}

	// Wait beyond the inherited request deadline, then prove the subscription
	// is still alive by delivering a newly published event.
	time.Sleep(75 * time.Millisecond)
	if err := broker.Publish("plugin.test.playback", json.RawMessage(`{"state":"playing"}`), false); err != nil {
		t.Fatal(err)
	}
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if !scanner.Scan() {
		t.Fatalf("read event after request deadline: %v", scanner.Err())
	}
	var event Event
	if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "plugin.test.playback" {
		t.Fatalf("event = %#v", event)
	}

	_ = clientConn.Close()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("subscription did not stop after client close")
	}
}

func TestSubscriptionEndsWhenServerCloses(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock, &captureDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := Subscribe(sock, []string{"plugin.test.playback"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected closed stream error")
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not close with server")
	}
}
