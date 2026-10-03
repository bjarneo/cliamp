package model

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// fakeTrackFavoriter is a provider.TrackFavoriter that owns tracks whose
// path starts with prefix.
type fakeTrackFavoriter struct {
	commandsTestProvider
	prefix string
	err    error

	mu    sync.Mutex
	calls []string
}

func (p *fakeTrackFavoriter) CanFavoriteTrack(track playlist.Track) bool {
	return strings.HasPrefix(track.Path, p.prefix)
}

func (p *fakeTrackFavoriter) SetTrackFavorite(_ context.Context, track playlist.Track, favorite bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := "remove"
	if favorite {
		state = "add"
	}
	p.calls = append(p.calls, state+" "+track.Path)
	return p.err
}

func (p *fakeTrackFavoriter) recorded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

// runCmd runs cmd and every command it batches, and returns the messages.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// favoriteKeyTestModel returns a model with a favorites store in a temp
// directory and one local track in the playlist, the playlist manager, the
// provider browser, and the search results.
func favoriteKeyTestModel(t *testing.T) (Model, *favorites.Store) {
	t.Helper()
	local := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := keybindingTestModel()
	m.localProvider, m.favStore = local, local.useFavorites(t)
	m.focus = focusPlaylist
	m.playlist.Add(playlist.Track{Path: "/playlist.mp3", Title: "Playlist"})
	return m, m.favStore
}

func TestFavoriteKeyDispatchByContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		setup    func(*Model)
		key      string
		wantPath string // the track that f favorites; empty when the key does nothing
		wantHelp string
	}{
		{
			name:     "playback playlist",
			setup:    func(*Model) {},
			key:      "f",
			wantPath: "/playlist.mp3",
			wantHelp: "Favorite track",
		},
		{
			name:  "n is unbound",
			setup: func(*Model) {},
			key:   "n",
		},
		{
			name: "playlist manager tracks",
			setup: func(m *Model) {
				m.plManager = plManagerState{visible: true, screen: plMgrScreenTracks, selPlaylist: "music"}
				m.plMgrLoadTracks([]playlist.Track{{Path: "/manager.mp3", Title: "Manager"}})
			},
			key:      "f",
			wantPath: "/manager.mp3",
			wantHelp: "Favorite",
		},
		{
			name: "browser track list",
			setup: func(m *Model) {
				m.navBrowser = navBrowserState{
					prov: commandsTestProvider{name: "Navidrome"}, visible: true,
					mode: navBrowseModeByAlbum, screen: navBrowseScreenTracks,
					tracks: []playlist.Track{{Path: "/first.mp3", Title: "First"}, {Path: "/browser.mp3", Title: "Browser"}},
					cursor: 1,
				}
			},
			key:      "f",
			wantPath: "/browser.mp3",
			wantHelp: "Favorite track",
		},
		{
			name: "filtered browser track list",
			setup: func(m *Model) {
				m.navBrowser = navBrowserState{
					prov: commandsTestProvider{name: "Navidrome"}, visible: true,
					mode: navBrowseModeByAlbum, screen: navBrowseScreenTracks,
					tracks: []playlist.Track{{Path: "/first.mp3", Title: "First"}, {Path: "/match.mp3", Title: "Match"}},
					search: "match",
				}
				m.navUpdateSearch()
			},
			key:      "f",
			wantPath: "/match.mp3",
			wantHelp: "Favorite track",
		},
		{
			name: "search results track",
			setup: func(m *Model) {
				m.searchOverlay = searchOverlayState{
					prov: commandsTestProvider{name: "Spotify"}, visible: true, screen: searchOverlayResults,
					results: []playlist.Track{albumResult("Album"), {Path: "spotify:track:1", Title: "Song"}}, cursor: 1,
				}
			},
			key:      "f",
			wantPath: "spotify:track:1",
			wantHelp: "Favorite track",
		},
		{
			name: "network search results",
			setup: func(m *Model) {
				m.netSearch = netSearchState{
					active: true, screen: netSearchResults,
					results: []playlist.Track{{Path: "https://youtube.com/watch?v=1", Title: "Video"}},
				}
			},
			key:      "f",
			wantPath: "https://youtube.com/watch?v=1",
			wantHelp: "Favorite track",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, store := favoriteKeyTestModel(t)
			tc.setup(&m)
			if tc.wantHelp != "" {
				found := false
				for _, entry := range m.buildKeymapEntries() {
					found = found || (entry.key == "f" && entry.action == tc.wantHelp)
				}
				if !found {
					t.Fatalf("keymap has no f entry %q", tc.wantHelp)
				}
			}

			m.handleKey(tea.KeyPressMsg{Text: tc.key})
			if tc.wantPath == "" {
				if store.Count() != 0 {
					t.Fatalf("%s favorited a track", tc.key)
				}
				return
			}
			if store.Count() != 1 || !store.IsFavorited(tc.wantPath) {
				t.Fatalf("favorites count = %d, want only %s", store.Count(), tc.wantPath)
			}
			if _, ok := m.favSet[tc.wantPath]; !ok {
				t.Fatal("favSet was not refreshed")
			}
			if m.status.kind == feedbackError {
				t.Fatalf("unexpected error: %s", m.status.text)
			}

			m.handleKey(tea.KeyPressMsg{Text: tc.key})
			if store.Count() != 0 {
				t.Fatalf("second f did not remove %s", tc.wantPath)
			}
		})
	}
}

