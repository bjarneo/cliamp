package model

import (
	"strings"
	"testing"

	"cliamp/playlist"
)

func TestFormatTrackTime(t *testing.T) {
	tests := []struct {
		secs int
		want string
	}{
		{0, ""},
		{-5, ""},
		{1, "0:01"},
		{59, "0:59"},
		{60, "1:00"},
		{222, "3:42"},
		{3599, "59:59"},
		{3600, "1:00:00"},
		{3661, "1:01:01"},
		{36000, "10:00:00"},
	}
	for _, tt := range tests {
		if got := formatTrackTime(tt.secs); got != tt.want {
			t.Errorf("formatTrackTime(%d) = %q, want %q", tt.secs, got, tt.want)
		}
	}
}

func TestFormatPlaylistDuration(t *testing.T) {
	tests := []struct {
		secs int
		want string
	}{
		{0, ""},
		{-1, ""},
		{45, "45s"},
		{59, "59s"},
		{60, "1m"},
		{600, "10m"},
		{3540, "59m"},
		{3600, "1h"},
		{3660, "1h 1m"},
		{7200, "2h"},
		{7320, "2h 2m"},
	}
	for _, tt := range tests {
		if got := formatPlaylistDuration(tt.secs); got != tt.want {
			t.Errorf("formatPlaylistDuration(%d) = %q, want %q", tt.secs, got, tt.want)
		}
	}
}

func TestPlaylistLabel(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		info   playlist.PlaylistInfo
		want   string
	}{
		{
			"name only when both unknown",
			"  ",
			playlist.PlaylistInfo{Name: "Mix"},
			"  Mix",
		},
		{
			"track count only",
			"> ",
			playlist.PlaylistInfo{Name: "Mix", TrackCount: 12},
			"> Mix · 12 tracks",
		},
		{
			"duration only",
			"  ",
			playlist.PlaylistInfo{Name: "Mix", DurationSecs: 3660},
			"  Mix · 1h 1m",
		},
		{
			"both",
			"  ",
			playlist.PlaylistInfo{Name: "Mix", TrackCount: 12, DurationSecs: 2700},
			"  Mix · 12 tracks · 45m",
		},
	}
	for _, tt := range tests {
		got := playlistLabel(tt.prefix, tt.info)
		if got != tt.want {
			t.Errorf("%s: playlistLabel = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestFormatTrackRow(t *testing.T) {
	// No duration: returns just "N. title".
	row := formatTrackRow(3, "Song", 0)
	if row != "3. Song" {
		t.Errorf("no-duration row = %q, want %q", row, "3. Song")
	}

	// With duration: ends with the time string.
	row = formatTrackRow(3, "Song", 222)
	if !strings.HasSuffix(row, "3:42") {
		t.Errorf("with-duration row %q does not end with %q", row, "3:42")
	}
	if !strings.HasPrefix(row, "3. Song") {
		t.Errorf("with-duration row %q does not start with %q", row, "3. Song")
	}
}

func TestHeaderStateIncremental(t *testing.T) {
	mk := func(album string) playlist.Track { return playlist.Track{Album: album} }

	tests := []struct {
		name        string
		batches     [][]playlist.Track
		wantHeaders bool
		wantTracks  int
		wantSegs    int
	}{
		{
			name:        "empty",
			batches:     nil,
			wantHeaders: false,
		},
		{
			name: "single track is below cohesion threshold",
			batches: [][]playlist.Track{
				{mk("Aja")},
			},
			wantHeaders: false,
			wantTracks:  1,
			wantSegs:    1,
		},
		{
			name: "full album in one shot is cohesive",
			batches: [][]playlist.Track{
				{mk("Aja"), mk("Aja"), mk("Aja"), mk("Aja")},
			},
			wantHeaders: true,
			wantTracks:  4,
			wantSegs:    1,
		},
		{
			name: "full album split across batches stays cohesive",
			batches: [][]playlist.Track{
				{mk("Aja"), mk("Aja")},
				{mk("Aja"), mk("Aja")},
			},
			wantHeaders: true,
			wantTracks:  4,
			wantSegs:    1,
		},
		{
			name: "mixtape across batches is not cohesive",
			batches: [][]playlist.Track{
				{mk("A"), mk("B")},
				{mk("C"), mk("D")},
			},
			wantHeaders: false,
			wantTracks:  4,
			wantSegs:    4,
		},
		{
			name: "two albums of 3 tracks each meet threshold",
			batches: [][]playlist.Track{
				{mk("X"), mk("X"), mk("X")},
				{mk("Y"), mk("Y"), mk("Y")},
			},
			wantHeaders: true,
			wantTracks:  6,
			wantSegs:    2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{}
			m.setHeaderStateFromTracks(nil) // reset counters
			for _, batch := range tt.batches {
				m.addToHeaderState(batch)
			}
			if m.showAlbumHeaders != tt.wantHeaders {
				t.Errorf("showAlbumHeaders = %v, want %v", m.showAlbumHeaders, tt.wantHeaders)
			}
			if m.headerTracks != tt.wantTracks {
				t.Errorf("headerTracks = %d, want %d", m.headerTracks, tt.wantTracks)
			}
			if m.headerSegments != tt.wantSegs {
				t.Errorf("headerSegments = %d, want %d", m.headerSegments, tt.wantSegs)
			}
		})
	}
}

func TestHeaderStateManualOverride(t *testing.T) {
	mk := func(album string) playlist.Track { return playlist.Track{Album: album} }

	m := &Model{}
	// Start with a cohesive album so the heuristic would prefer headers.
	m.setHeaderStateFromTracks([]playlist.Track{mk("A"), mk("A"), mk("A"), mk("A")})
	if !m.showAlbumHeaders {
		t.Fatalf("baseline cohesive album should default to showing headers")
	}

	// User manually toggles off.
	m.toggleAlbumHeadersManual()
	if m.showAlbumHeaders {
		t.Fatalf("after manual toggle showAlbumHeaders should be false")
	}

	// Adding more cohesive tracks must NOT flip back on.
	m.addToHeaderState([]playlist.Track{mk("A"), mk("A"), mk("A")})
	if m.showAlbumHeaders {
		t.Fatalf("manual override should suppress heuristic after Add")
	}

	// A fresh load via setHeaderStateFromTracks clears the manual flag.
	m.setHeaderStateFromTracks([]playlist.Track{mk("B"), mk("B"), mk("B"), mk("B")})
	if !m.showAlbumHeaders {
		t.Fatalf("setHeaderStateFromTracks should clear manual flag and re-run heuristic")
	}
}

func TestProviderKeyForShortcut(t *testing.T) {
	tests := map[string]string{
		"S": "spotify",
		"N": "navidrome",
		"P": "plex",
		"J": "jellyfin",
		"Y": "yt",
		"L": "local",
		"R": "radio",
		"x": "",
		"":  "",
	}
	for in, want := range tests {
		if got := providerKeyForShortcut(in); got != want {
			t.Errorf("providerKeyForShortcut(%q) = %q, want %q", in, got, want)
		}
	}
}
