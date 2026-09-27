package model

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type menuTestRecommender struct {
	commandsTestProvider
	recs []playlist.Track
}

func (p menuTestRecommender) RecommendTracks(_ context.Context, seed []playlist.Track, limit int) ([]playlist.Track, error) {
	return p.recs, nil
}

func menuItemKeys(items []trackMenuItem) []string {
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.key
	}
	return keys
}

func TestTrackMenuItemsOrderAndGating(t *testing.T) {
	base := playlist.Track{Title: "T", Artist: "Ar", Album: "Al", Path: "/t.mp3"}
	cases := []struct {
		name     string
		remove   menuRemoveKind
		provider playlist.Provider
		track    playlist.Track
		wantKeys []string
	}{
		{name: "playlist full", remove: menuRemovePlaylist, track: base, wantKeys: []string{"w", "a", "l", "t", "x", "i"}},
		{name: "queue hides add-to-queue", remove: menuRemoveQueue, track: base, wantKeys: []string{"w", "l", "t", "x", "i"}},
		{name: "no remove surface", remove: menuRemoveNone, track: base, wantKeys: []string{"w", "a", "l", "t", "i"}},
		{name: "radio when recommender exists", remove: menuRemoveNone, provider: menuTestRecommender{commandsTestProvider: commandsTestProvider{name: "Rec"}}, track: base, wantKeys: []string{"w", "r", "a", "l", "t", "i"}},
		{name: "no album or artist fields", remove: menuRemoveNone, track: playlist.Track{Title: "T", Path: "/t.mp3"}, wantKeys: []string{"w", "a", "i"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{playlist: playlist.New(), player: &playbackFakeEngine{}, mouse: &mouseState{}}
			if tc.provider != nil {
				m.providers = []ProviderEntry{{Provider: tc.provider}}
			}
			m.openTrackMenu(tc.track, tc.remove, 0)
			got := menuItemKeys(m.trackMenuItems())
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("keys = %v, want %v", got, tc.wantKeys)
			}
			for i := range got {
				if got[i] != tc.wantKeys[i] {
					t.Fatalf("keys = %v, want %v", got, tc.wantKeys)
				}
			}
		})
	}
}

func TestTrackMenuKeysNavigateAndClose(t *testing.T) {
	m := Model{playlist: playlist.New(), player: &playbackFakeEngine{}, mouse: &mouseState{}}
	m.openTrackMenu(playlist.Track{Title: "T", Artist: "A", Album: "L", Path: "/t.mp3"}, menuRemovePlaylist, 0)
	items := m.trackMenuItems()

	m.handleTrackMenuKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.trackMenu.cursor != len(items)-1 {
		t.Fatalf("cursor wrap = %d, want %d", m.trackMenu.cursor, len(items)-1)
	}
	m.handleTrackMenuKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.trackMenu.cursor != 0 {
		t.Fatalf("cursor wrap = %d, want 0", m.trackMenu.cursor)
	}
	m.handleTrackMenuKey(tea.KeyPressMsg{Text: "q"})
	if m.trackMenu.visible {
		t.Fatal("menu stayed open after q")
	}
}

func TestTrackMenuAcceleratorRunsItem(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "One", Path: "/one.mp3"})
	m := Model{playlist: p, player: &playbackFakeEngine{playing: true}, mouse: &mouseState{}}
	m.openTrackMenu(p.Tracks()[0], menuRemovePlaylist, 0)

	m.handleTrackMenuKey(tea.KeyPressMsg{Text: "a"})
	if m.trackMenu.visible {
		t.Fatal("menu stayed open after accelerator")
	}
	if p.QueueLen() != 1 {
		t.Fatalf("QueueLen = %d, want 1", p.QueueLen())
	}
}

func TestTrackHotkeysGateAndOpen(t *testing.T) {
	cases := []struct {
		name   string
		model  func() Model
		wantOk bool
	}{
		{
			name: "main playlist highlighted",
			model: func() Model {
				p := playlist.New()
				p.Add(playlist.Track{Title: "A", Path: "/a.mp3"})
				return Model{playlist: p, player: &playbackFakeEngine{}, focus: focusPlaylist, plVisible: 5, mouse: &mouseState{}}
			},
			wantOk: true,
		},
		{
			name: "provider focus has no track",
			model: func() Model {
				p := playlist.New()
				p.Add(playlist.Track{Title: "A", Path: "/a.mp3"})
				return Model{playlist: p, player: &playbackFakeEngine{}, focus: focusProvider, plVisible: 5, mouse: &mouseState{}}
			},
			wantOk: false,
		},
		{
			name: "playlist filter owns the keyboard",
			model: func() Model {
				p := playlist.New()
				p.Add(playlist.Track{Title: "A", Path: "/a.mp3"})
				return Model{playlist: p, player: &playbackFakeEngine{}, focus: focusPlaylist, plVisible: 5, mouse: &mouseState{}, search: searchState{active: true}}
			},
			wantOk: false,
		},
		{
			name: "empty playlist",
			model: func() Model {
				return Model{playlist: playlist.New(), player: &playbackFakeEngine{}, focus: focusPlaylist, plVisible: 5, mouse: &mouseState{}}
			},
			wantOk: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model()
			if _, handled := m.handleTrackHotkeys(tea.KeyPressMsg{Text: ";"}); handled != tc.wantOk {
				t.Fatalf("handled = %v, want %v", handled, tc.wantOk)
			}
		})
	}
}

