package lyrion

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// liveClient returns a client for a real LMS instance, or skips.
//
// Run against your own server with:
//
//	CLIAMP_LYRION_LIVE_URL=http://lms.local:9000 go test ./external/lyrion/ -run Live -v
//
// Set CLIAMP_LYRION_LIVE_USER and CLIAMP_LYRION_LIVE_PASS as well to exercise
// the authenticated path against a password-protected server.
func liveClient(t *testing.T) *Client {
	t.Helper()
	base := os.Getenv("CLIAMP_LYRION_LIVE_URL")
	if base == "" {
		t.Skip("CLIAMP_LYRION_LIVE_URL not set")
	}
	return New(base, os.Getenv("CLIAMP_LYRION_LIVE_USER"), os.Getenv("CLIAMP_LYRION_LIVE_PASS"))
}

func livePlayableTrack(t *testing.T, c *Client) playlist.Track {
	t.Helper()

	const probePage = 25
	for offset := 0; ; offset += probePage {
		albums, err := c.AlbumList("", offset, probePage)
		if err != nil {
			t.Fatalf("AlbumList(offset=%d) for probe: %v", offset, err)
		}
		if len(albums) == 0 {
			break
		}
		for _, album := range albums {
			tracks, err := c.AlbumTracks(album.ID)
			if err != nil {
				t.Fatalf("AlbumTracks(%s): %v", album.ID, err)
			}
			for _, track := range tracks {
				if !track.Unplayable {
					return track
				}
			}
		}
	}
	t.Fatal("no album in the catalogue had a playable track")
	return playlist.Track{}
}

func TestLiveBrowse(t *testing.T) {
	c := liveClient(t)

	if err := c.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	artists, err := c.Artists()
	if err != nil {
		t.Fatalf("Artists: %v", err)
	}
	if len(artists) == 0 {
		t.Fatal("no artists returned from a live library")
	}
	if artists[0].ID == "" || artists[0].Name == "" {
		t.Errorf("artist missing id or name: %+v", artists[0])
	}

	if _, err := c.Playlists(); err != nil {
		t.Fatalf("Playlists: %v", err)
	}

	albums, err := c.AlbumList("", 0, 5)
	if err != nil {
		t.Fatalf("AlbumList: %v", err)
	}
	if len(albums) == 0 {
		t.Fatal("no albums returned from a live library")
	}
	// Album artist comes back only when the uppercase S tag is right.
	if albums[0].Artist == "" || albums[0].ArtistID == "" {
		t.Errorf("album missing artist or artist_id — check albumTags: %+v", albums[0])
	}

	page2, err := c.AlbumList("", 5, 5)
	if err != nil {
		t.Fatalf("AlbumList page 2: %v", err)
	}
	if len(page2) > 0 && page2[0].ID == albums[0].ID {
		t.Error("offset was ignored: page 2 starts at the same album as page 1")
	}

	if _, err := c.ArtistAlbums(artists[0].ID); err != nil {
		t.Fatalf("ArtistAlbums: %v", err)
	}

	// Every tag letter must survive the round trip; LMS silently omits fields
	// for tags it was not asked for, so a wrong tag set shows up as blanks.
	tr := livePlayableTrack(t, c)
	if tr.Title == "" {
		t.Error("track missing title")
	}
	if tr.Album == "" {
		t.Error("track missing album — check songTags")
	}
	if tr.DurationSecs == 0 {
		t.Error("track missing duration — check songTags")
	}
	if !tr.Stream {
		t.Error("track not marked as a stream")
	}
	resolved, _, err := c.ResolveSource(tr.Path)
	if err != nil {
		t.Fatalf("ResolveSource(%q): %v", tr.Path, err)
	}
	if !IsStreamURL(resolved) {
		t.Errorf("IsStreamURL(%q) = false for a live track", resolved)
	}
}

func TestLiveSearch(t *testing.T) {
	c := liveClient(t)

	track := livePlayableTrack(t, c)
	found, err := c.SearchTracks(context.Background(), track.Title, 5)
	if err != nil {
		t.Fatalf("SearchTracks: %v", err)
	}
	if len(found) == 0 {
		t.Fatalf("no results for known track title %q in a live library", track.Title)
	}
	if len(found) > 5 {
		t.Errorf("got %d results, want the limit of 5 respected", len(found))
	}

	empty, err := c.SearchTracks(context.Background(), "zzzzqqqqxxxxvvvv", 5)
	if err != nil {
		t.Fatalf("SearchTracks with no matches: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("got %d results for a nonsense query, want 0", len(empty))
	}
}

// TestLiveStream fetches real audio bytes, which is the only way to confirm the
// download endpoint and any configured credentials actually work.
func TestLiveStream(t *testing.T) {
	c := liveClient(t)

	uri := livePlayableTrack(t, c).Path

	// Track paths are credential-free URIs; the player resolves them at play
	// time, so the test has to do the same.
	playable, _, err := c.ResolveSource(uri)
	if err != nil {
		t.Fatalf("ResolveSource(%q): %v", uri, err)
	}

	resp, err := httpClient.Get(playable)
	if err != nil {
		t.Fatalf("stream GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %s", resp.Status)
	}
	n, err := io.ReadFull(resp.Body, make([]byte, 32<<10))
	if err != nil && err != io.ErrUnexpectedEOF {
		t.Fatalf("reading audio: %v", err)
	}
	if n == 0 {
		t.Error("stream delivered no bytes")
	}
}
