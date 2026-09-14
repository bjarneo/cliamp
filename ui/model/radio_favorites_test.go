package model

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/playlist"
)

func radioFavoriteTestModel(t *testing.T) (Model, *radio.Provider, []playlist.Track) {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	favorites := radio.LoadFavorites()
	p := radio.New(radio.Options{Favorites: favorites, Country: radio.CountryDeclined})
	p.AppendCatalog([]radio.CatalogStation{
		{Name: "Playing FM", URL: "https://radio.example/playing"},
		{Name: "Selected FM", URL: "https://radio.example/selected", Country: "Norway", Bitrate: 128},
	})
	var tracks []playlist.Track
	for _, id := range []string{"c:0", "c:1"} {
		got, err := p.Tracks(id)
		if err != nil {
			t.Fatal(err)
		}
		tracks = append(tracks, got...)
	}
	withFrameWidth(t, 180)
	m := keybindingTestModel()
	m.width, m.height = 180, 40
	m.SetRadioFavorites(favorites)
	m.provider = p
	m.providers = append(m.providers, ProviderEntry{Key: "radio", Name: "Radio", Provider: p})
	m.catalogBatch.done = true // No network work during provider-list refresh.
	return m, p, tracks
}

func TestRadioFavoriteAfterBrowseLoadsPlaylist(t *testing.T) {
	withFrameWidth(t, 140)
	for _, route := range []string{"browse:countries", "browse:tags"} {
		t.Run(route, func(t *testing.T) {
			m, p, tracks := radioFavoriteTestModel(t)
			tracks[1].Title = "Artist - Current Song"
			m.loadedPlaylist = "previous local playlist"
			for _, entry := range p.BrowseEntries() {
				if entry.ID == route {
					m.openNavBrowserEntry(p, entry)
				}
			}
			updated, _ := m.Update(navTracksLoadedMsg{tracks: tracks, gen: m.requests.nav})
			m = updated.(Model)
			if m.loadedPlaylist != "" || m.focus != focusPlaylist || m.navBrowser.visible {
				t.Fatal("browse result did not enter the unsaved playback playlist")
			}

			// Playing and selected differ; current song metadata must not become
			// the saved station name.
			m.playlist.SetIndex(0)
			m.player.(*playbackFakeEngine).playing = true
			m.plCursor = 1
			if help := m.commandHelp(commandModeMain); !strings.Contains(help, "Favorite station") {
				t.Fatalf("missing station action: %q", help)
			}
			cmd := m.handleKey(tea.KeyPressMsg{Text: "f"})
			if cmd != nil {
				t.Fatal("in-memory favorite refresh unexpectedly scheduled work")
			}
			favorites := m.radioFavorites.Stations()
			if len(favorites) != 1 || favorites[0].Name != "Selected FM" || favorites[0].URL != tracks[1].Path {
				t.Fatalf("favorited wrong station: %+v", favorites)
			}
			if !radio.LoadFavorites().Contains(tracks[1].Path) {
				t.Fatal("favorite was not persisted")
			}
			if m.status.text != "Favorited station: Selected FM" {
				t.Fatalf("status = %q", m.status.text)
			}
			found := false
			for _, list := range m.providerLists {
				if list.ID == "f:"+tracks[1].Path {
					found = strings.Contains(list.Name, "Selected FM")
				}
			}
			if !found || !m.markerColumns().bookmark || !m.playlistTrackStarred(tracks[1]) {
				t.Fatal("favorite missing from Radio pane or playback star state")
			}
			if !strings.Contains(ansi.Strip(m.renderPlaylist()), "★") {
				t.Fatal("favorite star was not rendered")
			}
			track, _ := m.playlist.Track(1)
			if track.Bookmark || len(m.favSet) != 0 {
				t.Fatal("radio favorite changed bookmarks or heart favorites")
			}

			m.handleKey(tea.KeyPressMsg{Text: "f"})
			if m.radioFavorites.Count() != 0 || m.playlistTrackStarred(track) || radio.LoadFavorites().Count() != 0 {
				t.Fatal("second f did not remove the favorite")
			}
		})
	}
}