func TestTrackMenuHotkeyOpensOnHighlightedTrack(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "One", Path: "/one.mp3"}, playlist.Track{Title: "Two", Path: "/two.mp3"})
	m := Model{playlist: p, player: &playbackFakeEngine{}, focus: focusPlaylist, plCursor: 1, plVisible: 5, mouse: &mouseState{}}

	if _, handled := m.handleTrackHotkeys(tea.KeyPressMsg{Text: ";"}); !handled {
		t.Fatal("semicolon not handled")
	}
	if !m.trackMenu.visible || m.trackMenu.track.Path != "/two.mp3" {
		t.Fatalf("menu = %+v, want open on /two.mp3", m.trackMenu)
	}
	if m.trackMenu.remove != menuRemovePlaylist || m.trackMenu.removeIdx != 1 {
		t.Fatalf("remove = %v idx %d, want playlist 1", m.trackMenu.remove, m.trackMenu.removeIdx)
	}
}

func TestMenuQueueTrackTogglesPlaylistIndex(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "One", Path: "/one.mp3"}, playlist.Track{Title: "Two", Path: "/two.mp3"})
	m := Model{playlist: p, player: &playbackFakeEngine{playing: true}, mouse: &mouseState{}}
	m.openTrackMenu(p.Tracks()[1], menuRemovePlaylist, 1)

	m.menuQueueTrack()
	if p.QueueLen() != 1 || p.QueuePosition(1) != 1 {
		t.Fatalf("queue = %d pos %d, want track 1 queued", p.QueueLen(), p.QueuePosition(1))
	}
	m.menuQueueTrack()
	if p.QueueLen() != 0 {
		t.Fatalf("queue = %d, want toggled off", p.QueueLen())
	}
}

func TestMenuQueueTrackAppendsForNonPlaylistTracks(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "One", Path: "/one.mp3"})
	m := Model{playlist: p, player: &playbackFakeEngine{playing: true}, mouse: &mouseState{}}
	m.openTrackMenu(playlist.Track{Title: "Net", Path: "net:t"}, menuRemoveNone, 0)

	m.menuQueueTrack()
	if p.Len() != 2 {
		t.Fatalf("playlist len = %d, want track appended", p.Len())
	}
	if p.QueueLen() != 1 {
		t.Fatalf("queue = %d, want 1", p.QueueLen())
	}
}

func TestMenuRemoveTrackFromPlaylist(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "One", Path: "/one.mp3"}, playlist.Track{Title: "Two", Path: "/two.mp3"})
	m := Model{playlist: p, player: &playbackFakeEngine{}, focus: focusPlaylist, mouse: &mouseState{}}
	m.openTrackMenu(p.Tracks()[0], menuRemovePlaylist, 0)

	m.menuRemoveTrack()
	if p.Len() != 1 || p.Tracks()[0].Title != "Two" {
		t.Fatalf("playlist = %v, want only Two", p.Tracks())
	}
}

func TestMenuRemoveTrackFromQueue(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "One", Path: "/one.mp3"}, playlist.Track{Title: "Two", Path: "/two.mp3"})
	p.Queue(0)
	p.Queue(1)
	m := Model{playlist: p, player: &playbackFakeEngine{playing: true}, mouse: &mouseState{}}
	m.openTrackMenu(p.QueueTracks()[1], menuRemoveQueue, 1)

	m.menuRemoveTrack()
	if p.QueueLen() != 1 || p.QueuePosition(0) != 1 {
		t.Fatalf("queue = %d, want only position 0 left", p.QueueLen())
	}
}

func TestCreditsFieldsMetaSuffixes(t *testing.T) {
	cases := []struct {
		name      string
		meta      map[string]string
		wantLabel string
	}{
		{"namespaced composer", map[string]string{"qobuz.composer": "A Composer"}, "Composer"},
		{"bare isrc", map[string]string{"isrc": "US123"}, "ISRC"},
		{"label", map[string]string{"provider.label": "Label Co"}, "Label"},
		{"non-credit key skipped", map[string]string{"spotify.id": "abc"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := playlist.Track{Title: "T", Path: "/t", ProviderMeta: tc.meta}
			m := Model{playlist: playlist.New(), player: &playbackFakeEngine{}, mouse: &mouseState{}}
			fields := m.creditsFields(tr)
			got := ""
			for _, f := range fields {
				if f.label == tc.wantLabel {
					got = f.value
				}
			}
			if tc.wantLabel == "" {
				if got != "" {
					t.Fatalf("unexpected credit field %q", got)
				}
				// No credit keys: the overlay must still say so, never error.
				if fields[0].label != "Credits" {
					t.Fatalf("first field = %q, want the none-exposed notice", fields[0].label)
				}
				return
			}
			if got == "" {
				t.Fatalf("no field labelled %q in %+v", tc.wantLabel, fields)
			}
		})
	}
}

