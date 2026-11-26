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

func TestBrokerSignalsOverflowBeforeClosingSlowSubscriber(t *testing.T) {
	broker := NewBroker()
	sub, err := broker.Subscribe([]string{"runtime.state"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	for i := 0; i < subscriberBufferSize; i++ {
		if err := broker.Publish("runtime.state", json.RawMessage(`{}`), false); err != nil {
			t.Fatal(err)
		}
	}

	var overflow Event
	for event := range sub.Events() {
		if event.Event == overflowEventTopic {
			overflow = event
		}
	}
	if overflow.Event != overflowEventTopic || string(overflow.Data) != `{"resync_required":true}` {
		t.Fatalf("overflow = %#v", overflow)
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

func TestBrokerRetainedReplayDoesNotDeadlockOnOverflow(t *testing.T) {
	tests := []struct {
		name      string
		numTopics int
	}{
		{name: "full max topics", numTopics: maxTopicsPerSubscription},
		{name: "one below buffer size", numTopics: subscriberBufferSize - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			broker := NewBroker()
			topics := make([]string, tt.numTopics)
			for i := range tt.numTopics {
				topics[i] = fmt.Sprintf("plugin.replay.t%d", i)
				if err := broker.Publish(topics[i], json.RawMessage(`{"n":1}`), true); err != nil {
					t.Fatal(err)
				}
			}

			sub, err := broker.Subscribe(topics)
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()

			// Publish without draining sub.Events. Must overflow and disconnect instead of deadlocking.
			done := make(chan error, 1)
			go func() {
				done <- broker.Publish(topics[0], json.RawMessage(`{"n":2}`), false)
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Publish() error = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Publish() deadlocked on retained-full subscriber")
			}

			var events []Event
			for ev := range sub.Events() {
				events = append(events, ev)
			}
			if len(events) == 0 || events[len(events)-1].Event != overflowEventTopic {
				t.Fatalf("final event = %#v, want %s", events, overflowEventTopic)
			}
		})
	}
}

func TestBrokerCapsSubscribers(t *testing.T) {
	broker := NewBroker()
	subs := make([]*Subscription, 0, maxSubscribers)
	for range maxSubscribers {
		sub, err := broker.Subscribe([]string{"runtime.state"})
		if err != nil {
			t.Fatalf("Subscribe() failed before cap: %v", err)
		}
		subs = append(subs, sub)
	}
	defer func() {
		for _, sub := range subs {
			sub.Close()
		}
	}()

	if _, err := broker.Subscribe([]string{"runtime.state"}); !errors.Is(err, ErrTooManySubscribers) {
		t.Fatalf("Subscribe() past cap error = %v, want ErrTooManySubscribers", err)
	}

	subs[0].Close()
	replacement, err := broker.Subscribe([]string{"runtime.state"})
	if err != nil {
		t.Fatalf("Subscribe() after unsubscribe error = %v", err)
	}
	replacement.Close()
}

func TestBrokerReservesRuntimeRetainedTopics(t *testing.T) {
	broker := NewBroker()
	for i := range maxRetainedTopics {
		topic := fmt.Sprintf("plugin.flood.topic%d", i)
		if err := broker.Publish(topic, json.RawMessage(`{}`), true); err != nil {
			t.Fatal(err)
		}
	}
	if err := broker.Publish("plugin.flood.extra", json.RawMessage(`{}`), true); !errors.Is(err, ErrTooManyRetained) {
		t.Fatalf("plugin retain past cap = %v, want ErrTooManyRetained", err)
	}

	for _, topic := range []string{"runtime.state", "runtime.playback", "runtime.playlist", "runtime.settings"} {
		if err := broker.Publish(topic, json.RawMessage(`{"ok":true}`), true); err != nil {
			t.Fatalf("Publish(%q) failed after plugin cap reached: %v", topic, err)
		}
	}

	broker.ClearPrefix("runtime.")
	for i := range maxRetainedRuntimeTopics {
		topic := fmt.Sprintf("runtime.t%d", i)
		if err := broker.Publish(topic, json.RawMessage(`{}`), true); err != nil {
			t.Fatalf("Publish(%q) error = %v", topic, err)
		}
	}
	if err := broker.Publish("runtime.overflow", json.RawMessage(`{}`), true); !errors.Is(err, ErrTooManyRetained) {
		t.Fatalf("runtime retain past cap = %v, want ErrTooManyRetained", err)
	}
}

func TestBrokerPreMarshalsBroadcastEventOnce(t *testing.T) {
	broker := NewBroker()
	sub1, err := broker.Subscribe([]string{"runtime.state"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub1.Close()
	sub2, err := broker.Subscribe([]string{"runtime.state"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub2.Close()

	if err := broker.Publish("runtime.state", json.RawMessage(`{"state":"playing"}`), false); err != nil {
		t.Fatal(err)
	}
	ev1 := <-sub1.Events()
	ev2 := <-sub2.Events()
	if len(ev1.encoded) == 0 || string(ev1.encoded) != string(ev2.encoded) {
		t.Fatalf("encoded lines differ or empty: %q vs %q", ev1.encoded, ev2.encoded)
	}
	var decoded Event
	if err := json.Unmarshal(ev1.encoded, &decoded); err != nil {
		t.Fatalf("unmarshal pre-encoded line: %v", err)
	}
	if decoded.Event != "runtime.state" || string(decoded.Data) != `{"state":"playing"}` {
		t.Fatalf("decoded = %#v", decoded)
	}
}
