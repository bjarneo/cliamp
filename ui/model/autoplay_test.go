package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func autoplayTestModel(paths ...string) *Model {
	pl := playlist.New()
	for _, p := range paths {
		pl.Add(playlist.Track{Path: p, Title: p})
	}
	m := &Model{player: &playbackFakeEngine{}, playlist: pl, autoplayRadio: true}
	return m
}

func TestAutoplayEligibleSeed(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)

	mixURL, ok := m.autoplayEligibleSeed()
	if !ok {
		t.Fatal("eligible seed: ok = false, want true")
	}
	want := "https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=RDdQw4w9WgXcQ"
	if mixURL != want {
		t.Errorf("mixURL = %q, want %q", mixURL, want)
	}

	m.autoplayRadio = false
	if _, ok := m.autoplayEligibleSeed(); ok {
		t.Error("disabled autoplay: ok = true, want false")
	}
	m.autoplayRadio = true

	m.autoplayFailedSeed = track.Path
	if _, ok := m.autoplayEligibleSeed(); ok {
		t.Error("failed seed: ok = true, want false")
	}
}

func TestAutoplayEligibleSeedNonYouTube(t *testing.T) {
	m := autoplayTestModel("/home/user/song.mp3")
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)
	if _, ok := m.autoplayEligibleSeed(); ok {
		t.Error("local file seed: ok = true, want false")
	}
}

func TestContinueWithAutoplayStartsFetchOnce(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	m.playlist.SetIndex(0)
	track, _ := m.playlist.Current()
	m.setPlaybackTrack(track)

	cmd, pending := m.continueWithAutoplay()
	if !pending || cmd == nil {
		t.Fatalf("first call: cmd=%v pending=%v, want non-nil cmd + pending", cmd, pending)
	}
	if !m.autoplayLoading {
		t.Error("autoplayLoading not set")
	}
	// Second call while in flight: pending, but no duplicate fetch.
	cmd2, pending2 := m.continueWithAutoplay()
	if !pending2 || cmd2 != nil {
		t.Errorf("second call: cmd=%v pending=%v, want nil cmd + pending", cmd2, pending2)
	}
}

func TestAppendAutoplayTracksDedupes(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=seed00000001")
	added := m.appendAutoplayTracks([]playlist.Track{
		{Path: "https://www.youtube.com/watch?v=seed00000001", Title: "seed again"},   // dup of queue entry
		{Path: "https://music.youtube.com/watch?v=seed00000001", Title: "seed music"}, // same ID, other host
		{Path: "https://www.youtube.com/watch?v=fresh0000001", Title: "fresh 1"},
		{Path: "https://www.youtube.com/watch?v=fresh0000002", Title: "fresh 2"},
		{Path: "https://www.youtube.com/watch?v=fresh0000001", Title: "fresh 1 dup"},
	})
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if m.playlist.Len() != 3 {
		t.Errorf("playlist len = %d, want 3", m.playlist.Len())
	}
}

func TestAppendAutoplayTracksAllDuplicates(t *testing.T) {
	m := autoplayTestModel("https://www.youtube.com/watch?v=seed00000001")
	added := m.appendAutoplayTracks([]playlist.Track{
		{Path: "https://www.youtube.com/watch?v=seed00000001"},
	})
	if added != 0 {
		t.Errorf("added = %d, want 0", added)
	}
	if m.playlist.Len() != 1 {
		t.Errorf("playlist len = %d, want 1", m.playlist.Len())
	}
}