// On a show or an album, f keeps the provider favorite, such as a podcast
// subscription, and does not touch the ♥ favorites.
func TestFavoriteKeyKeepsProviderFavorites(t *testing.T) {
	for _, tc := range []struct {
		view   string
		wantID string
	}{
		{view: "pane", wantID: "s:target"},
		{view: "category", wantID: "target"},
		{view: "results", wantID: "target"},
	} {
		t.Run(tc.view, func(t *testing.T) {
			m, p := favoriteAlbumTestModel(tc.view)
			local := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
			m.localProvider, m.favStore = local, local.useFavorites(t)
			m.handleKey(tea.KeyPressMsg{Text: "f"})
			if len(p.toggled) != 1 || p.toggled[0] != tc.wantID {
				t.Fatalf("provider favorites = %v, want %s", p.toggled, tc.wantID)
			}
			if m.favStore.Count() != 0 {
				t.Fatal("f changed track favorites")
			}
		})
	}
}

func TestTrackFavoriteSyncRouting(t *testing.T) {
	for _, tc := range []struct {
		name        string
		path        string
		active      bool // the favoriter is the active provider
		err         error
		wantCalls   []string
		wantWarning string
	}{
		{
			name:      "active provider owns the track",
			path:      "fake:track:1",
			active:    true,
			wantCalls: []string{"add fake:track:1", "remove fake:track:1"},
		},
		{
			name:      "inactive provider owns the track",
			path:      "fake:track:2",
			wantCalls: []string{"add fake:track:2", "remove fake:track:2"},
		},
		{
			name: "no provider owns the track",
			path: "/local.mp3",
		},
		{
			name:        "provider call fails",
			path:        "fake:track:3",
			err:         errors.New("offline"),
			wantCalls:   []string{"add fake:track:3", "remove fake:track:3"},
			wantWarning: "Fake did not save the favorite: offline",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, store := favoriteKeyTestModel(t)
			fake := &fakeTrackFavoriter{commandsTestProvider: commandsTestProvider{name: "Fake"}, prefix: "fake:", err: tc.err}
			m.providers = append(m.providers, provider.Entry{Key: "fake", Name: "Fake", Provider: fake})
			if tc.active {
				m.provider = fake
			}
			track := playlist.Track{Path: tc.path, Title: "Song"}

			for _, want := range []bool{true, false} {
				cmd, err := m.toggleTrackFavorite(track)
				if err != nil {
					t.Fatal(err)
				}
				// The local store changes before the provider call runs.
				if got := store.IsFavorited(tc.path); got != want {
					t.Fatalf("local favorite = %v, want %v", got, want)
				}
				for _, msg := range runCmd(cmd) {
					if synced, ok := msg.(trackFavoriteSyncedMsg); ok {
						updated, _ := m.Update(synced)
						m = updated.(Model)
					}
				}
				if got := store.IsFavorited(tc.path); got != want {
					t.Fatal("a provider failure changed the local favorite")
				}
			}
			if got := fake.recorded(); strings.Join(got, ",") != strings.Join(tc.wantCalls, ",") {
				t.Fatalf("provider calls = %v, want %v", got, tc.wantCalls)
			}
			if tc.wantWarning == "" {
				if m.status.kind == feedbackWarning {
					t.Fatalf("unexpected warning: %s", m.status.text)
				}
				return
			}
			if m.status.kind != feedbackWarning || m.status.text != tc.wantWarning {
				t.Fatalf("status = %v %q, want warning %q", m.status.kind, m.status.text, tc.wantWarning)
			}
		})
	}
}

