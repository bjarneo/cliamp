package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
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
		providers:          []ProviderEntry{{Key: "stub", Name: "Plain", Provider: prov}},
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
		providers:          []ProviderEntry{{Key: "stub", Name: "Plain", Provider: prov}},
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
