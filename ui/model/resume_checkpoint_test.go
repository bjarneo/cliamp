package model

import (
	"errors"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

func TestResumeCheckpointWaitsForConfirmedPlayback(t *testing.T) {
	for _, tt := range []struct {
		name      string
		buffering bool
		seek      seekState
	}{
		{name: "buffering", buffering: true},
		{name: "seek preview", seek: seekState{active: true, targetPos: 600 * time.Second}},
		{name: "seek in flight", seek: seekState{inFlight: true}},
		{name: "pending seek", seek: seekState{pending: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			track := playlist.Track{Path: "https://jf.example/Items/one/Download", Stream: true}
			engine := &playbackFakeEngine{playing: true, position: 90 * time.Second}
			pl := playlist.New()
			pl.Add(track)
			m := Model{
				player: engine, playlist: pl, playingTrack: track, playingTrackActive: true,
				buffering: tt.buffering, seek: tt.seek, cachedPos: 600 * time.Second,
			}
			var positions []int
			m.SetResumeSaver(func(_ playlist.Track, seconds int, _ []playlist.Track, _ int) {
				positions = append(positions, seconds)
			})
			m.tickResumeSave(time.Now())
			if len(positions) != 0 {
				t.Fatalf("saved unconfirmed playback positions %v", positions)
			}
			m.buffering = false
			m.seek = seekState{}
			m.tickResumeSave(time.Now())
			if len(positions) != 1 || positions[0] != 90 {
				t.Fatalf("confirmed positions = %v, want [90] rather than stale display position", positions)
			}
		})
	}
}

func TestResumeCheckpointAfterFailedSeek(t *testing.T) {
	track := playlist.Track{Path: "https://jf.example/Items/one/Download", Stream: true}
	pl := playlist.New()
	pl.Add(track)
	m := Model{
		player: &playbackFakeEngine{playing: true, position: 90 * time.Second}, playlist: pl,
		playingTrack: track, playingTrackActive: true, cachedPos: 600 * time.Second,
		seek: seekState{active: true, inFlight: true, targetPos: 600 * time.Second},
	}
	position := -1
	m.SetResumeSaver(func(_ playlist.Track, seconds int, _ []playlist.Track, _ int) { position = seconds })
	updated, _ := m.Update(seekTickMsg{target: 600 * time.Second, err: errors.New("seek failed")})
	m = updated.(Model)
	m.tickResumeSave(time.Now())
	if position != 90 {
		t.Fatalf("saved failed seek at %d, want confirmed playback at 90", position)
	}
}

func TestResumeCheckpointReconcilesGaplessTrackFirst(t *testing.T) {
	jellyfinTrack := playlist.Track{Path: "https://jf.example/Items/one/Download", Stream: true}
	pl := playlist.New()
	pl.Add(jellyfinTrack, playlist.Track{Path: "local.mp3"})
	m := Model{
		player:   &playbackFakeEngine{playing: true, gaplessAdvanced: true, position: time.Second},
		playlist: pl, playingTrack: jellyfinTrack, playingTrackActive: true, vis: ui.NewVisualizer(44100),
	}
	m.SetVisualizer("none")
	var savedPositions []int
	m.SetResumeSaver(func(track playlist.Track, seconds int, _ []playlist.Track, _ int) {
		if track.Path == jellyfinTrack.Path {
			savedPositions = append(savedPositions, seconds)
		}
	})
	updated, _ := m.Update(tickMsg(time.Now()))
	if got := updated.(Model).playingTrack.Path; got != "local.mp3" {
		t.Fatalf("gapless active path = %q, want local.mp3", got)
	}
	if len(savedPositions) != 0 {
		t.Fatalf("saved previous Jellyfin track with next track positions: %v", savedPositions)
	}
}

func TestQuitSkipsUnconfirmedTrackPosition(t *testing.T) {
	for _, buffering := range []bool{false, true} {
		track := playlist.Track{Path: "https://jf.example/Items/one/Download", Stream: true}
		m := Model{
			player:       &playbackFakeEngine{playing: true, position: 90 * time.Second, gaplessAdvanced: !buffering},
			playingTrack: track, playingTrackActive: true, buffering: buffering,
		}
		m.quit()
		if path, seconds, _ := m.ResumeState(); path != "" || seconds != 0 {
			t.Fatalf("buffering=%t: captured mismatched pipeline position (%q, %d)", buffering, path, seconds)
		}
	}
}
