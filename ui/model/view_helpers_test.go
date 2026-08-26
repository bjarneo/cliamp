package model

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

func TestRestrictedMarkersAreViewOnly(t *testing.T) {
	track := playlist.Track{
		Title:        "Members Only",
		Artist:       "Creator",
		ProviderMeta: map[string]string{provider.MetaMixcloudExclusive: "true"},
	}
	if got := trackViewName(track); got != "Creator - Members Only [E]" {
		t.Fatalf("trackViewName = %q", got)
	}
	if track.Title != "Members Only" {
		t.Fatalf("track title mutated to %q", track.Title)
	}

	album := provider.AlbumInfo{Name: "Members Only", Restricted: true}
	if got := albumViewName(album); got != "Members Only [E]" {
		t.Fatalf("albumViewName = %q", got)
	}
	if album.Name != "Members Only" {
		t.Fatalf("album name mutated to %q", album.Name)
	}

	plain := playlist.Track{Title: "Open Show", Artist: "Creator"}
	if got := trackViewName(plain); got != "Creator - Open Show" {
		t.Fatalf("unrestricted trackViewName = %q", got)
	}
	notExclusive := playlist.Track{
		Title:        "Open Show",
		Artist:       "Creator",
		ProviderMeta: map[string]string{provider.MetaMixcloudExclusive: "false"},
	}
	if got := trackViewName(notExclusive); got != "Creator - Open Show" {
		t.Fatalf("non-exclusive trackViewName = %q", got)
	}
	if got := albumViewName(provider.AlbumInfo{Name: "Open Show"}); got != "Open Show" {
		t.Fatalf("unrestricted albumViewName = %q", got)
	}
}

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

func TestPlaylistLabel(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		info   playlist.PlaylistInfo
		want   string
	}{
		{
			"name only when no tracks",
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
			"duration shown for static playlists",
			"  ",
			playlist.PlaylistInfo{Name: "Mix", DurationSecs: 3660},
			"  Mix · 1h 1m",
		},
		{
			"tracks and duration both shown",
			"  ",
			playlist.PlaylistInfo{Name: "Mix", TrackCount: 12, DurationSecs: 2700},
			"  Mix · 12 tracks · 45m",
		},
		{
			"duration hidden for dir-backed playlists",
			"  ",
			playlist.PlaylistInfo{Name: "Mix", TrackCount: 12, DurationSecs: 2700, DirSourceCount: 2},
			"  Mix · 12 tracks",
		},
		{
			"favorites shows zero count",
			"  ",
			playlist.PlaylistInfo{Name: favorites.PlaylistName},
			"  Favorites · 0 tracks",
		},
		{
			"favorites with tracks",
			"  ",
			playlist.PlaylistInfo{Name: favorites.PlaylistName, TrackCount: 3},
			"  Favorites · 3 tracks",
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

func TestRenderTrackInfoScrollsLongArtistAndTitleOnce(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 20
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	track := playlist.Track{Artist: "Long Artist", Title: "Long Title", Album: "Long Album"}
	p := playlist.New()
	p.Add(track)
	name := track.DisplayName() + " · " + track.Album
	nameRunes := []rune(name)
	maxW := ui.PanelWidth - 4

	tests := []struct {
		name   string
		offset int
		want   string
	}{
		{
			name:   "starts at the artist",
			offset: 0,
			want:   string(nameRunes[:maxW]),
		},
		{
			name:   "advances through the title",
			offset: 1,
			want:   string(nameRunes[1 : maxW+1]),
		},
		{
			name:   "holds at the end instead of repeating",
			offset: len(nameRunes),
			want:   string(nameRunes[len(nameRunes)-maxW:]),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{playlist: p, titleOff: tt.offset}
			got := strings.TrimPrefix(ansi.Strip(m.renderTrackInfo()), "♫ ")
			if got != tt.want {
				t.Errorf("renderTrackInfo() = %q, want %q", got, tt.want)
			}
		})
	}

	track.DurationSecs = 1
	p.SetTrack(0, track)
	m := Model{playlist: p, titleOff: len(nameRunes)}
	got := ansi.Strip(m.renderSimplifiedTrackInfo())
	wantName := string(nameRunes[len(nameRunes)-(ui.PanelWidth-len("0:01")-1):])
	if !strings.HasPrefix(got, wantName) {
		t.Errorf("renderSimplifiedTrackInfo() = %q, want prefix %q", got, wantName)
	}

	ui.PanelWidth = 80
	track = playlist.Track{
		Artist: "An Artist With A Very Long Name",
		Title:  "An Equally Long Title",
		Album:  "An Album Too Long To Fit",
	}
	p.Replace([]playlist.Track{track})
	name = track.DisplayName() + " · " + track.Album
	nameRunes = []rune(name)
	m = Model{playlist: p, titleOff: 1}
	got = strings.TrimPrefix(ansi.Strip(m.renderTrackInfo()), "♫ ")
	if got != string(nameRunes[1:trackInfoMarqueeWidth+1]) {
		t.Errorf("wide renderTrackInfo() = %q, want marquee offset", got)
	}
}

func TestTitleScrollResetsAfterOnePass(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	p := playlist.New()
	p.Add(playlist.Track{
		Artist: "An Artist With A Very Long Name",
		Title:  "An Equally Long Title",
		Album:  "An Album Too Long To Fit",
	})
	m := Model{player: &playbackFakeEngine{playing: true}, playlist: p}
	m.titleOff = m.titleScrollLimit()
	now := time.Now()
	m.advanceTitleScroll(now)

	if m.titleOff != 0 {
		t.Errorf("titleOff = %d, want 0 after a full marquee pass", m.titleOff)
	}
	if !m.titleScrolled {
		t.Fatal("titleScrolled = false, want true after a full marquee pass")
	}
	m.advanceTitleScroll(now.Add(time.Second))
	if m.titleOff != 0 {
		t.Errorf("titleOff = %d after completion, want marquee to stay at the start", m.titleOff)
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
		"X": "mixcloud",
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
