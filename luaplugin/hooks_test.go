package luaplugin

import "testing"

func TestEnqueueCompletionWaitsForQueueSpace(t *testing.T) {
	m := newTestManager()
	p := &Plugin{installName: "completion", queue: make(chan func(), 1)}
	p.queue <- func() {}
	done := make(chan bool, 1)
	go func() { done <- m.enqueueCompletion(p, "completion", func() {}) }()
	<-p.queue
	if !<-done {
		t.Fatal("enqueueCompletion did not deliver the completion")
	}
}
