package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// resumeProv is a stub provider that reports a fixed resume target.
type resumeProv struct {
	idx    int
	offset time.Duration
	tracks []playlist.Track
}

func (p *resumeProv) Name() string { return "Stub" }

func (p *resumeProv) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{{ID: "b:book-1", Name: "Book"}}, nil
}

func (p *resumeProv) Tracks(string) ([]playlist.Track, error) { return p.tracks, nil }

func (p *resumeProv) ResumeTarget(string, []playlist.Track) (int, time.Duration) {
	return p.idx, p.offset
}

// plainProv implements only the base provider interface.
type plainProv struct{ tracks []playlist.Track }

func (p *plainProv) Name() string { return "Plain" }

func (p *plainProv) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }

func (p *plainProv) Tracks(string) ([]playlist.Track, error) { return p.tracks, nil }

func stubTracks() []playlist.Track {
	return []playlist.Track{
		{Path: "https://abs/one", Stream: true},
		{Path: "https://abs/two", Stream: true},
	}
}

func TestFetchTracksCmdCarriesResumeTarget(t *testing.T) {
	prov := &resumeProv{idx: 1, offset: 90 * time.Second, tracks: stubTracks()}

	msg, ok := fetchTracksCmd(prov, "b:book-1", 1)().(tracksLoadedMsg)
	if !ok {
		t.Fatal("expected a tracksLoadedMsg")
	}
	if msg.resumeIdx != 1 || msg.resumeOffset != 90*time.Second {
		t.Fatalf("resume = (%d, %v), want (1, 1m30s)", msg.resumeIdx, msg.resumeOffset)
	}
}

func TestFetchTracksCmdWithoutResumeCapability(t *testing.T) {
	prov := &plainProv{tracks: stubTracks()}

	msg, ok := fetchTracksCmd(prov, "anything", 1)().(tracksLoadedMsg)
	if !ok {
		t.Fatal("expected a tracksLoadedMsg")
	}
	if msg.resumeIdx != 0 || msg.resumeOffset != 0 {
		t.Fatalf("resume = (%d, %v), want (0, 0)", msg.resumeIdx, msg.resumeOffset)
	}
}

func TestApplyTracksResume(t *testing.T) {
	tests := []struct {
		name         string
		preArmedPath string
		preArmedSecs int
		msg          tracksLoadedMsg
		wantCursor   int
		wantPath     string
		wantSecs     int
	}{
		{
			name: "resumes second track",
			msg: tracksLoadedMsg{
				tracks:       stubTracks(),
				resumeIdx:    1,
				resumeOffset: 90 * time.Second,
			},
			wantCursor: 1,
			wantPath:   "https://abs/two",
			wantSecs:   90,
		},
		{
			name:       "no resume offset",
			msg:        tracksLoadedMsg{tracks: stubTracks()},
			wantCursor: 0,
		},
		{
			name: "index out of range",
			msg: tracksLoadedMsg{
				tracks:       stubTracks(),
				resumeIdx:    9,
				resumeOffset: 30 * time.Second,
			},
			wantCursor: 0,
		},
		{
			name:         "stale armed state cleared when new list lacks the armed path",
			preArmedPath: "https://abs/finished-book",
			preArmedSecs: 1200,
			msg:          tracksLoadedMsg{tracks: stubTracks()},
			wantCursor:   0,
			wantPath:     "",
			wantSecs:     0,
		},
		{
			name:         "armed state preserved when new list still contains the armed path",
			preArmedPath: "https://abs/one",
			preArmedSecs: 45,
			msg:          tracksLoadedMsg{tracks: stubTracks()},
			wantCursor:   0,
			wantPath:     "https://abs/one",
			wantSecs:     45,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{}
			m.resume.path = tt.preArmedPath
			m.resume.secs = tt.preArmedSecs
			m.applyTracksResume(tt.msg)

			if m.plCursor != tt.wantCursor {
				t.Fatalf("plCursor = %d, want %d", m.plCursor, tt.wantCursor)
			}
			if m.resume.path != tt.wantPath {
				t.Fatalf("resume.path = %q, want %q", m.resume.path, tt.wantPath)
			}
			if m.resume.secs != tt.wantSecs {
				t.Fatalf("resume.secs = %d, want %d", m.resume.secs, tt.wantSecs)
			}
		})
	}
}

