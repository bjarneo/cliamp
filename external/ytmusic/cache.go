package ytmusic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"cliamp/internal/appdir"
	"cliamp/playlist"
)

// cacheTTL is how long cached playlist/track data is considered fresh.
// After this, data is refetched from the API on next access.
const cacheTTL = 24 * time.Hour

// ytCache stores playlists and tracks on disk for fast startup.
// Path: ~/.config/cliamp/ytmusic_cache.json
type ytCache struct {
	Playlists   []playlistEntry            `json:"playlists,omitempty"`
	PlaylistsAt time.Time                  `json:"playlists_at"`
	Tracks      map[string]cachedTrackList `json:"tracks,omitempty"`
}

type cachedTrackList struct {
	Items     []playlist.Track `json:"items"`
	FetchedAt time.Time        `json:"fetched_at"`
}

func ytCachePath() string {
	dir, err := appdir.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ytmusic_cache.json")
}

func newYTCache() *ytCache {
	return &ytCache{Tracks: make(map[string]cachedTrackList)}
}

func loadYTCache() *ytCache {
	data, err := os.ReadFile(ytCachePath())
	if err != nil {
		return newYTCache()
	}
	var c ytCache
	if err := json.Unmarshal(data, &c); err != nil {
		return newYTCache()
	}
	if c.Tracks == nil {
		c.Tracks = make(map[string]cachedTrackList)
	}
	return &c
}

// snapshot returns a JSON-encoded copy of the cache. Call under the mutex
// so json.Marshal doesn't race with concurrent setPlaylists/setTracks calls.
func (c *ytCache) snapshot() []byte {
	data, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	return data
}

// save writes previously-snapshotted data to disk. Safe to call without a lock.
func saveSnapshot(data []byte) {
	if data == nil {
		return
	}
	path := ytCachePath()
	if path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, data, 0o600)
}

func (c *ytCache) playlistsFresh() bool {
	return len(c.Playlists) > 0 && time.Since(c.PlaylistsAt) < cacheTTL
}

func (c *ytCache) tracksFresh(playlistID string) ([]playlist.Track, bool) {
	ct, ok := c.Tracks[playlistID]
	if !ok || len(ct.Items) == 0 {
		return nil, false
	}
	if time.Since(ct.FetchedAt) >= cacheTTL {
		return nil, false
	}
	return ct.Items, true
}

func (c *ytCache) setPlaylists(pl []playlistEntry) {
	c.Playlists = pl
	c.PlaylistsAt = time.Now()
}

func (c *ytCache) setTracks(playlistID string, tracks []playlist.Track) {
	c.Tracks[playlistID] = cachedTrackList{
		Items:     tracks,
		FetchedAt: time.Now(),
	}
}
