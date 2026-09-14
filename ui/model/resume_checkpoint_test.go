package model

import (
	"errors"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

func TestResumeCheckpointWaitsForSeekCompletion(t *testing.T) {
	for _, tt := range []struct {
		name string
		seek seekState
	}{
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
				player: engine, playlist: pl, playing: &playbackTrack{track: track},
				seek: tt.seek, cachedPos: 600 * time.Second,
			}
			var positions []int
			m.SetResumeSaver(func(_ playlist.Track, seconds int, _ []playlist.Track, _ int) {
				positions = append(positions, seconds)
			})
			m.tickResumeSave(time.Now())
			if len(positions) != 0 {
				t.Fatalf("saved unconfirmed playback positions %v", positions)
			}
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
		playing: &playbackTrack{track: track}, cachedPos: 600 * time.Second,
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
		playlist: pl, playing: &playbackTrack{track: jellyfinTrack}, vis: ui.NewVisualizer(44100),
	}
	seedGaplessPreload(&m, m.player.(*playbackFakeEngine), pl.Tracks()[1])
	m.SetVisualizer("none")
	var savedPositions []int
	m.SetResumeSaver(func(track playlist.Track, seconds int, _ []playlist.Track, _ int) {
		if track.Path == jellyfinTrack.Path {
			savedPositions = append(savedPositions, seconds)
		}
	})
	updated, _ := m.Update(tickMsg(time.Now()))
	if got := updated.(Model).playing.track.Path; got != "local.mp3" {
		t.Fatalf("gapless active path = %q, want local.mp3", got)
	}
	if len(savedPositions) != 0 {
		t.Fatalf("saved previous Jellyfin track with next track positions: %v", savedPositions)
	}
}

func TestQuitSavesActiveTrackWhileReplacementBuffers(t *testing.T) {
	track := playlist.Track{Path: "https://jf.example/Items/one/Download", Stream: true}
	next := playlist.Track{Path: "https://jf.example/Items/two/Download", Stream: true}
	engine := &playbackFakeEngine{playing: true, position: 90 * time.Second}
	m := Model{player: engine, playlist: playlist.New()}
	m.playlist.Add(track, next)
	m.setPlaybackTrack(track)
	if cmd := m.playTrack(next); cmd == nil || !m.buffering {
		t.Fatal("replacement did not begin buffering")
	}
	m.quit()
	if path, seconds, _ := m.ResumeState(); path != track.Path || seconds != 90 {
		t.Fatalf("captured (%q, %d), want active source at 90 seconds", path, seconds)
	}
}

func TestQuitReconcilesGaplessTrackPosition(t *testing.T) {
	tracks := []playlist.Track{{Path: "previous.mp3"}, {Path: "next.mp3"}}
	pl := playlist.New()
	pl.Add(tracks...)
	engine := &playbackFakeEngine{playing: true, position: 3 * time.Second, gaplessAdvanced: true}
	m := Model{player: engine, playlist: pl}
	m.setPlaybackTrack(tracks[0])
	seedGaplessPreload(&m, engine, tracks[1])
	m.quit()
	if path, seconds, _ := m.ResumeState(); path != tracks[1].Path || seconds != 3 {
		t.Fatalf("captured (%q, %d), want next track at 3 seconds", path, seconds)
	}
}

func TestResumeCheckpointSavesActiveSourceWhileReplacementBuffers(t *testing.T) {
	current := playlist.Track{Path: "https://jf.example/Items/one/Download", Stream: true}
	next := playlist.Track{Path: "https://jf.example/Items/two/Download", Stream: true}
	engine := &playbackFakeEngine{playing: true, position: 90 * time.Second}
	m := Model{player: engine, playlist: playlist.New(), cachedPos: 600 * time.Second}
	m.playlist.Add(current, next)
	m.setPlaybackTrack(current)
	var savedTrack playlist.Track
	position := -1
	m.SetResumeSaver(func(track playlist.Track, seconds int, _ []playlist.Track, _ int) {
		savedTrack, position = track, seconds
	})
	m.playTrack(next)
	m.tickResumeSave(time.Now())
	if savedTrack.Path != current.Path || position != 90 {
		t.Fatalf("checkpoint = (%q,%d), want active source at 90", savedTrack.Path, position)
	}
}