func TestRadioFavoriteFollowsTrackAfterProviderSwitch(t *testing.T) {
	m, p, tracks := radioFavoriteTestModel(t)
	m.replacePlayerPlaylist(append(tracks, playlist.Track{Path: "/music/local.mp3", Title: "Local"}))
	m.plCursor = 1
	// Change the provider pill without replacing the mixed playback queue.
	other := commandsTestProvider{name: "Other"}
	m.provider = other
	cmd := m.handleKey(tea.KeyPressMsg{Text: "f"})
	if cmd != nil || !m.radioFavorites.Contains(tracks[1].Path) || m.provider.Name() != "Other" {
		t.Fatal("radio favorite depended on or refreshed the active provider")
	}
	// The Catalog adapter sees exactly the same favorite and removes it.
	if added, _, err := p.ToggleFavorite("c:1"); err != nil || added {
		t.Fatalf("catalog toggle = %v, %v; want removal", added, err)
	}
	if m.playlistTrackStarred(tracks[1]) {
		t.Fatal("playback star did not follow catalog removal")
	}

	m.plCursor = 2
	if m.selectedPlaylistStarAction() != starUnavailable {
		t.Fatal("ordinary mixed-queue track was treated as a radio station")
	}
	m.handleKey(tea.KeyPressMsg{Text: "f"})
	if m.radioFavorites.Count() != 0 {
		t.Fatal("ordinary track changed radio favorites")
	}
}

type radioBookmarkTestProvider struct {
	commandsTestProvider
	savedPlaylist, savedPath string
	err                      error
}

func (p *radioBookmarkTestProvider) SetBookmark(string, int) error { return p.err }
func (p *radioBookmarkTestProvider) SetBookmarkByPath(name, path string) error {
	p.savedPlaylist, p.savedPath = name, path
	return p.err
}

func TestRadioTrackInLocalPlaylistStillBookmarks(t *testing.T) {
	withFrameWidth(t, 140)
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			m, _, tracks := radioFavoriteTestModel(t)
			m.replacePlayerPlaylist(tracks)
			m.loadedPlaylist = "Saved radios"
			local := &radioBookmarkTestProvider{}
			if fail {
				local.err = errors.New("read-only playlist")
			}
			m.localProvider = local
			if help := m.commandHelp(commandModeMain); !strings.Contains(help, "Bookmark track") || strings.Contains(help, "Favorite station") {
				t.Fatalf("wrong local-playlist action: %q", help)
			}
			m.handleKey(tea.KeyPressMsg{Text: "f"})
			track, _ := m.playlist.Track(0)
			if local.savedPlaylist != "Saved radios" || local.savedPath != track.Path || track.Bookmark == fail {
				t.Fatal("local bookmark behavior changed")
			}
			if m.radioFavorites.Count() != 0 {
				t.Fatal("bookmark wrote radio favorites")
			}
			if !fail {
				m.handleKey(tea.KeyPressMsg{Text: "f"})
				track, _ = m.playlist.Track(0)
				if track.Bookmark {
					t.Fatal("second f did not remove bookmark")
				}
			}
		})
	}
}

