package favorites

import "sync"

// SyncQueue orders the calls that copy favorite changes to providers. The
// calls run one at a time. A call is skipped when a newer change for the same
// path exists, so the provider always ends with the last local state, even
// when the user toggles a track again before the first call completes.
//
// The zero value is ready to use. A SyncQueue must not be copied after first
// use.
type SyncQueue struct {
	callMu sync.Mutex // held while a provider call runs

	mu     sync.Mutex // guards next and latest; never held during a call
	next   uint64
	latest map[string]uint64
}

// Enqueue records a change for path and returns a function that applies it.
// Call Enqueue in the same order as the changes to the local store. The
// returned function can run on any goroutine. It waits while an earlier call
// runs, then calls apply unless a newer change for path exists. It reports
// whether apply ran.
func (q *SyncQueue) Enqueue(path string, apply func() error) func() (bool, error) {
	q.mu.Lock()
	q.next++
	seq := q.next
	if q.latest == nil {
		q.latest = make(map[string]uint64)
	}
	q.latest[path] = seq
	q.mu.Unlock()

	return func() (bool, error) {
		q.callMu.Lock()
		defer q.callMu.Unlock()

		q.mu.Lock()
		stale := q.latest[path] != seq
		q.mu.Unlock()
		if stale {
			return false, nil
		}

		err := apply()

		q.mu.Lock()
		if q.latest[path] == seq {
			delete(q.latest, path)
		}
		q.mu.Unlock()
		return true, err
	}
}
