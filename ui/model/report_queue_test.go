package model

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// waitIdle waits until q has run every report it got.
func (q *reportQueue) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		q.mu.Lock()
		idle := !q.running && len(q.pending) == 0
		q.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("report queue did not drain")
		}
		time.Sleep(time.Millisecond)
	}
}

// The reports run one at a time, in the order they were added, also when
// an early report is slow. A progress report that still waits is dropped
// when a newer progress report of the same track arrives.
func TestReportQueueKeepsOrder(t *testing.T) {
	for _, tt := range []struct {
		name  string
		slow  int // the report that sleeps
		count int
		// progress names the track of each progress report and is "" for
		// the other reports. With progress, the first report waits until
		// every report is added.
		progress []string
		// want is the order of the reports that run. nil means each report
		// in the add order.
		want []int
	}{
		{name: "one report", slow: -1, count: 1},
		{name: "fast reports", slow: -1, count: 50},
		{name: "a slow first report", slow: 0, count: 20},
		{name: "a slow middle report", slow: 10, count: 20},
		{name: "stale progress reports", slow: -1, count: 7,
			progress: []string{"", "a.mp3", "", "a.mp3", "b.mp3", "", "a.mp3"},
			want:     []int{0, 2, 4, 5, 6}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var q reportQueue
			var mu sync.Mutex
			var order []int
			var running, overlap atomic.Int32
			added := make(chan struct{})
			for i := range tt.count {
				progress := ""
				if tt.progress != nil {
					progress = tt.progress[i]
				}
				q.add(progress, func() {
					if running.Add(1) > 1 {
						overlap.Add(1)
					}
					defer running.Add(-1)
					if i == 0 && tt.progress != nil {
						<-added
					}
					if i == tt.slow {
						time.Sleep(20 * time.Millisecond)
					}
					mu.Lock()
					order = append(order, i)
					mu.Unlock()
				})
			}
			close(added)
			q.waitIdle(t)
			if overlap.Load() != 0 {
				t.Fatal("two reports ran at the same time")
			}
			want := tt.want
			if want == nil {
				for i := range tt.count {
					want = append(want, i)
				}
			}
			if !slices.Equal(order, want) {
				t.Fatalf("order = %v, want %v", order, want)
			}
		})
	}
}

// orderReporter records each report as "kind path". The first report
// waits for hold, so the reports after it wait in the queue, and a report
// that does not wait for it would finish first.
type orderReporter struct {
	plainProv
	hold    chan struct{}
	mu      sync.Mutex
	reports []string
}

func (r *orderReporter) record(kind string, track playlist.Track) {
	r.mu.Lock()
	first := len(r.reports) == 0
	r.mu.Unlock()
	if first {
		<-r.hold
	}
	r.mu.Lock()
	r.reports = append(r.reports, kind+" "+track.Path)
	r.mu.Unlock()
}

func (r *orderReporter) CanReportPlayback(playlist.Track) bool { return true }

func (r *orderReporter) ReportNowPlaying(track playlist.Track, _ time.Duration, _ bool) error {
	r.record("now-playing", track)
	return nil
}

func (r *orderReporter) ReportScrobble(track playlist.Track, _, _ time.Duration, _ bool) error {
	r.record("scrobble", track)
	return nil
}

func (r *orderReporter) ReportProgress(track playlist.Track, position time.Duration) error {
	r.record("progress "+position.String(), track)
	return nil
}

// The provider gets the now-playing, progress and scrobble reports of a
// track, and the next now-playing report, in the order the Model sent them.
// A newer progress report of the track replaces one that still waits.
func TestPlaybackReportsKeepOrder(t *testing.T) {
	a := playlist.Track{Title: "A", Path: "a.mp3", DurationSecs: 60}
	b := playlist.Track{Title: "B", Path: "b.mp3", DurationSecs: 60}
	reporter := &orderReporter{hold: make(chan struct{})}
	engine := &playbackFakeEngine{playing: true, position: 20 * time.Second, duration: time.Minute}
	m := Model{
		player:             engine,
		playlist:           playlist.New(),
		providers:          []provider.Entry{{Key: "p", Name: "P", Provider: reporter}},
		playingTrack:       a,
		playingTrackActive: true,
	}
	now := time.Now()
	m.nowPlaying(a)
	m.tickProgressReport(now)
	engine.position = 40 * time.Second
	m.tickProgressReport(now.Add(progressReportInterval))
	m.maybeScrobble(a, 40*time.Second, time.Minute)
	m.nowPlaying(b)
	close(reporter.hold)
	m.reports.waitIdle(t)

	want := []string{"now-playing a.mp3", "progress 40s a.mp3", "scrobble a.mp3", "now-playing b.mp3"}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if !slices.Equal(reporter.reports, want) {
		t.Fatalf("reports = %v, want %v", reporter.reports, want)
	}
}

// At exit, main waits a bounded time for the queued reports. The wait ends
// when the last report ran, or when the time is up while a report still
// runs.
func TestReportQueueWait(t *testing.T) {
	for _, tt := range []struct {
		name    string
		reports int
		release bool // the reports finish before the wait ends
		want    bool
	}{
		{name: "no report", want: true},
		{name: "finished reports", reports: 3, release: true, want: true},
		{name: "a report that still runs", reports: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{}
			release := make(chan struct{})
			defer close(release)
			var ran atomic.Int32
			for range tt.reports {
				m.queueReport("", func() {
					if !tt.release {
						<-release
					}
					ran.Add(1)
				})
			}
			timeout := 50 * time.Millisecond
			if tt.want {
				timeout = 5 * time.Second
			}
			if got := m.WaitReports(timeout); got != tt.want {
				t.Fatalf("WaitReports = %v, want %v", got, tt.want)
			}
			if tt.want && int(ran.Load()) != tt.reports {
				t.Fatalf("%d reports ran before the wait ended, want %d", ran.Load(), tt.reports)
			}
		})
	}
}