func TestRadioFavoriteWriteFailure(t *testing.T) {
	m, _, tracks := radioFavoriteTestModel(t)
	m.replacePlayerPlaylist(tracks)
	before := m.markerColumns()
	cache := *m.radioMarkers
	// Prevent the atomic writer from replacing the target with a regular file.
	if err := os.Mkdir(filepath.Join(os.Getenv("CLIAMP_CONFIG_DIR"), "radio_favorites.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if cmd := m.handleKey(tea.KeyPressMsg{Text: "f"}); cmd != nil {
		t.Fatal("failed write requested a list refresh")
	}
	if m.radioFavorites.Count() != 0 || m.playlistTrackStarred(tracks[0]) {
		t.Fatal("failed write published favorite state")
	}
	if m.markerColumns() != before || *m.radioMarkers != cache {
		t.Fatal("failed write invalidated or changed marker state")
	}
	if m.status.kind != feedbackError || !strings.Contains(m.status.text, "Favorite save failed") {
		t.Fatalf("missing error feedback: %q", m.status.text)
	}
}

func TestRadioHeartFavoritesRemainSeparate(t *testing.T) {
	m, _, tracks := radioFavoriteTestModel(t)
	m.replacePlayerPlaylist(tracks)
	hearts := &dirSourceTestProvider{}
	m.favMgr = hearts
	m.localProvider = hearts
	m.handleKey(tea.KeyPressMsg{Text: "n"})
	if !hearts.IsFavorited(tracks[0].Path) || m.radioFavorites.Count() != 0 {
		t.Fatal("heart favorite changed radio favorites")
	}
	m.handleKey(tea.KeyPressMsg{Text: "f"})
	m.handleKey(tea.KeyPressMsg{Text: "f"})
	if !hearts.IsFavorited(tracks[0].Path) {
		t.Fatal("radio favorite changed heart favorites")
	}
}

func TestRadioWrapperFavoritesKeepStationIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		tracks           int
	}{
		{"M3U", "/station.m3u", "#EXTM3U\n#EXTINF:-1,Endpoint A\nhttps://cdn.example/a\n#EXTINF:-1,Endpoint B\nhttps://cdn.example/b\n", 2},
		{"PLS", "/station.pls", "[playlist]\nNumberOfEntries=1\nFile1=https://cdn.example/a\nTitle1=Endpoint A\nLength1=-1\nVersion=2\n", 1},
		{"HLS", "/station.m3u8", "#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:10,\nsegment.ts\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			m, p, _ := radioFavoriteTestModel(t)
			station := radio.CatalogStation{
				Name: "Wrapper FM", URL: srv.URL + tc.path, Country: "Norway", State: "Oslo",
				Tags: "jazz", Bitrate: 128, Codec: "MP3", Homepage: "https://wrapper.example",
			}
			p.AppendCatalog([]radio.CatalogStation{station})
			m.requests.tracks = 1
			msg := fetchTracksCmd(p, "c:2", 1)().(tracksLoadedMsg)
			if msg.err != nil || len(msg.tracks) != tc.tracks {
				t.Fatalf("resolved tracks = %+v, error = %v", msg.tracks, msg.err)
			}
			for _, track := range msg.tracks {
				assertRadioLyricsScrollable(t, track)
				if got, ok := radio.StationFromTrack(track); !ok || got != station {
					t.Fatalf("resolved identity = %+v, %v; want %+v", got, ok, station)
				}
				if tc.name != "HLS" && track.Path == station.URL {
					t.Fatal("wrapper was not resolved to an audio URL")
				}
			}
			if len(msg.tracks) > 1 {
				msg.tracks[0].ProviderMeta["test"] = "first endpoint"
				if msg.tracks[1].Meta("test") != "" {
					t.Fatal("resolved endpoints share mutable metadata")
				}
			}
			updated, _ := m.Update(msg)
			m = updated.(Model)
			if m.selectedPlaylistStarAction() != starRadioFavorite {
				t.Fatal("resolved station lost the playback favorite action")
			}
			m.handleKey(tea.KeyPressMsg{Text: "f"})
			saved := radio.LoadFavorites().Stations()
			if len(saved) != 1 || saved[0] != station {
				t.Fatalf("saved %+v; want original wrapper station %+v", saved, station)
			}
			for _, track := range msg.tracks {
				if !m.playlistTrackStarred(track) {
					t.Fatal("resolved endpoint did not show the station favorite")
				}
			}

			// Loading the saved wrapper resolves again and still supports removal.
			reloaded := fetchTracksCmd(p, "f:"+station.URL, 1)().(tracksLoadedMsg)
			updated, _ = m.Update(reloaded)
			m = updated.(Model)
			m.handleKey(tea.KeyPressMsg{Text: "f"})
			if m.radioFavorites.Count() != 0 || radio.LoadFavorites().Count() != 0 {
				t.Fatal("reloaded wrapper could not be unfavorited")
			}
		})
	}
}

func TestRadioFavoriteRefreshKeepsCatalogSelection(t *testing.T) {
	m, p, _ := radioFavoriteTestModel(t)
	lists, err := p.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	m.providerLists = providerListsWithBrowse(p, lists)
	for i, list := range m.providerLists {
		if list.ID == "c:1" {
			m.provCursor = i
		}
	}
	cmd := m.openProviderList(m.provCursor)
	updated, _ := m.Update(cmd())
	m = updated.(Model)

	for _, wantFavorite := range []bool{true, false} {
		oldCursor := m.provCursor
		m.handleKey(tea.KeyPressMsg{Text: "f"})
		if selected := m.providerLists[m.provCursor]; selected.ID != "c:1" {
			t.Fatalf("refresh moved selection to %+v, want c:1", selected)
		}
		delta := -1
		if wantFavorite {
			delta = 1
		}
		if m.provCursor != oldCursor+delta {
			t.Fatalf("cursor = %d, want %d after favorite row change", m.provCursor, oldCursor+delta)
		}
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.focus != focusProvider || m.providerLists[m.provCursor].ID != "c:1" {
			t.Fatal("returning to provider lost the highlighted station")
		}
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	}
}

func TestProviderListRefreshPreservesLatestSelection(t *testing.T) {
	m, p, tracks := radioFavoriteTestModel(t)
	m.replacePlayerPlaylist(tracks)
	lists, _ := p.Playlists()
	m.replaceProviderLists(lists)
	m.handleKey(tea.KeyPressMsg{Text: "f"})
	cmd := fetchPlaylistsCmd(p, m.requests.provider)
	// The user can move the provider cursor while an initialization notification is pending.
	for i, list := range m.providerLists {
		if list.ID == "c:1" {
			m.provCursor = i
		}
	}
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if m.providerLists[m.provCursor].ID != "c:1" {
		t.Fatal("refresh overwrote navigation performed while it was pending")
	}
}