func TestCreditsOverlayScrollBounds(t *testing.T) {
	m := Model{playlist: playlist.New(), player: &playbackFakeEngine{}, plVisible: 2, mouse: &mouseState{}}
	m.openCredits(playlist.Track{Title: "T", Path: "/t", ProviderMeta: map[string]string{
		"p.composer": "C1", "p.producer": "P1", "p.label": "L1", "p.isrc": "I1",
	}})
	for range 10 {
		m.handleCreditsKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if got, want := m.credits.scroll, len(m.credits.fields)-2; got != want {
		t.Fatalf("scroll = %d, want clamped to %d", got, want)
	}
	for range 20 {
		m.handleCreditsKey(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	if m.credits.scroll != 0 {
		t.Fatalf("scroll = %d, want 0", m.credits.scroll)
	}
	m.handleCreditsKey(tea.KeyPressMsg{Text: "i"})
	if m.credits.visible {
		t.Fatal("credits stayed open after i")
	}
}

func TestTrackRadioMsgPlaysSeedFirst(t *testing.T) {
	seed := playlist.Track{Title: "Seed", Path: "net:seed"}
	recs := []playlist.Track{
		{Title: "R1", Path: "net:r1"},
		{Title: "Seed dup", Path: "net:seed"}, // provider echoed the seed; must not duplicate
		{Title: "", Path: ""},                 // empty rec dropped
		{Title: "R2", Path: "net:r2"},
	}
	p := playlist.New()
	m := Model{playlist: p, player: &playbackFakeEngine{}, mouse: &mouseState{}}
	m.trackMenu.track = seed
	gen := nextRequest(&m.requests.trackMenu)

	cmd := m.handleTrackRadio(trackRadioMsg{seed: seed, tracks: recs, gen: gen})
	if cmd == nil {
		t.Fatal("expected a play command")
	}
	got := p.Tracks()
	if len(got) != 3 || got[0].Path != "net:seed" || got[1].Path != "net:r1" || got[2].Path != "net:r2" {
		t.Fatalf("radio playlist = %v", got)
	}
}

func TestTrackRadioMsgStaleGenIgnored(t *testing.T) {
	p := playlist.New()
	p.Add(playlist.Track{Title: "Keep", Path: "k"})
	m := Model{playlist: p, player: &playbackFakeEngine{}, mouse: &mouseState{}}
	nextRequest(&m.requests.trackMenu) // a newer request already bumped the gen
	m.handleTrackRadio(trackRadioMsg{tracks: []playlist.Track{{Title: "X", Path: "x"}}, gen: 0})
	if p.Len() != 1 || p.Tracks()[0].Title != "Keep" {
		t.Fatal("stale radio batch replaced the playlist")
	}
}

func TestRecommenderForTrackFallsBackToRegistry(t *testing.T) {
	rec := menuTestRecommender{commandsTestProvider: commandsTestProvider{name: "Rec"}}
	m := Model{
		playlist:  playlist.New(),
		player:    &playbackFakeEngine{},
		providers: []ProviderEntry{{Provider: commandsTestProvider{name: "Plain"}}, {Provider: rec}},
		mouse:     &mouseState{},
	}
	got, name := m.recommenderForTrack(playlist.Track{Title: "T", Path: "x"})
	if got == nil || name != "Rec" {
		t.Fatalf("recommender = %v %q, want the registered Recommender", got, name)
	}
}

func TestTrackMenuSearchArtistAndAlbumCmds(t *testing.T) {
	s := menuTestMultiSearcher{commandsTestProvider: commandsTestProvider{name: "P"}}

	msg := searchArtistCmd(s, "P", "The Band", 1)().(menuArtistMsg)
	if msg.err != nil || msg.artist.Name != "The Band" {
		t.Fatalf("artist msg = %+v", msg)
	}
	msg = searchArtistCmd(s, "P", "unknown", 1)().(menuArtistMsg)
	if msg.artist.Name != "Alpha" {
		t.Fatalf("fallback artist = %q, want first result", msg.artist.Name)
	}
	amsg := searchAlbumCmd(s, "P", "Their Album", "The Band", 1)().(menuAlbumMsg)
	if amsg.err != nil || amsg.album.Name != "Their Album" {
		t.Fatalf("album msg = %+v", amsg)
	}
}

type menuTestMultiSearcher struct {
	commandsTestProvider
}

func (p menuTestMultiSearcher) SearchAll(_ context.Context, query string, _ int) (provider.SearchResults, error) {
	if query == "empty" {
		return provider.SearchResults{}, nil
	}
	return provider.SearchResults{
		Artists: []provider.ArtistInfo{{ID: "a1", Name: "Alpha"}, {ID: "a2", Name: "The Band"}},
		Albums:  []provider.AlbumInfo{{ID: "l1", Name: "Their Album", Artist: "The Band"}},
	}, nil
}
