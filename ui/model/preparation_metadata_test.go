package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
)

func TestPreloadRefreshesEmbeddedMetadataBeforeActivation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "next.mp3")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Saved metadata can outlive the tags it originally came from.
	next := playlist.Track{
		Path: path, Title: "Saved title", Artist: "Saved artist",
		AlbumArtURL: "file:///removed-cover.jpg", EmbeddedLyrics: "removed lyrics",
	}
	queue := playlist.New()
	queue.Replace([]playlist.Track{{Path: "current.mp3"}, next})
	queue.SetIndex(0)
	engine := &playbackFakeEngine{playing: true}
	store := history.NewAt(filepath.Join(t.TempDir(), "history.toml"))
	m := Model{player: engine, playlist: queue, historyStore: store}
	m.playing = m.capturePlaybackTrack(queue.Tracks()[0], 0)

	captured := m.capturePlaybackTrack(next, 1)
	if captured.track.AlbumArtURL != next.AlbumArtURL || captured.track.EmbeddedLyrics != next.EmbeddedLyrics {
		t.Fatal("capturing playback identity refreshed embedded metadata")
	}
	cmd := m.preloadNext()
	if cmd == nil || m.preloaded == nil {
		t.Fatal("next track was not scheduled for preparation")
	}
	if m.preloaded.track.AlbumArtURL != next.AlbumArtURL || m.preloaded.track.EmbeddedLyrics != next.EmbeddedLyrics {
		t.Fatal("scheduling a preload refreshed embedded metadata before its command ran")
	}

	msg := cmd().(sourcePreparedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.track.AlbumArtURL != "" || msg.track.EmbeddedLyrics != "" {
		t.Fatalf("preparation retained removed embedded metadata: %+v", msg.track)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	engine.gaplessAdvanced = true
	if !m.consumeGaplessAdvance(nil) {
		t.Fatal("prepared track was not promoted")
	}
	active, index := m.activePlaybackTrack()
	if index < 0 || active.Path != path || active.Title != next.Title || active.Artist != next.Artist {
		t.Fatalf("activated track lost saved identity: %+v", active)
	}
	if active.AlbumArtURL != "" || active.EmbeddedLyrics != "" {
		t.Fatalf("gapless activation restored stale embedded metadata: %+v", active)
	}
	entries, err := store.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Track.Path != path || entries[0].Track.AlbumArtURL != "" || entries[0].Track.EmbeddedLyrics != "" {
		t.Fatalf("listening history propagated stale metadata: %+v", entries)
	}
}
