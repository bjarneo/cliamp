package ipc

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxTopicsPerSubscription = 32
	maxRetainedTopics        = 256
	maxEventPayloadSize      = 64 << 10
	subscriberBufferSize     = 32
	overflowEventTopic       = "system.overflow"
)

var (
	ErrInvalidTopic    = errors.New("invalid event topic")
	ErrTooManyTopics   = errors.New("too many subscription topics")
	ErrPayloadTooLarge = errors.New("event payload exceeds 64 KiB")
	ErrTooManyRetained = errors.New("too many retained event topics")
)

// Event is one message delivered over an IPC subscription. Sequence numbers
// are process-local and monotonically increasing. Retained marks events replayed
// to a newly connected subscriber.
type Event struct {
	Event    string          `json:"event"`
	Sequence uint64          `json:"seq"`
	Time     int64           `json:"time"`
	Retained bool            `json:"retained,omitempty"`
	Data     json.RawMessage `json:"data"`
}

// Subscription is an in-memory event stream owned by a Broker.
type Subscription struct {
	broker *Broker
	id     uint64
	events <-chan Event
	once   sync.Once
}

func (s *Subscription) Events() <-chan Event { return s.events }

// Close unregisters the subscription. It is safe to call more than once.
func (s *Subscription) Close() {
	if s == nil || s.broker == nil {
		return
	}
	s.once.Do(func() { s.broker.unsubscribe(s.id) })
}

type subscriber struct {
	topics map[string]struct{}
	events chan Event
}

// Broker distributes process-local events and optionally retains the latest
// event per topic. Publish never waits for a subscriber: a slow subscriber is
// disconnected rather than being allowed to block Cliamp or a Lua callback.
type Broker struct {
	mu          sync.Mutex
	nextEvent   uint64
	nextSub     uint64
	retained    map[string]Event
	subscribers map[uint64]*subscriber
	closed      bool
}

func NewBroker() *Broker {
	return &Broker{
		retained:    make(map[string]Event),
		subscribers: make(map[uint64]*subscriber),
	}
}

// Publish sends data to current subscribers and, when retain is true, stores
// the latest value in memory for replay to future subscribers.
func (b *Broker) Publish(topic string, data json.RawMessage, retain bool) error {
	if !validTopic(topic) {
		return ErrInvalidTopic
	}
	if len(data) > maxEventPayloadSize {
		return ErrPayloadTooLarge
	}
	if !json.Valid(data) {
		return errors.New("event payload is not valid JSON")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("event broker is closed")
	}
	// Reject before consuming a sequence number so numbering stays gap-free.
	if retain {
		if _, exists := b.retained[topic]; !exists && len(b.retained) >= maxRetainedTopics {
			return ErrTooManyRetained
		}
	}
	b.nextEvent++
	event := Event{
		Event:    topic,
		Sequence: b.nextEvent,
		Time:     time.Now().Unix(),
		Data:     append(json.RawMessage(nil), data...),
	}
	if retain {
		b.retained[topic] = event
	}
	for id, sub := range b.subscribers {
		if _, ok := sub.topics[topic]; !ok {
			continue
		}
		// Preserve one slot for an explicit resync signal. A consumer can then
		// refresh runtime.state before reconnecting instead of mistaking a clean
		// close for an idle server.
		if len(sub.events) >= cap(sub.events)-1 {
			b.nextEvent++
			overflow := Event{
				Event:    overflowEventTopic,
				Sequence: b.nextEvent,
				Time:     time.Now().Unix(),
				Data:     json.RawMessage(`{"resync_required":true}`),
			}
			sub.events <- overflow
			delete(b.subscribers, id)
			close(sub.events)
			continue
		}
		sub.events <- event
	}
	return nil
}

// Subscribe registers an exact-topic subscription and queues retained values
// before any subsequently published events. Topic ordering makes retained
// replay deterministic.
func (b *Broker) Subscribe(topics []string) (*Subscription, error) {
	if len(topics) == 0 {
		return nil, errors.New("at least one topic is required")
	}
	if len(topics) > maxTopicsPerSubscription {
		return nil, ErrTooManyTopics
	}
	set := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		if !validTopic(topic) {
			return nil, ErrInvalidTopic
		}
		set[topic] = struct{}{}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errors.New("event broker is closed")
	}
	b.nextSub++
	id := b.nextSub
	capacity := subscriberBufferSize
	if len(set) > capacity {
		capacity = len(set)
	}
	ch := make(chan Event, capacity)
	sub := &subscriber{topics: set, events: ch}
	b.subscribers[id] = sub

	sorted := make([]string, 0, len(set))
	for topic := range set {
		sorted = append(sorted, topic)
	}
	sort.Strings(sorted)
	for _, topic := range sorted {
		if event, ok := b.retained[topic]; ok {
			event.Retained = true
			ch <- event
		}
	}
	return &Subscription{broker: b, id: id, events: ch}, nil
}

// ClearPrefix removes retained values under a plugin namespace. Live
// subscribers remain connected and will receive future publications.
func (b *Broker) ClearPrefix(prefix string) {
	b.mu.Lock()
	for topic := range b.retained {
		if strings.HasPrefix(topic, prefix) {
			delete(b.retained, topic)
		}
	}
	b.mu.Unlock()
}

// Close disconnects all subscribers and rejects future publications and
// subscriptions. It is safe to call more than once.
func (b *Broker) Close() {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		for id, sub := range b.subscribers {
			delete(b.subscribers, id)
			close(sub.events)
		}
		clear(b.retained)
	}
	b.mu.Unlock()
}

func (b *Broker) unsubscribe(id uint64) {
	b.mu.Lock()
	if sub, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(sub.events)
	}
	b.mu.Unlock()
}

func validTopic(topic string) bool {
	if topic == overflowEventTopic {
		return false
	}
	if topic == "" || len(topic) > 256 || strings.HasPrefix(topic, ".") || strings.HasSuffix(topic, ".") || strings.Contains(topic, "..") {
		return false
	}
	for _, r := range topic {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
