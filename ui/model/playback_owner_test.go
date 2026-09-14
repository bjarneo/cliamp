package model

import (
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
)

func (m *Model) setPlaybackTrack(track playlist.Track) {
	m.playing = m.capturePlaybackTrack(track, 0)
	m.playbackDetached = false
}

func ownedPath(m Model) string {
	if m.playing == nil {
		return ""
	}
	return m.playing.track.Path
}

func deliverPrepared(t *testing.T, m *Model, msg any) {
	t.Helper()
	updated, _ := m.Update(msg)
	*m = updated.(Model)
}

// Preparation must never alter the real engine. Whether the obsolete ready
// message arrives before or after a failed retry, A must still be audible.
func TestPreparedStreamCannotPlayAfterSuperseded(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	for _, failureFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready first", true: "failure first"}[failureFirst], func(t *testing.T) {
			engine := sharedPlayer
			engine.Stop()
			defer engine.Stop()
			aPath := filepath.Join(t.TempDir(), "a.wav")
			if err := os.WriteFile(aPath, ownershipTestWAV(30), 0o600); err != nil {
				t.Fatal(err)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				if requests > 1 {
					http.Error(w, "retry refused", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "audio/wav")
				_, _ = w.Write(ownershipTestWAV(60))
			}))
			defer server.Close()
			a := playlist.Track{Path: aPath, Title: "A", DurationSecs: 30}
			b := playlist.Track{Path: server.URL + "/b.wav", Title: "B", Stream: true}
			pl := playlist.New()
			pl.Add(a, b, b)
			pl.SetIndex(0)
			m := Model{player: engine, playlist: pl}
			m.playTrack(a)
			ready := m.nextTrack()().(sourcePreparedMsg)
			if ready.err != nil || engine.Duration() != 30*time.Second {
				t.Fatalf("preparation changed audio: err=%v duration=%v", ready.err, engine.Duration())
			}
			failure := m.nextTrack()().(sourcePreparedMsg)
			if failure.err == nil {
				t.Fatal("retry should fail")
			}
			messages := []sourcePreparedMsg{ready, failure}
			if failureFirst {
				messages[0], messages[1] = messages[1], messages[0]
			}
			for _, msg := range messages {
				deliverPrepared(t, &m, msg)
			}
			if ownedPath(m) != a.Path || engine.Duration() != 30*time.Second || !engine.IsPlaying() {
				t.Fatalf("failed retry displaced A: owner=%s duration=%v", ownedPath(m), engine.Duration())
			}
			if m.pending != nil || m.buffering || !errors.Is(m.err, failure.err) {
				t.Fatalf("retry did not settle: pending=%v buffering=%v err=%v", m.pending, m.buffering, m.err)
			}
		})
	}
}

// Failed starts must not record listening history, overwrite resume context,
// change the active display metadata, or claim playback in external reports.
func TestPreparationDoesNotApplyTrackEffects(t *testing.T) {
	a := playlist.Track{Path: "/a.flac", Title: "A"}
	b := playlist.Track{Path: "https://example.com/b.mp3", Title: "B", Stream: true}
	pl := playlist.New()
	pl.Add(a, b)
	pl.SetIndex(0)
	engine := &nowPlayingEngine{}
	var saved []string
	m := Model{player: engine, playlist: pl, historyStore: history.NewAt(filepath.Join(t.TempDir(), "history.toml")),
		resumeSaver: func(track playlist.Track, _ int, _ []playlist.Track, _ int) { saved = append(saved, track.Path) },
	}
	m.playTrack(a)
	m.streamTitle = "A metadata"
	engine.startErr = errors.New("offline")
	cmd := m.nextTrack()
	if displayed, _ := m.displayedPlaybackTrack(); displayed.Path != b.Path {
		t.Fatalf("loading display = %q", displayed.Path)
	}
	if active, _ := m.activePlaybackTrack(); active.Path != a.Path {
		t.Fatalf("reported active = %q", active.Path)
	}
	deliverPrepared(t, &m, cmd())
	entries, err := m.historyStore.Recent(0)
	if err != nil || len(entries) != 1 || entries[0].Track.Path != a.Path {
		t.Fatalf("history after failure = %+v, %v", entries, err)
	}
	if len(saved) != 1 || saved[0] != a.Path || m.streamTitle != "A metadata" || ownedPath(m) != a.Path {
		t.Fatalf("failed start changed active effects: saved=%v title=%q owner=%q", saved, m.streamTitle, ownedPath(m))
	}
}

func TestStopDiscardsAlreadyPreparedSource(t *testing.T) {
	b := playlist.Track{Path: "https://example.com/b.mp3", Stream: true}
	pl := playlist.New()
	pl.Add(b)
	engine := &playbackFakeEngine{}
	m := Model{player: engine, playlist: pl}
	ready := m.playTrack(b)()
	if engine.IsPlaying() {
		t.Fatal("preparation started playback")
	}
	m.stopPlayback()
	deliverPrepared(t, &m, ready)
	if engine.IsPlaying() || m.playing != nil || m.pending != nil || m.buffering {
		t.Fatal("late ready message revived stopped playback")
	}
}

