package luaplugin

import (
	"testing"
	"time"
)

func TestEnqueueCompletionWaitsForQueueSpace(t *testing.T) {
	m := newTestManager()
	p := &Plugin{installName: "completion", queue: make(chan func(), 1)}
	p.queue <- func() {}
	done := make(chan bool, 1)
	go func() { done <- m.enqueueCompletion(p, "completion", func() {}) }()

	select {
	case <-done:
		t.Fatal("enqueueCompletion delivered while the queue was full")
	case <-time.After(100 * time.Millisecond):
	}

	<-p.queue
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("enqueueCompletion did not deliver the completion")
		}
	case <-time.After(time.Second):
		t.Fatal("enqueueCompletion did not retry after the queue had space")
	}
}