// A second toggle made before the first provider call runs wins, so the
// provider ends with the local state.
func TestTrackFavoriteSyncKeepsLastState(t *testing.T) {
	m, store := favoriteKeyTestModel(t)
	fake := &fakeTrackFavoriter{commandsTestProvider: commandsTestProvider{name: "Fake"}, prefix: "fake:"}
	m.providers = append(m.providers, provider.Entry{Key: "fake", Name: "Fake", Provider: fake})
	track := playlist.Track{Path: "fake:track:1", Title: "Song"}

	first, err := m.toggleTrackFavorite(track)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.toggleTrackFavorite(track)
	if err != nil {
		t.Fatal(err)
	}
	runCmd(second)
	runCmd(first)
	if store.IsFavorited(track.Path) {
		t.Fatal("track should not be a favorite after two toggles")
	}
	if got := fake.recorded(); len(got) != 1 || got[0] != "remove fake:track:1" {
		t.Fatalf("provider calls = %v, want only the last state", got)
	}
}

// fixedTracksProvider returns the same tracks for every playlist.
type fixedTracksProvider struct {
	commandsTestProvider
	tracks []playlist.Track
}

func (p fixedTracksProvider) Tracks(string) ([]playlist.Track, error) { return p.tracks, nil }

// The bookmark field of IPC track info keeps its JSON name and reports the ♥
// favorite state, so cliamp status --json matches the playlist rows.
func TestIPCTrackInfoBookmarkReportsFavorite(t *testing.T) {
	m, _ := favoriteKeyTestModel(t)
	legacy := playlist.Track{Path: "/legacy.mp3", Title: "Legacy", Bookmark: true}
	m.playlist.Add(legacy)
	m.favSet = map[string]struct{}{"/playlist.mp3": {}}
	m.playlist.SetIndex(0)

	snapshot := m.runtimeSnapshot()
	if snapshot.LogicalTrack == nil || !snapshot.LogicalTrack.Bookmark {
		t.Fatalf("snapshot logical track = %+v, want bookmark true", snapshot.LogicalTrack)
	}
	queue := runV2(t, &m, "queue.list", ipc.Request{}).Tracks
	if !queue[0].Bookmark || queue[1].Bookmark {
		t.Fatalf("queue bookmarks = %v, %v; want true, false", queue[0].Bookmark, queue[1].Bookmark)
	}
	page := m.v2PlaylistResponsePage(0, 0).Tracks
	if !page[0].Bookmark || page[1].Bookmark {
		t.Fatalf("playlist page bookmarks = %v, %v; want true, false", page[0].Bookmark, page[1].Bookmark)
	}
	if ipcTrackFromInfo(ipc.TrackInfo{Path: "/x.mp3", Bookmark: true}).Bookmark {
		t.Fatal("a favorite from IPC must not set the legacy bookmark flag")
	}

	// A command that runs later uses the favorites captured when it was made.
	prov := fixedTracksProvider{commandsTestProvider{name: "Fixed"}, []playlist.Track{{Path: "/playlist.mp3"}, legacy}}
	m.providers = append(m.providers, provider.Entry{Key: "fixed", Name: "Fixed", Provider: prov})
	reply := make(chan ipc.Response, 1)
	cmd := m.handleIPCLibrary(ipcLibraryRequest{Op: "provider.tracks", Provider: "fixed", Playlist: "any", Reply: reply})
	m.favSet = nil
	runCmd(cmd)
	tracks := (<-reply).Tracks
	if len(tracks) != 2 || !tracks[0].Bookmark || tracks[1].Bookmark {
		t.Fatalf("provider tracks = %+v", tracks)
	}
}

