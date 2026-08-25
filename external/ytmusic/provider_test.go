package ytmusic

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestRefreshInvalidatesAllCaches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	b := newBase(nil, "client-id", "client-secret", false)

	b.allPlaylists = []playlistEntry{{ID: "p1", Name: "One", TrackCount: 5}}
	b.classified = map[string]bool{"p1": true}
	b.trackCache["p1"] = []playlist.Track{{Path: "https://example/v", Title: "t"}}

	dc := b.ensureDiskCache()
	dc.setPlaylists(b.allPlaylists)
	dc.setTracks("p1", b.trackCache["p1"])
	saveSnapshot(dc.snapshot())

	if !dc.playlistsFresh() {
		t.Fatal("disk cache should be fresh before refresh")
	}

	b.refresh()

	if b.allPlaylists != nil {
		t.Error("allPlaylists not cleared")
	}
	if b.classified != nil {
		t.Error("classified not cleared")
	}
	if len(b.trackCache) != 0 {
		t.Errorf("trackCache not cleared: %d entries", len(b.trackCache))
	}

	if b.disk.playlistsFresh() {
		t.Error("disk cache still reports fresh after refresh")
	}
	if !b.disk.PlaylistsAt.IsZero() {
		t.Errorf("PlaylistsAt should be zero, got %v", b.disk.PlaylistsAt)
	}
	if len(b.disk.Playlists) != 0 {
		t.Errorf("disk Playlists not cleared: %d entries", len(b.disk.Playlists))
	}
	if len(b.disk.Tracks) != 0 {
		t.Errorf("disk Tracks not cleared: %d entries", len(b.disk.Tracks))
	}

	reloaded := loadYTCache(storedOAuthCacheScope("client-id"))
	if reloaded.playlistsFresh() {
		t.Error("reloaded disk cache still fresh after refresh")
	}
	if !reloaded.PlaylistsAt.Equal(time.Time{}) {
		t.Errorf("reloaded PlaylistsAt should be zero, got %v", reloaded.PlaylistsAt)
	}
	if len(reloaded.Tracks) != 0 {
		t.Errorf("reloaded disk Tracks not cleared: %d entries", len(reloaded.Tracks))
	}
}