func TestTracksLoadedResumeKeepsCursorVisible(t *testing.T) {
	tracks := make([]playlist.Track, 40)
	for i := range tracks {
		tracks[i] = playlist.Track{Path: "https://abs/chapter", Title: "Chapter"}
	}
	prov := &resumeProv{}
	m := Model{
		player:   &playbackFakeEngine{},
		playlist: playlist.New(),
		provider: prov,
	}
	m.requests.tracks = 1

	updated, _ := m.Update(tracksLoadedMsg{
		tracks:       tracks,
		providerName: prov.Name(),
		resumeIdx:    30,
		resumeOffset: 90 * time.Second,
		gen:          1,
	})
	got := updated.(Model)
	visible := got.effectivePlaylistVisible()
	if got.plCursor != 30 || got.plScroll == 0 || got.plCursor-got.plScroll >= visible {
		t.Fatalf("resume viewport = cursor:%d scroll:%d visible:%d", got.plCursor, got.plScroll, visible)
	}
}

// progressProv records interim progress reports.
type progressProv struct {
	plainProv
	reports chan time.Duration
}

func (p *progressProv) CanReportPlayback(playlist.Track) bool { return true }

func (p *progressProv) ReportNowPlaying(playlist.Track, time.Duration, bool) error { return nil }

func (p *progressProv) ReportScrobble(playlist.Track, time.Duration, time.Duration, bool) error {
	return nil
}

func (p *progressProv) ReportProgress(_ playlist.Track, position time.Duration) error {
	p.reports <- position
	return nil
}

func TestTickProgressReportThrottles(t *testing.T) {
	prov := &progressProv{reports: make(chan time.Duration, 4)}
	m := Model{
		player:             &playbackFakeEngine{playing: true, position: 42 * time.Second},
		provider:           prov,
		providers:          []provider.Entry{{Key: "stub", Name: "Plain", Provider: prov}},
		playingTrack:       stubTracks()[0],
		playingTrackActive: true,
	}

	now := time.Now()
	m.tickProgressReport(now)
	select {
	case <-prov.reports:
	case <-time.After(time.Second):
		t.Fatal("expected a progress report on the first tick")
	}

	m.tickProgressReport(now.Add(time.Second))
	select {
	case <-prov.reports:
		t.Fatal("progress reported again inside the throttle window")
	case <-time.After(50 * time.Millisecond):
	}

	m.tickProgressReport(now.Add(progressReportInterval + time.Second))
	select {
	case <-prov.reports:
	case <-time.After(time.Second):
		t.Fatal("expected a progress report after the throttle window")
	}
}

func TestTickProgressReportSkipsWhenPaused(t *testing.T) {
	prov := &progressProv{reports: make(chan time.Duration, 1)}
	m := Model{
		player:             &playbackFakeEngine{playing: true, paused: true},
		provider:           prov,
		providers:          []provider.Entry{{Key: "stub", Name: "Plain", Provider: prov}},
		playingTrack:       stubTracks()[0],
		playingTrackActive: true,
	}

	m.tickProgressReport(time.Now())
	select {
	case <-prov.reports:
		t.Fatal("progress reported while paused")
	case <-time.After(50 * time.Millisecond):
	}
}

// timelineProv records the reports of a TimelineReporter as "kind position".
type timelineProv struct {
	progressProv
	timeline chan string
}

func newTimelineProv() *timelineProv {
	return &timelineProv{progressProv: progressProv{reports: make(chan time.Duration, 8)}, timeline: make(chan string, 8)}
}

func (p *timelineProv) ReportProgress(_ playlist.Track, position time.Duration) error {
	p.timeline <- "playing " + position.String()
	return nil
}

func (p *timelineProv) ReportPaused(_ playlist.Track, position time.Duration) error {
	p.timeline <- "paused " + position.String()
	return nil
}

func (p *timelineProv) ReportStopped(_ playlist.Track, position time.Duration) error {
	p.timeline <- "stopped " + position.String()
	return nil
}

