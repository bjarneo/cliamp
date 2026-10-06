package model

import (
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
	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
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
	m := keybindingTestModel()
	m.width, m.height = 180, 40
	m.SetRadioFavorites(favorites)
	m.provider = p
	m.providers = append(m.providers, provider.Entry{Key: "radio", Name: "Radio", Provider: p})
	m.provPillIdx = len(m.providers) - 1
	m.catalogBatch.done = true // No network work during provider-list refresh.
	return m, p, tracks
}

func TestRadioFavoriteAfterBrowseLoadsPlaylist(t *testing.T) {
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
			for _, list := range m.provPane.lists {
				if list.ID == "f:"+tracks[1].Path {
					found = strings.Contains(list.Name, "Selected FM")
				}
			}
			if !found || !m.playlistTrackFavorited(tracks[1]) {
				t.Fatal("favorite missing from Radio pane or playback heart state")
			}
			if plain := ansi.Strip(m.renderPlaylist()); !strings.Contains(plain, "♥") || strings.Contains(plain, "★") {
				t.Fatalf("station favorite did not render as a heart:\n%s", plain)
			}
			track, _ := m.playlist.Track(1)
			if len(m.favSet) != 0 {
				t.Fatal("radio favorite changed track favorites")
			}

			m.handleKey(tea.KeyPressMsg{Text: "f"})
			if m.radioFavorites.Count() != 0 || m.playlistTrackFavorited(track) || radio.LoadFavorites().Count() != 0 {
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
	if m.playlistTrackFavorited(tracks[1]) {
		t.Fatal("playback heart did not follow catalog removal")
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

// failingFavorites returns a favorites store whose writes fail, because the
// directory of its file is a regular file.
func failingFavorites(t *testing.T) *favorites.Store {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return favorites.NewAt(filepath.Join(blocker, "favorites.toml"))
}

// A station row outside saved playlists toggles the station favorite. In a
// saved local playlist the same row toggles the track favorite, because the
// row no longer carries the station metadata the Radio pane uses.
func TestRadioRowFavoriteDispatch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		saved       bool
		fail        bool
		wantHelp    string
		wantStation bool
		wantTrack   bool
	}{
		{name: "unsaved playlist", wantHelp: "Favorite station", wantStation: true},
		{name: "saved playlist", saved: true, wantHelp: "Favorite track", wantTrack: true},
		{name: "saved playlist write failure", saved: true, fail: true, wantHelp: "Favorite track"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, tracks := radioFavoriteTestModel(t)
			m.replacePlayerPlaylist(tracks)
			if tc.saved {
				m.loadedPlaylist = "Saved radios"
			}
			local := &dirSourceTestProvider{}
			hearts := local.useFavorites(t)
			if tc.fail {
				local.favs = failingFavorites(t)
			}
			m.localProvider, m.favStore = local, local.favs
			if help := m.commandHelp(commandModeMain); !strings.Contains(help, tc.wantHelp) {
				t.Fatalf("help = %q, want %q", help, tc.wantHelp)
			}

			m.handleKey(tea.KeyPressMsg{Text: "f"})
			if got := m.radioFavorites.Contains(tracks[0].Path); got != tc.wantStation {
				t.Fatalf("station favorite = %v, want %v", got, tc.wantStation)
			}
			if got := hearts.IsFavorited(tracks[0].Path); got != tc.wantTrack {
				t.Fatalf("track favorite = %v, want %v", got, tc.wantTrack)
			}
			if got := m.playlistTrackFavorited(tracks[0]); got != (tc.wantStation || tc.wantTrack) {
				t.Fatalf("row heart = %v, want %v", got, tc.wantStation || tc.wantTrack)
			}
			if tc.fail && (m.status.kind != feedbackError || !strings.Contains(m.status.text, "Favorite failed")) {
				t.Fatalf("missing error feedback: %q", m.status.text)
			}
			if tc.fail {
				return
			}

			m.handleKey(tea.KeyPressMsg{Text: "f"})
			if m.radioFavorites.Count() != 0 || hearts.Count() != 0 || m.playlistTrackFavorited(tracks[0]) {
				t.Fatal("second f did not remove the favorite")
			}
		})
	}
}

