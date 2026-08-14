package ipc

import (
	"encoding/json"
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
