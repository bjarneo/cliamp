package ytmusic

import (
	"bytes"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// The Ephemeral flag marks queue entries the player owns for the current
// session. It must never survive a cache round trip, or restored tracks would
// be silently deleted the next time the user plays something.
func TestYTCacheDropsEphemeralFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	scope := "test-scope"
	cache := newYTCache(scope)
	cache.setTracks("list", []playlist.Track{{Path: "https://music.youtube.com/watch?v=abcdefghijk", Title: "Cached", Ephemeral: true}})

	data := cache.snapshot()
	if bytes.Contains(data, []byte("Ephemeral")) {
		t.Errorf("snapshot serialized the Ephemeral field: %s", data)
	}
	saveSnapshot(data)

	loaded := loadYTCache(scope)
	tracks, ok := loaded.tracksFresh("list")
	if !ok || len(tracks) != 1 {
		t.Fatalf("tracksFresh: ok=%v len=%d, want true and 1", ok, len(tracks))
	}
	if tracks[0].Ephemeral {
		t.Error("cached track restored with Ephemeral = true")
	}
	if tracks[0].Title != "Cached" {
		t.Errorf("Title = %q, want %q", tracks[0].Title, "Cached")
	}
}