// IPC playlist.bookmark on a station toggles the store that f toggles and
// that the reported bookmark reads. A row of a loaded saved playlist uses the
// favorites store. Any other station uses its station favorite, also while a
// saved playlist is loaded.
func TestIPCBookmarkStationRowMatchesReportedBookmark(t *testing.T) {
	for _, tc := range []struct {
		name        string
		saved       bool
		notInQueue  bool
		list        string
		wantStation bool
	}{
		{name: "unsaved playlist", list: "queue.list", wantStation: true},
		{name: "saved playlist", saved: true, list: "queue.list"},
		{name: "provider list with a saved playlist loaded", saved: true, notInQueue: true, list: "provider.tracks", wantStation: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, tracks := radioFavoriteTestModel(t)
			if tc.notInQueue {
				m.replacePlayerPlaylist([]playlist.Track{{Path: "a.mp3", Title: "A"}})
			} else {
				m.replacePlayerPlaylist(tracks)
			}
			if tc.saved {
				m.loadedPlaylist = "Saved radios"
			}
			local := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
			store := local.useFavorites(t)
			m.localProvider, m.favStore = local, store
			info := ipcTrackInfo(tracks[0], 0, 0, false)

			for _, want := range []bool{true, false} {
				if response := runV2(t, &m, "playlist.bookmark", ipc.Request{Provider: "radio", Track: &info}); !response.OK {
					t.Fatalf("bookmark response = %+v", response)
				}
				listed := runV2(t, &m, tc.list, ipc.Request{Provider: "radio", Playlist: "c:0"})
				if !listed.OK || len(listed.Tracks) == 0 {
					t.Fatalf("%s response = %+v", tc.list, listed)
				}
				if got := listed.Tracks[0].Bookmark; got != want {
					t.Fatalf("reported bookmark = %v, want %v", got, want)
				}
				if got := m.radioFavorites.Contains(tracks[0].Path); got != (want && tc.wantStation) {
					t.Fatalf("station favorite = %v, want %v", got, want && tc.wantStation)
				}
				if got := store.IsFavorited(tracks[0].Path); got != (want && !tc.wantStation) {
					t.Fatalf("track favorite = %v, want %v", got, want && !tc.wantStation)
				}
			}
		})
	}
}