func TestRadioFavoriteRefreshKeepsSurvivingFavoriteSelection(t *testing.T) {
	for _, count := range []int{2, 3} {
		t.Run(fmt.Sprintf("%d favorites", count), func(t *testing.T) {
			m, p, tracks := radioFavoriteTestModel(t)
			// Equal display names must not be mistaken for station identity.
			stations := []radio.CatalogStation{
				{Name: "Same name", URL: tracks[0].Path},
				{Name: "Same name", URL: tracks[1].Path},
				{Name: "Same name", URL: "https://radio.example/third"},
			}
			for _, station := range stations[:count] {
				if added, err := m.radioFavorites.Toggle(station); err != nil || !added {
					t.Fatalf("toggle = %v, %v", added, err)
				}
			}
			m.replacePlayerPlaylist(tracks)
			lists, err := p.Playlists()
			if err != nil {
				t.Fatal(err)
			}
			m.replaceProviderLists(lists)
			// Select the second favorite in the provider pane, but leave the first
			// station selected in playback. Esc returns without loading another queue.
			favoritesSeen := 0
			for i, list := range m.providerLists {
				if strings.HasPrefix(list.ID, "f:") {
					favoritesSeen++
					if favoritesSeen == 2 {
						m.provCursor = i
						break
					}
				}
			}
			for _, wantFavorite := range []bool{false, true} {
				m.handleKey(tea.KeyPressMsg{Text: "f"})
				m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
				selected := m.providerLists[m.provCursor]
				if !strings.HasPrefix(selected.ID, "f:") {
					t.Fatalf("highlight moved out of Favorites to %+v", selected)
				}
				got, err := p.Tracks(selected.ID)
				if err != nil || len(got) != 1 || got[0].Path != tracks[1].Path {
					t.Fatalf("highlighted tracks = %+v, %v; want %s", got, err, tracks[1].Path)
				}
				if m.radioFavorites.Contains(tracks[0].Path) != wantFavorite {
					t.Fatal("unexpected mutation of the playback-selected station")
				}
				m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			}
		})
	}
}

func TestRadioProviderToggleRejectsDelayedPlaybackRefresh(t *testing.T) {
	m, p, tracks := radioFavoriteTestModel(t)
	if _, _, err := p.ToggleFavorite("c:0"); err != nil {
		t.Fatal(err)
	}
	lists, err := p.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	m.replaceProviderLists(lists)
	m.replacePlayerPlaylist(tracks)
	m.plCursor = 1
	m.handleKey(tea.KeyPressMsg{Text: "f"})
	// A pending notification must not undo a newer provider-pane mutation.
	delayed := fetchPlaylistsCmd(p, m.requests.provider)()
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	found := false
	for i, list := range m.providerLists {
		if list.ID == "f:"+tracks[0].Path {
			m.provCursor = i
			found = true
			break
		}
	}
	if !found || m.focus != focusProvider {
		t.Fatal("could not select the existing favorite in the provider pane")
	}
	m.handleKey(tea.KeyPressMsg{Text: "f"})
	if m.radioFavorites.Contains(tracks[0].Path) {
		t.Fatal("provider toggle did not remove the favorite")
	}
	updated, _ := m.Update(delayed)
	m = updated.(Model)
	found = false
	for _, list := range m.providerLists {
		if list.ID == "f:"+tracks[0].Path {
			t.Fatal("delayed refresh restored the removed favorite row")
		}
		if list.ID == "f:"+tracks[1].Path {
			found = true
			if _, err := p.Tracks(list.ID); err != nil {
				t.Fatalf("surviving favorite cannot load: %v", err)
			}
		}
	}
	if !found {
		t.Fatal("surviving favorite disappeared")
	}
}

func TestRadioRefreshDuringCatalogLoading(t *testing.T) {
	for _, remove := range []bool{false, true} {
		for _, batchFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("remove=%t/batchFirst=%t", remove, batchFirst), func(t *testing.T) {
				m, p, tracks := radioFavoriteTestModel(t)
				if remove {
					if _, _, err := p.ToggleFavorite("c:0"); err != nil {
						t.Fatal(err)
					}
				}
				lists, err := p.Playlists()
				if err != nil {
					t.Fatal(err)
				}
				m.replaceProviderLists(lists)
				for i, list := range m.providerLists {
					if list.ID == "c:1" {
						m.provCursor = i
					}
				}
				m.replacePlayerPlaylist(tracks)
				m.catalogBatch = catalogBatchState{loading: true, offset: 2}
				m.requests.catalog = 1
				if cmd := m.handleKey(tea.KeyPressMsg{Text: "f"}); cmd != nil {
					t.Fatal("favorite refresh should be synchronous")
				}
				if m.providerLists[m.provCursor].ID != "c:1" {
					t.Fatal("favorite mutation moved the selection")
				}
				// Initialization can still have a queued notification. Capture it
				// BEFORE the catalog append: it must never contain stale rows.
				refresh := fetchPlaylistsCmd(p, m.requests.provider)()
				if _, ok := refresh.(radioListsRefreshMsg); !ok {
					t.Fatalf("Radio refresh carries a snapshot: %T", refresh)
				}
				p.AppendCatalog([]radio.CatalogStation{{Name: "New FM", URL: "https://radio.example/new"}})
				batch := catalogBatchMsg{providerName: p.Name(), gen: 1, added: 1}
				messages := []tea.Msg{refresh, batch}
				if batchFirst {
					messages = []tea.Msg{batch, refresh}
				}
				for _, msg := range messages {
					updated, _ := m.Update(msg)
					m = updated.(Model)
					if got := m.providerLists[m.provCursor].ID; got != "c:1" {
						t.Fatalf("after %T selected %s, want c:1", msg, got)
					}
					found := false
					for _, row := range m.providerLists {
						found = found || row.ID == "c:2"
					}
					if !found {
						t.Fatalf("after %T completed catalog row disappeared", msg)
					}
				}
				if m.catalogBatch.offset != 3 || !m.catalogBatch.done || m.catalogBatch.loading {
					t.Fatalf("catalog progress = %+v", m.catalogBatch)
				}
			})
		}
	}
}

