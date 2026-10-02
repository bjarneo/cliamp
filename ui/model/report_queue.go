package model

import (
	"slices"
	"sync"
	"time"
)

// reportQueue runs the playback reports to providers one at a time, in the
// order that Update added them. So the now-playing, progress and scrobble
// reports of a track reach the provider in order, as the favorite calls do
// through favorites.SyncQueue. Unlike that queue, it skips no now-playing
// or scrobble report. It drops only a progress report that a newer one
// replaces, so a slow server does not make the queue grow. It runs the
// reports on one goroutine that it starts when a report waits and that ends
// when none waits, so add never blocks Update.
type reportQueue struct {
	mu      sync.Mutex
	pending []queuedReport
	running bool
	idle    chan struct{} // closed when the running drain ends
}

// queuedReport is a report that waits to run. progress is the track path
// of a progress report and "" for the other reports.
type queuedReport struct {
	progress string
	run      func()
}

// add queues report and returns at once. For a progress report, progress
// names the track, and add drops a progress report of that track that
// still waits, because the newer position replaces it. The other reports
// pass "" and are never dropped.
func (q *reportQueue) add(progress string, report func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if progress != "" {
		q.pending = slices.DeleteFunc(q.pending, func(r queuedReport) bool { return r.progress == progress })
	}
	q.pending = append(q.pending, queuedReport{progress: progress, run: report})
	if !q.running {
		q.running = true
		q.idle = make(chan struct{})
		go q.drain(q.idle)
	}
}

// drain runs the queued reports until none waits. It then closes idle.
func (q *reportQueue) drain(idle chan struct{}) {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.running = false
			q.mu.Unlock()
			close(idle)
			return
		}
		report := q.pending[0].run
		q.pending[0] = queuedReport{}
		q.pending = q.pending[1:]
		q.mu.Unlock()
		report()
	}
}

// wait waits until no report waits or runs, or until timeout passes. It
// reports whether the queue drained.
func (q *reportQueue) wait(timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		q.mu.Lock()
		running, idle := q.running, q.idle
		q.mu.Unlock()
		if !running {
			return true
		}
		select {
		case <-idle:
		case <-deadline.C:
			return false
		}
	}
}
