package main

import (
	"testing"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

// TestDaemonStreamTitleFields pins the daemon's IPC contract against the TUI's:
// same split, same fallbacks, same Station derivation. statusResponse itself
// needs a live *player.Player, so it calls the extracted helper directly.
func TestDaemonStreamTitleFields(t *testing.T) {
	tests := []struct {
		name        string
		streamTitle string
		track       playlist.Track
		wantTitle   string
		wantArtist  string
		wantStation string
		wantStream  string
	}{
		{
			name:        "artist and title split on separator",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Title: "NCS Trap Stream", Stream: true},
			wantTitle:   "Awake",
			wantArtist:  "Tycho",
			wantStation: "NCS Trap Stream",
			wantStream:  "Tycho - Awake",
		},
		{
			name:        "empty title after the separator keeps the station",
			streamTitle: "Tycho - ",
			track:       playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle:   "Lofi Stream",
			wantStream:  "Tycho - ",
		},
		{
			name:        "title-only metadata becomes the title",
			streamTitle: "Morning Session",
			track:       playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle:   "Morning Session",
			wantStation: "Lofi Stream",
			wantStream:  "Morning Session",
		},
		{
			name:      "no metadata leaves the entry untouched",
			track:     playlist.Track{Title: "Lofi Stream", Stream: true},
			wantTitle: "Lofi Stream",
		},
		{
			name:        "non-stream track is never rewritten",
			streamTitle: "Tycho - Awake",
			track:       playlist.Track{Title: "Alien Boy", Artist: "Oliver Tree"},
			wantTitle:   "Alien Boy",
			wantArtist:  "Oliver Tree",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := ipc.TrackInfo{Title: tc.track.Title, Artist: tc.track.Artist}
			applyStreamTitle(&info, tc.track, tc.streamTitle)

			for _, f := range []struct{ field, got, want string }{
				{"Title", info.Title, tc.wantTitle},
				{"Artist", info.Artist, tc.wantArtist},
				{"Station", info.Station, tc.wantStation},
				{"StreamTitle", info.StreamTitle, tc.wantStream},
			} {
				if f.got != f.want {
					t.Errorf("%s = %q, want %q", f.field, f.got, f.want)
				}
			}
		})
	}
}