func TestRadioFavoriteMarkerColumnsFollowPlaylist(t *testing.T) {
	m, p, radioTracks := radioFavoriteTestModel(t)
	local := playlist.Track{Path: "/music/local.mp3", Title: "Local"}
	bookmarked := local
	bookmarked.Bookmark = true
	wrapper := radioTracks[0]
	wrapper.Path = "https://cdn.example/resolved"
	offscreen := make([]playlist.Track, 100)
	for i := range offscreen {
		offscreen[i] = local
	}
	offscreen = append(offscreen, radioTracks[0])
	for _, tc := range []struct {
		name   string
		tracks []playlist.Track
		saved  bool
		want   bool
	}{
		{name: "local files only", tracks: []playlist.Track{local}},
		{name: "unfavorited station", tracks: radioTracks[1:]},
		{name: "favorite station", tracks: radioTracks[:1], want: true},
		{name: "mixed queue", tracks: []playlist.Track{local, radioTracks[0]}, want: true},
		{name: "offscreen favorite", tracks: offscreen, want: true},
		{name: "resolved wrapper", tracks: []playlist.Track{wrapper}, want: true},
		{name: "saved playlist ignores radio favorite", tracks: radioTracks[:1], saved: true},
		{name: "local bookmark", tracks: []playlist.Track{bookmarked}, saved: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.replacePlayerPlaylist(tc.tracks)
			if tc.saved {
				m.loadedPlaylist = "Saved"
			}
			before := m.renderPlaylist()
			wantAfterRemoval := m.playlist.BookmarkCount() > 0
			if _, _, err := p.ToggleFavorite("c:0"); err != nil {
				t.Fatal(err)
			}
			if got := m.markerColumns().bookmark; got != tc.want {
				t.Errorf("star column = %v, want %v", got, tc.want)
			}
			if !tc.want && m.renderPlaylist() != before {
				t.Error("unrelated favorite changed playlist rendering")
			}
			if tc.name == "offscreen favorite" && strings.Contains(m.renderPlaylist(), "★") {
				t.Error("test requires the favorite to be outside the visible rows")
			}
			if _, _, err := p.ToggleFavorite("c:0"); err != nil {
				t.Fatal(err)
			}
			if got := m.markerColumns().bookmark; got != wantAfterRemoval {
				t.Errorf("star column after removal = %v", got)
			}
		})
	}
}

func TestRadioMarkerCacheTracksInputs(t *testing.T) {
	m, p, tracks := radioFavoriteTestModel(t)
	m.replacePlayerPlaylist(tracks[:1])
	assertStar := func(want bool) {
		t.Helper()
		if got := m.markerColumns().bookmark; got != want {
			t.Fatalf("star column = %v, want %v", got, want)
		}
	}
	assertStar(false)
	// Provider mutations bypass playback keys but share the revisioned store.
	if _, _, err := p.ToggleFavorite("c:0"); err != nil {
		t.Fatal(err)
	}
	assertStar(true)
	m.playlist.SetTrack(0, tracks[1])
	assertStar(false)
	m.playlist.SetTrack(0, tracks[0])
	assertStar(true)
	m.loadedPlaylist = "Saved"
	assertStar(false)
	m.playlist.ToggleBookmark(0)
	assertStar(true)
	m.playlist.ToggleBookmark(0)
	assertStar(false)
	m.loadedPlaylist = ""
	assertStar(true)
	station, _ := radio.StationFromTrack(tracks[0])
	if added, err := m.radioFavorites.Toggle(station); err != nil || added {
		t.Fatalf("toggle = %v, %v", added, err)
	}
	assertStar(false)

	// Equal revisions from different playlist objects cannot reuse a result.
	a, b := playlist.New(), playlist.New()
	a.Replace(tracks[:1])
	b.Replace(tracks[1:])
	if a.Revision() != b.Revision() {
		t.Fatal("test needs equal revisions")
	}
	if added, err := m.radioFavorites.Toggle(station); err != nil || !added {
		t.Fatalf("toggle = %v, %v", added, err)
	}
	m.playlist = a
	assertStar(true)
	m.playlist = b
	assertStar(false)

	// Replacing the store also invalidates derived state.
	m.playlist = a
	assertStar(true)
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	m.SetRadioFavorites(radio.LoadFavorites())
	assertStar(false)
}