func TestRadioFavoriteWriteFailure(t *testing.T) {
	m, _, tracks := radioFavoriteTestModel(t)
	m.replacePlayerPlaylist(tracks)
	before := m.renderPlaylist()
	// Prevent the atomic writer from replacing the target with a regular file.
	if err := os.Mkdir(filepath.Join(os.Getenv("CLIAMP_CONFIG_DIR"), "radio_favorites.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if cmd := m.handleKey(tea.KeyPressMsg{Text: "f"}); cmd != nil {
		t.Fatal("failed write requested a list refresh")
	}
	if m.radioFavorites.Count() != 0 || m.playlistTrackFavorited(tracks[0]) {
		t.Fatal("failed write published favorite state")
	}
	if m.renderPlaylist() != before {
		t.Fatal("failed write changed the playlist rows")
	}
	if m.status.kind != feedbackError || !strings.Contains(m.status.text, "Favorite failed") {
		t.Fatalf("missing error feedback: %q", m.status.text)
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
				if !m.playlistTrackFavorited(track) {
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
	m.provPane.lists = providerListsWithBrowse(p, lists)
	for i, list := range m.provPane.lists {
		if list.ID == "c:1" {
			m.provPane.cursor = i
		}
	}
	cmd := m.openProviderList(m.provPane.cursor)
	updated, _ := m.Update(cmd())
	m = updated.(Model)

	for _, wantFavorite := range []bool{true, false} {
		oldCursor := m.provPane.cursor
		m.handleKey(tea.KeyPressMsg{Text: "f"})
		if selected := m.provPane.lists[m.provPane.cursor]; selected.ID != "c:1" {
			t.Fatalf("refresh moved selection to %+v, want c:1", selected)
		}
		delta := -1
		if wantFavorite {
			delta = 1
		}
		if m.provPane.cursor != oldCursor+delta {
			t.Fatalf("cursor = %d, want %d after favorite row change", m.provPane.cursor, oldCursor+delta)
		}
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.focus != focusProvider || m.provPane.lists[m.provPane.cursor].ID != "c:1" {
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
	for i, list := range m.provPane.lists {
		if list.ID == "c:1" {
			m.provPane.cursor = i
		}
	}
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if m.provPane.lists[m.provPane.cursor].ID != "c:1" {
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
			for i, list := range m.provPane.lists {
				if strings.HasPrefix(list.ID, "f:") {
					favoritesSeen++
					if favoritesSeen == 2 {
						m.provPane.cursor = i
						break
					}
				}
			}
			for _, wantFavorite := range []bool{false, true} {
				m.handleKey(tea.KeyPressMsg{Text: "f"})
				m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
				selected := m.provPane.lists[m.provPane.cursor]
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
	for i, list := range m.provPane.lists {
		if list.ID == "f:"+tracks[0].Path {
			m.provPane.cursor = i
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
	for _, list := range m.provPane.lists {
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
				for i, list := range m.provPane.lists {
					if list.ID == "c:1" {
						m.provPane.cursor = i
					}
				}
				m.replacePlayerPlaylist(tracks)
				m.catalogBatch = catalogBatchState{loading: true, offset: 2}
				m.requests.catalog = 1
				if cmd := m.handleKey(tea.KeyPressMsg{Text: "f"}); cmd != nil {
					t.Fatal("favorite refresh should be synchronous")
				}
				if m.provPane.lists[m.provPane.cursor].ID != "c:1" {
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
					if got := m.provPane.lists[m.provPane.cursor].ID; got != "c:1" {
						t.Fatalf("after %T selected %s, want c:1", msg, got)
					}
					found := false
					for _, row := range m.provPane.lists {
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

// A station favorite shows the same ♥ in the pinned favorite column as a track
// favorite, so toggling it never moves the titles.
func TestRadioFavoriteHeartKeepsLayout(t *testing.T) {
	m, p, radioTracks := radioFavoriteTestModel(t)
	local := playlist.Track{Path: "/music/local.mp3", Title: "Local"}
	legacyBookmark := local
	legacyBookmark.Bookmark = true
	wrapper := radioTracks[0]
	wrapper.Path = "https://cdn.example/resolved"
	for _, tc := range []struct {
		name   string
		tracks []playlist.Track
		saved  bool
		want   bool
	}{
		{name: "local files only", tracks: []playlist.Track{local}},
		{name: "legacy bookmark shows nothing", tracks: []playlist.Track{legacyBookmark}, saved: true},
		{name: "unfavorited station", tracks: radioTracks[1:]},
		{name: "favorite station", tracks: radioTracks[:1], want: true},
		{name: "mixed queue", tracks: []playlist.Track{local, radioTracks[0]}, want: true},
		{name: "resolved wrapper", tracks: []playlist.Track{wrapper}, want: true},
		{name: "saved playlist ignores radio favorite", tracks: radioTracks[:1], saved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.replacePlayerPlaylist(tc.tracks)
			if tc.saved {
				m.loadedPlaylist = "Saved"
			}
			before := m.renderPlaylist()
			cols := m.markerColumns()
			if _, _, err := p.ToggleFavorite("c:0"); err != nil {
				t.Fatal(err)
			}
			after := m.renderPlaylist()
			if got := strings.Contains(ansi.Strip(after), "♥"); got != tc.want {
				t.Errorf("heart shown = %v, want %v", got, tc.want)
			}
			if strings.Contains(after, "★") {
				t.Error("playlist rows show a star")
			}
			if m.markerColumns() != cols {
				t.Error("a favorite changed the marker columns")
			}
			beforeLines, afterLines := strings.Split(before, "\n"), strings.Split(after, "\n")
			for i := range min(len(beforeLines), len(afterLines)) {
				if ansi.StringWidth(beforeLines[i]) != ansi.StringWidth(afterLines[i]) {
					t.Errorf("row %d changed width:\n%q\n%q", i, beforeLines[i], afterLines[i])
				}
			}
			if !tc.want && after != before {
				t.Error("unrelated favorite changed playlist rendering")
			}
			if _, _, err := p.ToggleFavorite("c:0"); err != nil {
				t.Fatal(err)
			}
			if m.renderPlaylist() != before {
				t.Error("removing the favorite did not restore the rows")
			}
		})
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
	if m.provPane.lists[len(m.provPane.lists)-1].ID != "c:2" {
		t.Fatal("initialization used stale rows")
	}
	m.provider = commandsTestProvider{name: "Other"}
	m.switchProvider(len(m.providers) - 1)
	if m.provPane.loading || m.provPane.lists[len(m.provPane.lists)-1].ID != "c:2" {
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
				for i, row := range m.provPane.lists {
					if row.ID == "c:0" {
						m.provPane.cursor = i
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
				if m.provPane.lists[m.provPane.cursor].ID != "c:0" {
					t.Fatal("favorite moved the selection")
				}
				hasFavorite := false
				for _, row := range m.provPane.lists {
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
	for i, row := range m.provPane.lists {
		if row.ID == "c:1" {
			m.provPane.cursor = i
		}
	}
	m.focus = focusProvider
	pending := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if pending == nil || !m.provPane.loading {
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
	if cmd != nil || !m.provPane.loading || m.requests.tracks != trackGen {
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
	if m.provPane.loading || !ok || track.Path != tracks[1].Path {
		t.Fatal("original track load did not complete")
	}
}

func TestTrackFavoriteHelpRequiresPlaylistFocus(t *testing.T) {
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
			m, _, _ := radioFavoriteTestModel(t)
			local := playlist.Track{Path: "/music/local.mp3", Title: "Local"}
			m.replacePlayerPlaylist([]playlist.Track{local})
			localProv := &dirSourceTestProvider{}
			hearts := localProv.useFavorites(t)
			m.favStore, m.localProvider = hearts, localProv
			m.focus = tc.focus
			m.layout.tier = layoutMinimal
			found := false
			for _, entry := range m.buildKeymapEntries() {
				found = found || entry.action == "Favorite track"
			}
			if found != tc.enabled {
				t.Fatalf("track favorite keymap visibility = %v, want %v", found, tc.enabled)
			}
			if tc.focus == focusPlaylist {
				m.handleKey(tea.KeyPressMsg{Text: "f"})
				if !hearts.IsFavorited(local.Path) {
					t.Fatal("track favorite handler disagrees with help")
				}
			}
		})
	}
}

// The header shows the ♥ count of the favorites store and never a star badge,
// also for playlists with legacy bookmarks or station favorites.
func TestPlaybackHeaderShowsOnlyHeartBadge(t *testing.T) {
	m, p, tracks := radioFavoriteTestModel(t)
	if _, _, err := p.ToggleFavorite("c:0"); err != nil {
		t.Fatal(err)
	}
	legacyBookmark := playlist.Track{Path: "/music/local.mp3", Title: "Local", Bookmark: true}
	m.replacePlayerPlaylist(append(tracks, legacyBookmark))
	if header := ansi.Strip(m.renderPlaybackHeader()); strings.Contains(header, "★") {
		t.Fatalf("header shows a star badge: %s", header)
	}
	m.favSet = map[string]struct{}{"/music/local.mp3": {}}
	if header := ansi.Strip(m.renderPlaybackHeader()); !strings.Contains(header, "♥") || !strings.Contains(header, " 1]") {
		t.Fatalf("header = %s, want the heart badge", header)
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
				for i, row := range m.provPane.lists {
					if row.ID == selectedID {
						m.provPane.cursor = i
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
				if m.playlistTrackFavorited(tracks[0]) != remove {
					t.Fatal("test requires the displayed state to remain stale")
				}
				updated, cmd := m.Update(tea.KeyPressMsg{Text: "f"})
				m = updated.(Model)
				if cmd != nil || m.playlistTrackFavorited(tracks[0]) == remove || radio.LoadFavorites().Contains(station.URL) == remove {
					t.Fatal("f did not apply the displayed add/remove intent")
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
