package model

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
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

// window returns what the marquee shows for a name at a given tick, with the
// "♫ " prefix stripped.
func marqueeWindow(t *testing.T, m Model) string {
	t.Helper()
	return strings.TrimPrefix(ansi.Strip(m.renderTrackInfo()), "♫ ")
}

// TestRenderTrackInfoFitsWithoutScrolling checks that a name with room to
// spare is drawn whole: the marquee only moves when it has to.
func TestRenderTrackInfoFitsWithoutScrolling(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	p := playlist.New()
	p.Add(playlist.Track{Artist: "Bonobo", Title: "Kerala", Album: "Migration"})
	name := "Bonobo - Kerala · Migration"

	for _, tick := range []int{0, 1, 7, 40, 1000} {
		m := Model{playlist: p, titleOff: tick}
		if got := marqueeWindow(t, m); got != name {
			t.Fatalf("tick %d: renderTrackInfo() = %q, want the whole name %q", tick, got, name)
		}
	}
}

// TestRenderTrackInfoMarqueeLoops checks the three things a marquee has to get
// right: it holds at the start, it advances a cell at a time, and it comes
// back around instead of freezing after one pass.
func TestRenderTrackInfoMarqueeLoops(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 20
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	track := playlist.Track{Artist: "Long Artist", Title: "Long Title", Album: "Long Album"}
	p := playlist.New()
	p.Add(track)
	name := track.DisplayName() + " · " + track.Album
	width := ui.PanelWidth - 2
	cycle := lipgloss.Width(name + marqueeGap)

	start := marqueeWindow(t, Model{playlist: p, titleOff: 0})
	if want := ansi.Truncate(name, width, ""); start != want {
		t.Fatalf("at rest = %q, want the head of the name %q", start, want)
	}

	// Held at the start for the whole hold window, then moving.
	if got := marqueeWindow(t, Model{playlist: p, titleOff: marqueeHoldTicks - 1}); got != start {
		t.Fatalf("during hold = %q, want it still at %q", got, start)
	}
	if got := marqueeWindow(t, Model{playlist: p, titleOff: marqueeHoldTicks + 1}); got == start {
		t.Fatalf("after hold = %q, want the window to have advanced", got)
	}

	// A full cycle brings it back to the start rather than leaving it parked.
	if got := marqueeWindow(t, Model{playlist: p, titleOff: cycle + marqueeHoldTicks}); got != start {
		t.Fatalf("after a full cycle = %q, want back at %q", got, start)
	}

	// Every window is exactly one row wide, at every point in the cycle.
	for tick := range cycle + 2*marqueeHoldTicks {
		if got := lipgloss.Width(marqueeWindow(t, Model{playlist: p, titleOff: tick})); got > width {
			t.Fatalf("tick %d: window width = %d, want at most %d", tick, got, width)
		}
	}
}

// TestMarqueeMeasuresDisplayCells checks the bug that made a wide-glyph title
// overflow its row: the window has to be measured in cells, not runes.
func TestMarqueeMeasuresDisplayCells(t *testing.T) {
	const width = 20
	// Every glyph here is two cells wide, so a rune-counted window would come
	// out twice as wide as the row.
	name := strings.Repeat("音楽", 20)

	for _, tick := range []int{0, marqueeHoldTicks, marqueeHoldTicks + 3, 500} {
		got := scrollTrackName(name, width, tick)
		if w := lipgloss.Width(got); w > width {
			t.Fatalf("tick %d: window width = %d cells, want at most %d: %q", tick, w, width, got)
		}
	}
}

// TestRenderSimplifiedTrackInfoLeavesRoomForDuration checks that the
// simplified row scrolls against its own budget: it shares the row with the
// duration, so the marquee gets less width than the full view's.
func TestRenderSimplifiedTrackInfoLeavesRoomForDuration(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 30
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	p := playlist.New()
	p.Add(playlist.Track{
		Artist:       "An Artist With A Very Long Name",
		Title:        "An Equally Long Title",
		DurationSecs: 1,
	})

	for _, tick := range []int{0, marqueeHoldTicks + 2, 200} {
		m := Model{playlist: p, titleOff: tick}
		row := ansi.Strip(m.renderSimplifiedTrackInfo())
		if got := lipgloss.Width(row); got > ui.PanelWidth {
			t.Fatalf("tick %d: row width = %d, want at most %d: %q", tick, got, ui.PanelWidth, row)
		}
		if !strings.HasSuffix(row, "0:01") {
			t.Fatalf("tick %d: row %q lost its duration", tick, row)
		}
	}
}

// TestTitleScrollAdvancesWhilePlaying checks the tick gate: the marquee moves
// only while playback is running, and no faster than its interval.
func TestTitleScrollAdvancesWhilePlaying(t *testing.T) {
	now := time.Now()

	t.Run("advances on interval", func(t *testing.T) {
		m := Model{player: &playbackFakeEngine{playing: true}}
		m.advanceTitleScroll(now)
		if m.titleOff != 1 {
			t.Fatalf("titleOff = %d, want 1", m.titleOff)
		}
		m.advanceTitleScroll(now.Add(titleScrollInterval / 2))
		if m.titleOff != 1 {
			t.Fatalf("titleOff = %d, want it held inside the interval", m.titleOff)
		}
		m.advanceTitleScroll(now.Add(titleScrollInterval))
		if m.titleOff != 2 {
			t.Fatalf("titleOff = %d, want 2 after the interval", m.titleOff)
		}
	})

	t.Run("keeps going past one pass", func(t *testing.T) {
		m := Model{player: &playbackFakeEngine{playing: true}}
		at := now
		for range 500 {
			at = at.Add(titleScrollInterval)
			m.advanceTitleScroll(at)
		}
		if m.titleOff != 500 {
			t.Fatalf("titleOff = %d, want 500: the marquee must not stop after one pass", m.titleOff)
		}
	})

	t.Run("stays put when paused or stopped", func(t *testing.T) {
		for name, engine := range map[string]*playbackFakeEngine{
			"stopped": {},
			"paused":  {playing: true, paused: true},
		} {
			m := Model{player: engine}
			m.advanceTitleScroll(now)
			if m.titleOff != 0 {
				t.Fatalf("%s: titleOff = %d, want 0", name, m.titleOff)
			}
		}
	})
}

// TestResetTitleScrollReturnsToStart checks that a new track starts its
// marquee from the beginning.
func TestResetTitleScrollReturnsToStart(t *testing.T) {
	m := Model{titleOff: 42, titleLastScroll: time.Now()}
	m.resetTitleScroll()
	if m.titleOff != 0 {
		t.Fatalf("titleOff = %d, want 0", m.titleOff)
	}
	if !m.titleLastScroll.IsZero() {
		t.Fatalf("titleLastScroll = %v, want the zero time", m.titleLastScroll)
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