func TestRadioMarkerCacheDoesNotCopyTracksOnRepaint(t *testing.T) {
	m, p, tracks := radioFavoriteTestModel(t)
	if _, _, err := p.ToggleFavorite("c:0"); err != nil {
		t.Fatal(err)
	}
	providerTracks := make([]playlist.Track, 10000)
	for i := range providerTracks {
		providerTracks[i] = playlist.Track{
			Path: "https://nav.example/track", Stream: true,
			ProviderMeta: map[string]string{"navidrome.id": "track"},
		}
	}
	m.replacePlayerPlaylist(providerTracks)
	if m.markerColumns().bookmark {
		t.Fatal("unrelated radio favorite reserved a column")
	}
	if allocs := testing.AllocsPerRun(20, func() { m.markerColumns() }); allocs != 0 {
		t.Fatalf("unchanged marker state allocated %v times", allocs)
	}
	m.playlist.Add(tracks[0])
	if !m.markerColumns().bookmark {
		t.Fatal("appending an offscreen favorite did not invalidate the cache")
	}
}

// Compare warm repaints with a real revision change on every iteration. Keep
// SetIndex near the start of the order so its own lookup does not dominate the
// cost of recounting the playlist. Fixture construction and disk I/O are untimed.
func BenchmarkRadioMarkerColumns(b *testing.B) {
	b.Setenv("CLIAMP_CONFIG_DIR", b.TempDir())
	favorites := radio.LoadFavorites()
	if added, err := favorites.Toggle(radio.CatalogStation{Name: "Station 0", URL: "https://radio.example/0"}); err != nil || !added {
		b.Fatalf("toggle = %v, %v", added, err)
	}
	for _, kind := range []string{"local", "provider", "radio"} {
		for _, count := range []int{10, 2000, 20000} {
			for _, advance := range []bool{false, true} {
				b.Run(fmt.Sprintf("%s/tracks=%d/advance=%v", kind, count, advance), func(b *testing.B) {
					m := keybindingTestModel()
					m.SetRadioFavorites(favorites)
					tracks := make([]playlist.Track, count)
					for i := range tracks {
						switch kind {
						case "local":
							tracks[i] = playlist.Track{Path: fmt.Sprintf("/music/%d.mp3", i)}
						case "provider":
							tracks[i] = playlist.Track{
								Path: fmt.Sprintf("https://nav.example/%d", i), Stream: true,
								ProviderMeta: map[string]string{"navidrome.id": fmt.Sprint(i)},
							}
						case "radio":
							path := fmt.Sprintf("https://radio.example/%d", i)
							tracks[i] = playlist.Track{
								Path: path, Stream: true, Realtime: true,
								ProviderMeta: map[string]string{"radio.name": fmt.Sprintf("Station %d", i), "radio.url": path},
							}
						}
					}
					m.replacePlayerPlaylist(tracks)
					m.markerColumns()
					nextIndex := 1
					b.ReportAllocs()
					for b.Loop() {
						if advance {
							m.playlist.SetIndex(nextIndex)
							nextIndex = 1 - nextIndex
						}
						m.markerColumns()
					}
				})
			}
		}
	}
}

func TestRadioInitializationAndSwitchUseCurrentState(t *testing.T) {
	m, p, _ := radioFavoriteTestModel(t)
	var notification tea.Msg
	for _, cmd := range m.Init()().(tea.BatchMsg) {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(radioListsRefreshMsg); ok {
				notification = msg
			}
		}
	}
	if notification == nil {
		t.Fatal("initialization did not request current Radio state")
	}
	p.AppendCatalog([]radio.CatalogStation{{Name: "New FM", URL: "https://radio.example/new"}})
	updated, _ := m.Update(notification)
	m = updated.(Model)
	if m.providerLists[len(m.providerLists)-1].ID != "c:2" {
		t.Fatal("initialization used stale rows")
	}
	m.provider = commandsTestProvider{name: "Other"}
	m.switchProvider(len(m.providers) - 1)
	if m.provLoading || m.providerLists[len(m.providerLists)-1].ID != "c:2" {
		t.Fatal("switching to Radio did not synchronously project current rows")
	}
}