func (p *timelineProv) ReportScrobble(_ playlist.Track, elapsed, _ time.Duration, _ bool) error {
	p.timeline <- "scrobble " + elapsed.String()
	return nil
}

// expect fails unless the next report is want, or, for want "", unless no
// report comes.
func (p *timelineProv) expect(t *testing.T, want string) {
	t.Helper()
	if want == "" {
		select {
		case got := <-p.timeline:
			t.Fatalf("unexpected report %q", got)
		case <-time.After(50 * time.Millisecond):
		}
		return
	}
	select {
	case got := <-p.timeline:
		if got != want {
			t.Fatalf("report = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("no report, want %q", want)
	}
}

func timelineModel(prov *timelineProv, engine *playbackFakeEngine) Model {
	return Model{
		player:              engine,
		provider:            prov,
		providers:           []provider.Entry{{Key: "stub", Name: "Plain", Provider: prov}},
		playingTrack:        stubTracks()[0],
		playingTrackActive:  true,
		playingTrackStarted: true,
	}
}

// A TimelineReporter hears of a pause and of the resume at once, inside the
// throttle window, and gets the paused position after it.
func TestTickProgressReportsPause(t *testing.T) {
	prov := newTimelineProv()
	engine := &playbackFakeEngine{playing: true, position: 10 * time.Second}
	m := timelineModel(prov, engine)

	now := time.Now()
	m.tickProgressReport(now)
	prov.expect(t, "playing 10s")

	engine.paused = true
	m.tickProgressReport(now.Add(time.Second))
	prov.expect(t, "paused 10s")
	m.tickProgressReport(now.Add(2 * time.Second))
	prov.expect(t, "")
	m.tickProgressReport(now.Add(time.Second + progressReportInterval))
	prov.expect(t, "paused 10s")

	engine.paused = false
	m.tickProgressReport(now.Add(2*time.Second + progressReportInterval))
	prov.expect(t, "playing 10s")
}

// A seek reports the new position at once; the position moving on with
// playback, at the playback speed, waits for the interval.
func TestTickProgressReportsSeek(t *testing.T) {
	for _, tt := range []struct {
		name   string
		speed  float64
		paused bool
		at     time.Duration // position one second after the first report
		want   string
	}{
		{name: "plays on", at: 11 * time.Second},
		{name: "plays on faster", speed: 2, at: 12 * time.Second},
		{name: "seek forward", at: 70 * time.Second, want: "playing 1m10s"},
		{name: "seek back", at: 3 * time.Second, want: "playing 3s"},
		{name: "seek while paused", paused: true, at: 70 * time.Second, want: "paused 1m10s"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prov := newTimelineProv()
			engine := &playbackFakeEngine{playing: true, paused: tt.paused, position: 10 * time.Second, speed: tt.speed}
			m := timelineModel(prov, engine)
			now := time.Now()
			m.tickProgressReport(now)
			<-prov.timeline
			engine.position = tt.at
			m.tickProgressReport(now.Add(time.Second))
			prov.expect(t, tt.want)
		})
	}
}

// No report goes out while the track buffers.
func TestTickProgressReportWaitsForBuffering(t *testing.T) {
	prov := newTimelineProv()
	m := timelineModel(prov, &playbackFakeEngine{playing: true})
	m.buffering = true
	m.tickProgressReport(time.Now())
	prov.expect(t, "")
}

// Leaving a track scrobbles it past half its length; before that, a
// TimelineReporter hears that the listen stopped, so the server does not
// show it as playing on.
func TestLeaveTrackEndsListen(t *testing.T) {
	for _, tt := range []struct {
		name    string
		elapsed time.Duration
		want    string
	}{
		{name: "skipped early", elapsed: 20 * time.Second, want: "stopped 20s"},
		{name: "played past half", elapsed: 150 * time.Second, want: "scrobble 2m30s"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prov := newTimelineProv()
			m := timelineModel(prov, &playbackFakeEngine{playing: true})
			m.leaveTrack(tt.elapsed, 200*time.Second)
			prov.expect(t, tt.want)
			prov.expect(t, "")
		})
	}
}
