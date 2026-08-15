package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBrokerRetainsAndReplaysLatestEvent(t *testing.T) {
	broker := NewBroker()
	if err := broker.Publish("plugin.discord-rpc.playback", json.RawMessage(`{"status":"playing"}`), true); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish("plugin.discord-rpc.playback", json.RawMessage(`{"status":"paused"}`), true); err != nil {
		t.Fatal(err)
	}

	sub, err := broker.Subscribe([]string{"plugin.discord-rpc.playback"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	select {
	case event := <-sub.Events():
		if !event.Retained || string(event.Data) != `{"status":"paused"}` || event.Sequence != 2 {
			t.Fatalf("retained event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for retained event")
	}
}

func TestBrokerFiltersTopics(t *testing.T) {
	broker := NewBroker()
	sub, err := broker.Subscribe([]string{"plugin.one.playback"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	if err := broker.Publish("plugin.two.playback", json.RawMessage(`{}`), false); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sub.Events():
		t.Fatalf("received unrelated event: %#v", event)
	case <-time.After(25 * time.Millisecond):
	}

	if err := broker.Publish("plugin.one.playback", json.RawMessage(`{"ok":true}`), false); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sub.Events():
		if event.Event != "plugin.one.playback" || event.Retained {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBrokerRejectsInvalidInput(t *testing.T) {
	broker := NewBroker()
	for _, topic := range []string{"", ".bad", "bad.", "bad..topic", "bad/topic", "bad topic"} {
		if err := broker.Publish(topic, json.RawMessage(`{}`), false); !errors.Is(err, ErrInvalidTopic) {
			t.Errorf("Publish(%q) error = %v, want ErrInvalidTopic", topic, err)
		}
	}
	if err := broker.Publish("valid.topic", json.RawMessage(`not-json`), false); err == nil {
		t.Fatal("expected invalid JSON error")
	}
	if err := broker.Publish("valid.topic", make(json.RawMessage, maxEventPayloadSize+1), false); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized payload error = %v", err)
	}
}

func TestBrokerClearsRetainedNamespace(t *testing.T) {
	broker := NewBroker()
	_ = broker.Publish("plugin.one.playback", json.RawMessage(`{}`), true)
	_ = broker.Publish("plugin.two.playback", json.RawMessage(`{}`), true)
	broker.ClearPrefix("plugin.one.")

	one, err := broker.Subscribe([]string{"plugin.one.playback"})
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	select {
	case event := <-one.Events():
		t.Fatalf("cleared event replayed: %#v", event)
	case <-time.After(25 * time.Millisecond):
	}

	two, err := broker.Subscribe([]string{"plugin.two.playback"})
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	select {
	case <-two.Events():
	case <-time.After(time.Second):
		t.Fatal("unrelated retained event was removed")
	}
}

func TestBrokerCloseDisconnectsAndRejectsWork(t *testing.T) {
	broker := NewBroker()
	sub, err := broker.Subscribe([]string{"plugin.test.events"})
	if err != nil {
		t.Fatal(err)
	}
	broker.Close()
	if _, ok := <-sub.Events(); ok {
		t.Fatal("subscription remained open")
	}
	if err := broker.Publish("plugin.test.events", json.RawMessage(`{}`), false); err == nil {
		t.Fatal("publish succeeded after close")
	}
	if _, err := broker.Subscribe([]string{"plugin.test.events"}); err == nil {
		t.Fatal("subscribe succeeded after close")
	}
	broker.Close()
	sub.Close()
}

func TestBrokerDisconnectsSlowSubscriber(t *testing.T) {
	broker := NewBroker()
	sub, err := broker.Subscribe([]string{"plugin.test.events"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	for i := 0; i < subscriberBufferSize+1; i++ {
		if err := broker.Publish("plugin.test.events", json.RawMessage(`{}`), false); err != nil {
			t.Fatal(err)
		}
	}
	for range sub.Events() {
	}
}

func TestBrokerRejectedRetainDoesNotConsumeSequence(t *testing.T) {
	broker := NewBroker()
	for i := range maxRetainedTopics {
		topic := fmt.Sprintf("plugin.filler.topic%d", i)
		if err := broker.Publish(topic, json.RawMessage(`{}`), true); err != nil {
			t.Fatal(err)
		}
	}
	if err := broker.Publish("plugin.filler.overflow", json.RawMessage(`{}`), true); !errors.Is(err, ErrTooManyRetained) {
		t.Fatalf("retained overflow error = %v, want ErrTooManyRetained", err)
	}

	sub, err := broker.Subscribe([]string{"plugin.seq.check"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if err := broker.Publish("plugin.seq.check", json.RawMessage(`{}`), false); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sub.Events():
		if event.Sequence != uint64(maxRetainedTopics)+1 {
			t.Fatalf("sequence = %d, want %d", event.Sequence, maxRetainedTopics+1)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}