// A local favorite mutation must not turn an idle, unfinished catalog into a
// network request, whether it originates in playback or in the provider pane.
func TestRadioFavoriteDoesNotStartCatalogLoading(t *testing.T) {
	for _, pane := range []string{"playback", "provider"} {
		for _, remove := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/remove=%v", pane, remove), func(t *testing.T) {
				m, p, tracks := radioFavoriteTestModel(t)
				m.replacePlayerPlaylist(tracks)
				if remove {
					if _, _, err := p.ToggleFavorite("c:0"); err != nil {
						t.Fatal(err)
					}
				}
				if err := m.refreshProviderListsNow(); err != nil {
					t.Fatal(err)
				}
				for i, row := range m.providerLists {
					if row.ID == "c:0" {
						m.provCursor = i
					}
				}
				if pane == "provider" {
					m.focus = focusProvider
				}
				m.catalogBatch = catalogBatchState{offset: 2}
				catalogGen := m.requests.catalog
				providerGen := m.requests.provider

				updated, cmd := m.Update(tea.KeyPressMsg{Text: "f"})
				m = updated.(Model)
				if cmd != nil || m.catalogBatch.loading || m.catalogBatch.done || m.catalogBatch.offset != 2 || m.requests.catalog != catalogGen {
					t.Fatalf("local favorite scheduled catalog work: cmd=%v, state=%+v", cmd != nil, m.catalogBatch)
				}
				if m.requests.provider == providerGen {
					t.Fatal("favorite did not invalidate pending provider refreshes")
				}
				if m.radioFavorites.Contains(tracks[0].Path) == remove {
					t.Fatal("favorite did not change")
				}
				if m.providerLists[m.provCursor].ID != "c:0" {
					t.Fatal("favorite moved the selection")
				}
				hasFavorite := false
				for _, row := range m.providerLists {
					hasFavorite = hasFavorite || row.ID == "f:"+tracks[0].Path
				}
				if hasFavorite == remove {
					t.Fatal("favorite rows were not refreshed synchronously")
				}
			})
		}
	}
}

func TestRadioPlaybackFavoritePreservesPendingTrackLoad(t *testing.T) {
	m, _, tracks := radioFavoriteTestModel(t)
	m.replacePlayerPlaylist(tracks)
	if err := m.refreshProviderListsNow(); err != nil {
		t.Fatal(err)
	}
	for i, row := range m.providerLists {
		if row.ID == "c:1" {
			m.provCursor = i
		}
	}
	m.focus = focusProvider
	pending := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if pending == nil || !m.provLoading {
		t.Fatal("provider selection did not start a track load")
	}
	trackGen := m.requests.tracks
	// Leave the provider pane while its result is still pending.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.focus != focusPlaylist {
		t.Fatal("did not return to playback")
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "f"})
	m = updated.(Model)
	if cmd != nil || !m.provLoading || m.requests.tracks != trackGen {
		t.Fatal("favorite changed the pending track load")
	}
	if !m.radioFavorites.Contains(tracks[0].Path) {
		t.Fatal("playback favorite did not persist")
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.focus != focusProvider {
		t.Fatal("did not return to the provider")
	}
	if cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || m.requests.tracks != trackGen {
		t.Fatal("favorite allowed a duplicate track load")
	}
	updated, _ = m.Update(pending())
	m = updated.(Model)
	track, ok := m.playlist.Track(0)
	if m.provLoading || !ok || track.Path != tracks[1].Path {
		t.Fatal("original track load did not complete")
	}
}

func TestHeartFavoriteHelpRequiresPlaylistFocus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		focus   focusArea
		enabled bool
	}{
		{"playlist", focusPlaylist, true},
		{"provider", focusProvider, false},
		{"volume", focusVolume, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, tracks := radioFavoriteTestModel(t)
			m.replacePlayerPlaylist(tracks)
			hearts := &dirSourceTestProvider{}
			m.favMgr, m.localProvider = hearts, hearts
			m.focus = tc.focus
			m.layout.tier = layoutMinimal
			found := false
			for _, entry := range m.buildKeymapEntries() {
				found = found || entry.action == "Favorite track"
			}
			if found != tc.enabled {
				t.Fatalf("heart favorite keymap visibility = %v, want %v", found, tc.enabled)
			}
			m.handleKey(tea.KeyPressMsg{Text: "n"})
			if hearts.IsFavorited(tracks[0].Path) != tc.enabled {
				t.Fatal("heart favorite handler disagrees with help")
			}
		})
	}
}

