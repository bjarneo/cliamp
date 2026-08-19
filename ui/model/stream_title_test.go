package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// TestResolveTrackDisplayStreamTitle covers the ICY override a radio stream
// needs: its playlist entry only knows the station name. Audiobookshelf tracks
// are also Stream: true, so the no-metadata case matters for them too --
// streamTitle is cleared on every track change, and their own title must stand.
func TestResolveTrackDisplayStreamTitle(t *testing.T) {
	tests := []struct {
		name        string
		streamTitle string
		track       playlist.Track
		wantArtist  string
		wantTitle   string
	}{
		{
			name:        "icy artist and title split on separator",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Title: "NCS Trap Stream", Stream: true},
			wantArtist:  "Tycho",
			wantTitle:   "Awake",
		},
		{
			name:        "icy value without separator becomes the title",
			streamTitle: "Morning Session",
			track:       playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle:   "Morning Session",
		},
		{
			// "Artist - " cuts to an empty title; keep the stored one rather than
			// blanking it. The daemon guards the same case.
			name:        "icy value with an empty title keeps the station name",
			streamTitle: "Tycho - ",
			track:       playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle:   "Lofi Stream",
		},
		{
			name:      "no icy metadata keeps the station name",
			track:     playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle: "Lofi Stream",
		},
		{
			// Library providers (Navidrome, Plex, Jellyfin, Emby, Qobuz, NetEase,
			// Audiobookshelf) are Stream: true too, so they take the same branch
			// as radio and must keep their own title and author.
			name: "audiobook stream without icy keeps its own metadata",
			track: playlist.Track{
				Title:  "Tortilla Face",
				Artist: "Bobby Lee & Andrew Santino",
				Album:  "Bad Friends",
				Stream: true,
			},
			wantArtist: "Bobby Lee & Andrew Santino",
			wantTitle:  "Tortilla Face",
		},
		{
			// A stale ICY title must never bleed onto a non-stream track.
			name:        "spotify track ignores a leftover stream title",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Title: "Alien Boy", Artist: "Oliver Tree"},
			wantArtist:  "Oliver Tree",
			wantTitle:   "Alien Boy",
		},
		{
			// " - " in a real track title is not an ICY separator to split on.
			name:       "spotify remix title keeps its separator",
			track:      playlist.Track{Title: "Counting - Taiki Nulight Remix", Artist: "Hamdi"},
			wantArtist: "Hamdi",
			wantTitle:  "Counting - Taiki Nulight Remix",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{streamTitle: tc.streamTitle}
			artist, title := m.resolveTrackDisplay(tc.track)
			if artist != tc.wantArtist || title != tc.wantTitle {
				t.Errorf("resolveTrackDisplay() = (%q, %q), want (%q, %q)",
					artist, title, tc.wantArtist, tc.wantTitle)
			}
		})
	}
}

// TestIPCTrackInfoKeepsAlbum pins the show/book name a podcast or audiobook
// carries in Album: the stream branch rewrites Title and Artist, so Album has to
// come through ipcTrackInfo untouched for a client to render "Bad Friends".
func TestIPCTrackInfoKeepsAlbum(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
	}{
		{
			name: "audiobook keeps the show name",
			track: playlist.Track{
				Title:  "Tortilla Face",
				Artist: "Bobby Lee & Andrew Santino",
				Album:  "Bad Friends",
				Path:   "http://abs.example/api/items/x/file/y",
				Stream: true,
			},
		},
		{
			name: "spotify keeps the album",
			track: playlist.Track{
				Title:  "Alien Boy",
				Artist: "Oliver Tree",
				Album:  "Ugly is Beautiful",
				Path:   "spotify:track:abc123",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := ipcTrackInfo(tc.track, 0, 0)
			for _, f := range []struct{ field, got, want string }{
				{"Title", info.Title, tc.track.Title},
				{"Artist", info.Artist, tc.track.Artist},
				{"Album", info.Album, tc.track.Album},
			} {
				if f.got != f.want {
					t.Errorf("%s = %q, want %q", f.field, f.got, f.want)
				}
			}
		})
	}
}