func TestFailedRetryKeepsCommittedTrack(t *testing.T) {
	b := playlist.Track{Path: "https://example.com/b.mp3", Stream: true}
	pl := playlist.New()
	pl.Add(b, b)
	engine := &nowPlayingEngine{}
	m := Model{player: engine, playlist: pl}
	ready := m.playTrack(b)()
	deliverPrepared(t, &m, ready)
	engine.startErr = errors.New("retry refused")
	failure := m.nextTrack()()
	deliverPrepared(t, &m, failure)
	if ownedPath(m) != b.Path || !engine.IsPlaying() || m.pending != nil {
		t.Fatal("failed retry displaced committed source")
	}
}

func TestDetachedPreparationRetainsOriginalContext(t *testing.T) {
	a := playlist.Track{Path: "/a.flac"}
	b := playlist.Track{Path: "https://example.com/b.mp3", Stream: true}
	pl := playlist.New()
	pl.Add(a, b)
	pl.SetIndex(1)
	m := Model{player: &playbackFakeEngine{}, playlist: pl}
	ready := m.playTrack(b)()
	m.detachPlaybackTrack()
	m.replacePlaylist([]playlist.Track{{Path: "/other.flac"}})
	deliverPrepared(t, &m, ready)
	if !m.playbackDetached || ownedPath(m) != b.Path || m.playing.index != 1 || len(m.playing.context) != 2 {
		t.Fatalf("lost original context: %+v", m.playing)
	}
}

func ownershipTestWAV(seconds int) []byte {
	data := make([]byte, 44+seconds*44100*4)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 2)
	binary.LittleEndian.PutUint32(data[24:], 44100)
	binary.LittleEndian.PutUint32(data[28:], 44100*4)
	binary.LittleEndian.PutUint16(data[32:], 4)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	return data
}

type scrobbleResult struct {
	track    playlist.Track
	elapsed  time.Duration
	seekable bool
}

type activationReporter struct {
	nowPlayingProv
	finished chan scrobbleResult
}

func (p *activationReporter) ReportScrobble(track playlist.Track, elapsed, _ time.Duration, seekable bool) error {
	p.finished <- scrobbleResult{track, elapsed, seekable}
	return nil
}

func TestReplacementScrobblesOnlyWhenItCommits(t *testing.T) {
	a := playlist.Track{Path: "/a.flac", DurationSecs: 120}
	b := playlist.Track{Path: "https://example.com/b.mp3", Stream: true}
	pl := playlist.New()
	pl.Add(a, b)
	engine := &playbackFakeEngine{duration: 120 * time.Second, seekable: true}
	reporter := &activationReporter{nowPlayingProv: nowPlayingProv{reports: make(chan playlist.Track, 4)}, finished: make(chan scrobbleResult, 4)}
	m := Model{player: engine, playlist: pl, providers: []ProviderEntry{{Provider: reporter}}}
	m.playTrack(a)
	engine.position = 90 * time.Second
	ready := m.nextTrack()()
	select {
	case result := <-reporter.finished:
		t.Fatalf("preparation scrobbled before replacement: %+v", result)
	default:
	}
	deliverPrepared(t, &m, ready)
	select {
	case result := <-reporter.finished:
		if result.track.Path != a.Path || result.elapsed != 90*time.Second || !result.seekable {
			t.Fatalf("wrong outgoing source statistics: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("committed replacement did not scrobble the outgoing track")
	}
}

func TestResumeDoesNotPairOldTrackWithPromotedSource(t *testing.T) {
	a := playlist.Track{Path: "/a.flac"}
	engine := &playbackFakeEngine{playing: true, currentTicket: 2, position: 12 * time.Second}
	pl := playlist.New()
	pl.Add(a)
	var saved []string
	m := Model{player: engine, playlist: pl, playing: &playbackTrack{ticket: 1, track: a},
		resumeSaver: func(track playlist.Track, _ int, _ []playlist.Track, _ int) { saved = append(saved, track.Path) },
	}
	m.tickResumeSave(time.Now())
	if len(saved) != 0 {
		t.Fatalf("paired old track with promoted source's position: %v", saved)
	}
}

type advanceOnClearEngine struct {
	playbackFakeEngine
	advanceOnClear bool
}

func (e *advanceOnClearEngine) ClearPreload() {
	if e.advanceOnClear {
		e.advanceOnClear = false
		e.gaplessAdvanced = true
	}
	e.playbackFakeEngine.ClearPreload()
}

func TestNextSettlesGaplessBeforeMovingPlaylist(t *testing.T) {
	a := playlist.Track{Path: "/a.flac"}
	b := playlist.Track{Path: "/b.flac"}
	c := playlist.Track{Path: "/c.flac"}
	pl := playlist.New()
	pl.Add(a, b, c)
	pl.SetIndex(0)
	engine := &advanceOnClearEngine{playbackFakeEngine: playbackFakeEngine{playing: true, currentTicket: 1, startSeq: 2, nextTicket: 2}, advanceOnClear: true}
	m := Model{player: engine, playlist: pl}
	m.playing = m.capturePlaybackTrack(a, 1)
	m.preloaded = m.capturePlaybackTrack(b, 2)
	m.nextTrack()
	current, _ := pl.Current()
	if current.Path != c.Path || ownedPath(m) != c.Path {
		t.Fatalf("navigation and audio disagree: playlist=%q audio=%q", current.Path, ownedPath(m))
	}
}
