package model

import (
	"context"
	"errors"
	"testing"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/playlist"
)

type fakeDJSession struct {
	tracks        []playlist.Track
	err           error
	calls, closed int
}

func (d *fakeDJSession) Next(context.Context) ([]playlist.Track, error) {
	d.calls++
	return d.tracks, d.err
}
func (d *fakeDJSession) Close() { d.closed++ }

func armTestDJ(t *testing.T, m *Model, d *fakeDJSession) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	m.spotifyDJ = spotifyDJState{ctx: ctx, cancel: cancel, session: d, gen: 10}
	t.Cleanup(cancel)
}

func djModelTrack(path string) playlist.Track {
	return playlist.Track{Path: "spotify:dj:" + path, Title: path, ProviderMeta: map[string]string{"spotify.dj": "true"}}
}

func TestDJBatchesContinueOnlyAtBoundary(t *testing.T) {
	m := keybindingTestModel()
	d := &fakeDJSession{tracks: []playlist.Track{djModelTrack("next")}}
	m.playlist.Replace([]playlist.Track{djModelTrack("one"), djModelTrack("two")})
	armTestDJ(t, &m, d)
	if cmd := m.nextTrack(); cmd == nil || d.calls != 0 || m.spotifyDJ.loading || m.playlist.Index() != 1 {
		t.Fatal("skip did not advance within batch")
	}
	cmd := m.nextTrack()
	if cmd == nil || !m.spotifyDJ.loading {
		t.Fatal("missing batch request")
	}
	if m.nextTrack() != nil {
		t.Fatal("duplicate batch request")
	}
	msg := cmd().(djBatchMsg)
	if d.calls != 1 {
		t.Fatal("batch not fetched")
	}
	if cmd := m.handleDJBatch(msg); cmd == nil || m.playlist.Len() != 1 || m.playlist.Tracks()[0].Title != "next" || m.spotifyDJ.session != d {
		t.Fatal("next batch not activated")
	}
}

func TestDJPreviousAndPreloadStayWithinBatch(t *testing.T) {
	m := keybindingTestModel()
	m.playlist.Replace([]playlist.Track{djModelTrack("one"), djModelTrack("two")})
	armTestDJ(t, &m, &fakeDJSession{})
	if cmd := m.skipPrev(); cmd != nil || m.playlist.Index() != 0 {
		t.Fatal("previous moved beyond the current DJ batch")
	}
	if _, ok := m.preloadTarget(); ok {
		t.Fatal("DJ batch armed a speculative narration preload")
	}
}

func TestDJReplacementRejectsLateBatches(t *testing.T) {
	m := keybindingTestModel()
	d := &fakeDJSession{tracks: []playlist.Track{djModelTrack("late")}}
	armTestDJ(t, &m, d)
	cmd := m.nextDJBatch()
	m.replacePlaylist([]playlist.Track{{Path: "ordinary.mp3"}})
	if d.closed != 1 || m.spotifyDJ.cancel != nil {
		t.Fatal("replacement kept DJ alive")
	}
	if follow := m.handleDJBatch(cmd().(djBatchMsg)); follow != nil || m.playlist.Tracks()[0].Path != "ordinary.mp3" {
		t.Fatal("late DJ replaced ordinary queue")
	}
}

func TestDJLoadingPauseAndErrors(t *testing.T) {
	m := keybindingTestModel()
	d := &fakeDJSession{tracks: []playlist.Track{djModelTrack("next")}}
	armTestDJ(t, &m, d)
	cmd := m.nextDJBatch()
	m.togglePlayPause()
	if follow := m.handleDJBatch(cmd().(djBatchMsg)); follow != nil || !m.spotifyDJ.paused {
		t.Fatal("batch ignored pause")
	}
	if follow := m.togglePlayPause(); follow == nil || m.spotifyDJ.paused {
		t.Fatal("paused batch did not resume")
	}
	m.spotifyDJ.loading = true
	failure := errors.New("narration unavailable")
	if follow := m.handleDJBatch(djBatchMsg{gen: m.spotifyDJ.gen, session: d, err: failure}); follow != nil || m.err != failure || m.spotifyDJ.cancel != nil {
		t.Fatal("DJ failure hidden")
	}
}

func TestDJOrderAndStop(t *testing.T) {
	m := keybindingTestModel()
	d := &fakeDJSession{}
	armTestDJ(t, &m, d)
	m.setShuffle(true)
	m.setRepeat(playlist.RepeatAll)
	if m.playlist.Shuffled() || m.playlist.Repeat() != playlist.RepeatOff {
		t.Fatal("DJ order changed")
	}
	m.stopByUser()
	if m.spotifyDJ.cancel != nil || d.closed != 1 {
		t.Fatal("stop kept DJ alive")
	}
}

func TestDJMediaControlsDuringBatchLoad(t *testing.T) {
	m := keybindingTestModel()
	armTestDJ(t, &m, &fakeDJSession{})
	m.spotifyDJ.loading = true
	for _, tc := range []struct {
		msg    interface{}
		paused bool
	}{
		{playback.PauseMsg{}, true}, {playback.PauseMsg{}, true},
		{playback.PlayMsg{}, false}, {playback.PlayMsg{}, false},
	} {
		next, cmd := m.update(tc.msg)
		m = next.(Model)
		if cmd != nil || m.spotifyDJ.paused != tc.paused {
			t.Fatalf("message %T paused=%v", tc.msg, m.spotifyDJ.paused)
		}
	}
}

func TestDJPauseDuringStreamSetupRetiresPlayback(t *testing.T) {
	m := keybindingTestModel()
	armTestDJ(t, &m, &fakeDJSession{})
	m.playlist.Replace([]playlist.Track{djModelTrack("one")})
	m.buffering = true
	gen := m.requests.stream
	if cmd := m.togglePlayPause(); cmd != nil || m.buffering || !m.spotifyDJ.paused || m.requests.stream == gen {
		t.Fatal("DJ setup ignored pause")
	}
	if cmd := m.togglePlayPause(); cmd == nil || m.spotifyDJ.paused {
		t.Fatal("DJ setup did not resume")
	}
}