// playlist.bookmark is a legacy IPC alias: it toggles the ♥ favorite.
func TestIPCBookmarkAliasTogglesFavorite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		track   *ipc.TrackInfo
		noStore bool
		wantOK  bool
	}{
		{name: "toggles the favorite", track: &ipc.TrackInfo{Path: "/song.mp3", Title: "Song"}, wantOK: true},
		{name: "track is required", wantOK: false},
		{name: "no favorites store", track: &ipc.TrackInfo{Path: "/song.mp3"}, noStore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, store := favoriteKeyTestModel(t)
			if tc.noStore {
				m.favStore = nil
			}
			for _, want := range []bool{true, false} {
				reply := make(chan ipc.Response, 1)
				runCmd(m.handleIPCLibrary(ipcLibraryRequest{Op: "playlist.bookmark", Provider: "local", Playlist: "Mix", Track: tc.track, Reply: reply}))
				response := <-reply
				if response.OK != tc.wantOK {
					t.Fatalf("response = %+v, want OK=%v", response, tc.wantOK)
				}
				if !tc.wantOK {
					if store.Count() != 0 {
						t.Fatal("a failed request changed favorites")
					}
					return
				}
				if got := store.IsFavorited(tc.track.Path); got != want {
					t.Fatalf("favorited = %v, want %v", got, want)
				}
				if _, ok := m.favSet[tc.track.Path]; ok != want {
					t.Fatal("favSet does not match the store")
				}
			}
		})
	}
}

// countingListsProvider counts the calls to Playlists, the rows of the
// provider pane.
type countingListsProvider struct {
	commandsTestProvider
	mu    sync.Mutex
	lists int
}

func (p *countingListsProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lists++
	return nil, nil
}

func (p *countingListsProvider) listCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lists
}

// A history record or a favorite toggle writes a local store. Only the Local
// pane lists these stores, so only an active Local pane reloads its rows. A
// remote pane gets no provider call.
func TestLocalStoreWritesRefreshOnlyTheLocalPane(t *testing.T) {
	track := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	for _, write := range []struct {
		name string
		run  func(m *Model) tea.Cmd
	}{
		{name: "history record", run: func(m *Model) tea.Cmd { return m.recordListenedTrack(track) }},
		{name: "favorite toggle", run: func(m *Model) tea.Cmd {
			cmd, err := m.toggleTrackFavorite(track)
			if err != nil {
				t.Fatal(err)
			}
			return cmd
		}},
	} {
		for _, active := range []string{providerKeyLocal, "navidrome"} {
			t.Run(write.name+"/"+active, func(t *testing.T) {
				t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
				localProv := &countingListsProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
				remote := &countingListsProvider{commandsTestProvider: commandsTestProvider{name: "Navidrome"}}
				m := Model{
					player:       &playbackFakeEngine{},
					playlist:     playlist.New(),
					favStore:     favorites.New(),
					historyStore: history.New(),
					providers: []provider.Entry{
						{Key: providerKeyLocal, Name: "Local", Provider: localProv},
						{Key: "navidrome", Name: "Navidrome", Provider: remote},
					},
				}
				m.provider, m.provPillIdx = localProv, 0
				if active != providerKeyLocal {
					m.provider, m.provPillIdx = remote, 1
				}

				runCmds(write.run(&m))
				if got, want := localProv.listCalls(), map[bool]int{true: 1, false: 0}[active == providerKeyLocal]; got != want {
					t.Fatalf("Local pane loads = %d, want %d", got, want)
				}
				if got := remote.listCalls(); got != 0 {
					t.Fatalf("remote Playlists calls = %d, want 0 after a local write", got)
				}
			})
		}
	}
}

// runCmds runs cmd and every command of a batch that it returns.
func runCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmds(c)
		}
	}
}