func TestRadioStarBadgeMatchesRows(t *testing.T) {
	m, p, tracks := radioFavoriteTestModel(t)
	if _, _, err := p.ToggleFavorite("c:0"); err != nil {
		t.Fatal(err)
	}
	local := playlist.Track{Path: "/music/local.mp3", Title: "Local"}
	bookmarked := local
	bookmarked.Bookmark = true
	unfavoritedBookmark := tracks[1]
	unfavoritedBookmark.Bookmark = true
	favoritedBookmark := tracks[0]
	favoritedBookmark.Bookmark = true
	wrapper := tracks[0]
	wrapper.Path = "https://cdn.example/resolved"
	offscreen := append(make([]playlist.Track, 100), tracks[0])
	for _, tc := range []struct {
		name   string
		tracks []playlist.Track
		saved  bool
		want   int
	}{
		{name: "unrelated favorites", tracks: []playlist.Track{local}},
		{name: "radio favorite", tracks: tracks, want: 1},
		{name: "local bookmark", tracks: []playlist.Track{bookmarked}, want: 1},
		{name: "mixed meanings", tracks: []playlist.Track{bookmarked, tracks[0]}, want: 2},
		{name: "wrapper endpoints count as rows", tracks: []playlist.Track{tracks[0], wrapper}, want: 2},
		{name: "radio favorite overrides bookmark", tracks: []playlist.Track{unfavoritedBookmark}},
		{name: "no double count", tracks: []playlist.Track{favoritedBookmark}, want: 1},
		{name: "saved playlist uses bookmarks", tracks: []playlist.Track{unfavoritedBookmark, tracks[0]}, saved: true, want: 1},
		{name: "offscreen stars count", tracks: offscreen, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.replacePlayerPlaylist(tc.tracks)
			if tc.saved {
				m.loadedPlaylist = "Saved"
			}
			header := ansi.Strip(m.renderPlaybackHeader())
			if tc.want == 0 {
				if strings.Contains(header, "[★ ") {
					t.Fatalf("unexpected star badge: %s", header)
				}
			} else if !strings.Contains(header, fmt.Sprintf("[★ %d]", tc.want)) {
				t.Fatalf("header = %s; want %d starred rows", header, tc.want)
			}
			if m.markerColumns().bookmark != (tc.want > 0) {
				t.Fatal("star badge and column disagree")
			}
		})
	}
}

func TestRadioStarBadgeCountInvalidation(t *testing.T) {
	m, p, tracks := radioFavoriteTestModel(t)
	m.replacePlayerPlaylist(tracks)
	for _, step := range []struct {
		id   string
		want int
	}{
		{"c:0", 1},
		{"c:1", 2},
		{"c:0", 1},
	} {
		if _, _, err := p.ToggleFavorite(step.id); err != nil {
			t.Fatal(err)
		}
		if header := ansi.Strip(m.renderPlaybackHeader()); !strings.Contains(header, fmt.Sprintf("[★ %d]", step.want)) {
			t.Fatalf("stale count after toggling %s: %s", step.id, header)
		}
	}
	m.playlist.Remove(1)
	if strings.Contains(ansi.Strip(m.renderPlaybackHeader()), "[★ ") || m.markerColumns().bookmark {
		t.Fatal("removed row still contributes a star")
	}
}

func TestRadioFavoriteKeyFollowsDisplayedState(t *testing.T) {
	for _, pane := range []string{"playback", "provider"} {
		for _, remove := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/remove=%v", pane, remove), func(t *testing.T) {
				m, p, tracks := radioFavoriteTestModel(t)
				m.replacePlayerPlaylist(tracks)
				if remove {
					if _, _, err := p.ToggleFavorite("c:0"); err != nil {
						t.Fatal(err)
					}
				}
				if err := m.refreshProviderListsNow(); err != nil {
					t.Fatal(err)
				}
				selectedID := "c:0"
				if remove {
					selectedID = "f:" + tracks[0].Path
				}
				for i, row := range m.providerLists {
					if row.ID == selectedID {
						m.provCursor = i
					}
				}
				if pane == "provider" {
					m.focus = focusProvider
				}
				station, _ := radio.StationFromTrack(tracks[0])
				remote := radio.LoadFavorites()
				if _, err := remote.Toggle(station); err != nil {
					t.Fatal(err)
				}
				if m.playlistTrackStarred(tracks[0]) != remove {
					t.Fatal("test requires the displayed state to remain stale")
				}
				m.markerColumns() // Warm the cache before the local toggle.
				updated, cmd := m.Update(tea.KeyPressMsg{Text: "f"})
				m = updated.(Model)
				if cmd != nil || m.playlistTrackStarred(tracks[0]) == remove || radio.LoadFavorites().Contains(station.URL) == remove {
					t.Fatal("f did not apply the displayed add/remove intent")
				}
				if m.markerColumns().bookmark == remove {
					t.Fatal("local toggle did not invalidate the star cache")
				}
				wantStatus := "Favorited"
				if remove {
					wantStatus = "Removed"
				}
				if !strings.Contains(m.status.text, wantStatus) {
					t.Fatalf("feedback disagrees with visible intent: %s", m.status.text)
				}
			})
		}
	}
}
