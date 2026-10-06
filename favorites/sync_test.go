package favorites

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

func TestSyncQueue(t *testing.T) {
	tests := []struct {
		name string
		// changes are enqueued in order, then run in the order of runOrder.
		changes  []string
		runOrder []int
		wantRan  []bool
		wantLog  []string
	}{
		{
			name:     "one change",
			changes:  []string{"a=true"},
			runOrder: []int{0},
			wantRan:  []bool{true},
			wantLog:  []string{"a=true"},
		},
		{
			name:     "newer change for the same path wins",
			changes:  []string{"a=true", "a=false"},
			runOrder: []int{1, 0},
			wantRan:  []bool{false, true},
			wantLog:  []string{"a=false"},
		},
		{
			name:     "older change is skipped when it runs first",
			changes:  []string{"a=true", "a=false"},
			runOrder: []int{0, 1},
			wantRan:  []bool{false, true},
			wantLog:  []string{"a=false"},
		},
		{
			name:     "different paths all run",
			changes:  []string{"a=true", "b=true"},
			runOrder: []int{1, 0},
			wantRan:  []bool{true, true},
			wantLog:  []string{"b=true", "a=true"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var q SyncQueue
			var log []string
			runs := make([]func() (bool, error), len(tt.changes))
			for i, change := range tt.changes {
				path := change[:1]
				runs[i] = q.Enqueue(path, func() error {
					log = append(log, change)
					return nil
				})
			}
			ran := make([]bool, len(runs))
			for _, i := range tt.runOrder {
				ok, err := runs[i]()
				if err != nil {
					t.Fatal(err)
				}
				ran[i] = ok
			}
			if !slices.Equal(ran, tt.wantRan) {
				t.Fatalf("ran = %v, want %v", ran, tt.wantRan)
			}
			if !slices.Equal(log, tt.wantLog) {
				t.Fatalf("calls = %v, want %v", log, tt.wantLog)
			}
		})
	}
}

func TestSyncQueueReturnsError(t *testing.T) {
	var q SyncQueue
	want := errors.New("offline")
	ran, err := q.Enqueue("a", func() error { return want })()
	if !ran || !errors.Is(err, want) {
		t.Fatalf("run = %v, %v; want true, %v", ran, err, want)
	}
	// The failed change does not block a later change for the same path.
	if ran, err := q.Enqueue("a", func() error { return nil })(); !ran || err != nil {
		t.Fatalf("later run = %v, %v", ran, err)
	}
}

func TestSyncQueueRunsOneCallAtATime(t *testing.T) {
	var q SyncQueue
	var mu sync.Mutex
	active, maxActive := 0, 0
	var wg sync.WaitGroup
	for i := range 20 {
		run := q.Enqueue(string(rune('a'+i)), func() error {
			mu.Lock()
			active++
			maxActive = max(maxActive, active)
			mu.Unlock()
			mu.Lock()
			active--
			mu.Unlock()
			return nil
		})
		wg.Go(func() { _, _ = run() })
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("max concurrent calls = %d, want 1", maxActive)
	}
}
