package model

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// The queue edits, the playlist manager and the duration backfill save
// through playlistUpdater. A changed method set would make them skip the save.
var _ playlistUpdater = (*local.Provider)(nil)

// remoteListProvider serves the same tracks for every playlist and album, as
// a Navidrome server does for one list.
type remoteListProvider struct {
	commandsTestProvider
	tracks []playlist.Track
}

func (p remoteListProvider) Tracks(string) ([]playlist.Track, error)      { return p.tracks, nil }
func (p remoteListProvider) AlbumTracks(string) ([]playlist.Track, error) { return p.tracks, nil }

// dirFiles returns the names of the files in dir.
func dirFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// trackPaths returns the paths of tracks in order.
func trackPaths(tracks []playlist.Track) []string {
	paths := make([]string, len(tracks))
	for i, track := range tracks {
		paths[i] = track.Path
	}
	return paths
}

// A V2 provider.load marks only a saved local playlist as the loaded list.
// shift+down then writes the new order to that playlist file only. Favorites
// stays the loaded list for the ♥ rule, but it is not a playlist file.
func TestV2ProviderLoadKeepsWriteBacksLocal(t *testing.T) {
	a := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	b := playlist.Track{Path: "/music/b.mp3", Title: "B"}
	remote := []playlist.Track{{Path: "/navidrome/1.flac", Title: "One"}, {Path: "/navidrome/2.flac", Title: "Two"}}
	for _, tc := range []struct {
		name       string
		op         string
		params     ipc.Request
		wantLoaded string
		wantMix    []string
	}{
		{name: "local playlist", op: "provider.load", params: ipc.Request{Provider: "local", Playlist: "Mix"}, wantLoaded: "Mix", wantMix: []string{b.Path, a.Path}},
		{name: "local favorites", op: "provider.load", params: ipc.Request{Provider: "local", Playlist: favorites.PlaylistName}, wantLoaded: favorites.PlaylistName, wantMix: []string{a.Path, b.Path}},
		{name: "local history", op: "provider.load", params: ipc.Request{Provider: "local", Playlist: history.PlaylistName}, wantMix: []string{a.Path, b.Path}},
		{name: "navidrome playlist", op: "provider.load", params: ipc.Request{Provider: "navidrome", Playlist: "42"}, wantMix: []string{a.Path, b.Path}},
		{name: "navidrome album", op: "provider.load_album", params: ipc.Request{Provider: "navidrome", Album: "7"}, wantMix: []string{a.Path, b.Path}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			favs, hist := favorites.New(), history.New()
			lp := local.New(favs, hist)
			if err := lp.SavePlaylist("Mix", []playlist.Track{a, b}); err != nil {
				t.Fatal(err)
			}
			for i, track := range []playlist.Track{b, a} {
				if err := hist.Record(track, time.Unix(int64(1000+i), 0)); err != nil {
					t.Fatal(err)
				}
				if _, err := favs.Toggle(track); err != nil {
					t.Fatal(err)
				}
			}
			navidrome := remoteListProvider{commandsTestProvider{name: "Navidrome"}, remote}
			m := Model{
				player:        &playbackFakeEngine{},
				playlist:      playlist.New(),
				vis:           ui.NewVisualizer(44100),
				provider:      lp,
				localProvider: lp,
				providers: []provider.Entry{
					{Key: "local", Name: "Local", Provider: lp},
					{Key: "navidrome", Name: "Navidrome", Provider: navidrome},
				},
			}

			if response := runV2(t, &m, tc.op, tc.params); !response.OK || response.Total != 2 {
				t.Fatalf("response = %+v, want 2 loaded tracks", response)
			}
			if m.loadedPlaylist != tc.wantLoaded {
				t.Fatalf("loadedPlaylist = %q, want %q", m.loadedPlaylist, tc.wantLoaded)
			}

			m.focus = focusPlaylist
			m.plCursor = 0
			m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
			if m.plCursor != 1 {
				t.Fatalf("plCursor = %d, want 1 after the move", m.plCursor)
			}
			if m.status.kind == feedbackError {
				t.Fatalf("unexpected error: %s", m.status.text)
			}
			if got := dirFiles(t, filepath.Join(dir, "playlists")); !reflect.DeepEqual(got, []string{"Mix.toml"}) {
				t.Fatalf("playlist files = %v, want only Mix.toml", got)
			}
			mix, err := lp.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := trackPaths(mix); !reflect.DeepEqual(got, tc.wantMix) {
				t.Fatalf("Mix order = %v, want %v", got, tc.wantMix)
			}
		})
	}
}

// x on a row of a loaded playlist removes the track from the playlist file
// in one locked update. A track that another writer added after the load
// survives the removal, and Ctrl+Z puts the removed track back.
func TestQueueRemoveUpdatesTheLoadedPlaylistFile(t *testing.T) {
	a := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	b := playlist.Track{Path: "/music/b.mp3", Title: "B"}
	added := playlist.Track{Path: "/music/added.mp3", Title: "Added"}
	for _, tc := range []struct {
		name      string
		otherAdds bool // another writer adds a track after the load
		wantMix   []string
		wantUndo  []string
	}{
		{name: "only this writer", wantMix: []string{a.Path}, wantUndo: []string{a.Path, b.Path}},
		// The undo puts the removed track back at its place in the file.
		{name: "another writer added a track", otherAdds: true, wantMix: []string{a.Path, added.Path}, wantUndo: []string{a.Path, b.Path, added.Path}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			lp := local.New(nil, nil)
			if err := lp.SavePlaylist("Mix", []playlist.Track{a, b}); err != nil {
				t.Fatal(err)
			}
			m := Model{
				player:        &playbackFakeEngine{},
				playlist:      playlist.New(),
				vis:           ui.NewVisualizer(44100),
				provider:      lp,
				localProvider: lp,
				providers:     []provider.Entry{{Key: "local", Name: "Local", Provider: lp}},
			}
			if response := runV2(t, &m, "provider.load", ipc.Request{Provider: "local", Playlist: "Mix"}); !response.OK {
				t.Fatalf("provider.load = %+v", response)
			}
			if tc.otherAdds {
				if _, _, err := local.New(nil, nil).AddTracks("Mix", []playlist.Track{added}); err != nil {
					t.Fatal(err)
				}
			}

			m.focus = focusPlaylist
			m.plCursor = 1
			m.handleKey(tea.KeyPressMsg{Text: "x"})
			if m.status.kind == feedbackError {
				t.Fatalf("unexpected error: %s", m.status.text)
			}
			mix, err := lp.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := trackPaths(mix); !reflect.DeepEqual(got, tc.wantMix) {
				t.Fatalf("Mix after x = %v, want %v", got, tc.wantMix)
			}

			m.handleKey(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			if mix, err = lp.Tracks("Mix"); err != nil {
				t.Fatal(err)
			}
			if got := trackPaths(mix); !reflect.DeepEqual(got, tc.wantUndo) {
				t.Fatalf("Mix after undo = %v, want %v", got, tc.wantUndo)
			}
		})
	}
}

// shift+down on a row of a loaded playlist writes the new order in one locked
// update. A track or a tag that another writer added after the load
// survives the move, and a duration that only the queue knows fills the file.
func TestQueueMoveKeepsOtherWriters(t *testing.T) {
	a := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	b := playlist.Track{Path: "/music/b.mp3", Title: "B"}
	added := playlist.Track{Path: "/music/added.mp3", Title: "Added"}
	for _, tc := range []struct {
		name string
		// other runs after the load as a second writer of the file.
		other     func(t *testing.T, other *local.Provider)
		queue     func(m *Model) // edits the queue after the load
		wantMix   []string
		wantAlbum string // the album of a in the file after the move
		wantSecs  int    // the duration of a in the file after the move
	}{
		{name: "only this writer", wantMix: []string{b.Path, a.Path}},
		{
			name: "another writer added a track",
			other: func(t *testing.T, other *local.Provider) {
				if _, _, err := other.AddTracks("Mix", []playlist.Track{added}); err != nil {
					t.Fatal(err)
				}
			},
			wantMix: []string{b.Path, a.Path, added.Path},
		},
		{
			name: "another writer set an album",
			other: func(t *testing.T, other *local.Provider) {
				err := other.UpdatePlaylist("Mix", func(tracks []playlist.Track) ([]playlist.Track, error) {
					tracks[0].Album = "Enriched"
					return tracks, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			},
			wantMix:   []string{b.Path, a.Path},
			wantAlbum: "Enriched",
		},
		{
			name: "the queue knows a duration",
			queue: func(m *Model) {
				track, _ := m.playlist.Track(0)
				track.DurationSecs = 200
				m.playlist.SetTrack(0, track)
			},
			wantMix:  []string{b.Path, a.Path},
			wantSecs: 200,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			lp := local.New(nil, nil)
			if err := lp.SavePlaylist("Mix", []playlist.Track{a, b}); err != nil {
				t.Fatal(err)
			}
			m := Model{
				player:        &playbackFakeEngine{},
				playlist:      playlist.New(),
				vis:           ui.NewVisualizer(44100),
				provider:      lp,
				localProvider: lp,
				providers:     []provider.Entry{{Key: "local", Name: "Local", Provider: lp}},
			}
			if response := runV2(t, &m, "provider.load", ipc.Request{Provider: "local", Playlist: "Mix"}); !response.OK {
				t.Fatalf("provider.load = %+v", response)
			}
			if tc.other != nil {
				tc.other(t, local.New(nil, nil))
			}
			if tc.queue != nil {
				tc.queue(&m)
			}

			m.focus = focusPlaylist
			m.plCursor = 0
			m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
			if m.status.kind == feedbackError {
				t.Fatalf("unexpected error: %s", m.status.text)
			}
			mix, err := lp.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := trackPaths(mix); !reflect.DeepEqual(got, tc.wantMix) {
				t.Fatalf("Mix after the move = %v, want %v", got, tc.wantMix)
			}
			if got := mix[1]; got.Album != tc.wantAlbum || got.DurationSecs != tc.wantSecs {
				t.Fatalf("a in the file = album %q, duration %d; want %q, %d", got.Album, got.DurationSecs, tc.wantAlbum, tc.wantSecs)
			}
		})
	}
}

func TestOrderByRows(t *testing.T) {
	track := func(path, title string, secs int) playlist.Track {
		return playlist.Track{Path: path, Title: title, DurationSecs: secs}
	}
	for _, tc := range []struct {
		name  string
		saved []playlist.Track
		order []playlist.Track
		want  []playlist.Track
	}{
		{
			name:  "order wins",
			saved: []playlist.Track{track("/a", "A", 0), track("/b", "B", 0)},
			order: []playlist.Track{track("/b", "B", 0), track("/a", "A", 0)},
			want:  []playlist.Track{track("/b", "B", 0), track("/a", "A", 0)},
		},
		{
			name:  "saved tracks without a row go last",
			saved: []playlist.Track{track("/a", "A", 0), track("/new", "New", 0), track("/b", "B", 0)},
			order: []playlist.Track{track("/b", "B", 0), track("/a", "A", 0)},
			want:  []playlist.Track{track("/b", "B", 0), track("/a", "A", 0), track("/new", "New", 0)},
		},
		{
			name:  "rows the file lacks are left out",
			saved: []playlist.Track{track("/a", "A", 0)},
			order: []playlist.Track{track("/gone", "Gone", 0), track("/a", "A", 0)},
			want:  []playlist.Track{track("/a", "A", 0)},
		},
		{
			name:  "saved fields win",
			saved: []playlist.Track{track("/a", "Saved", 0), track("/b", "B", 90)},
			order: []playlist.Track{track("/b", "Queue", 200), track("/a", "Queue", 200)},
			want:  []playlist.Track{track("/b", "B", 90), track("/a", "Saved", 200)},
		},
		{
			name:  "one path twice keeps its rows apart",
			saved: []playlist.Track{track("/a", "A1", 0), track("/b", "B", 0), track("/a", "A2", 0)},
			order: []playlist.Track{track("/b", "B", 0), track("/a", "A1", 0), track("/a", "A2", 0)},
			want:  []playlist.Track{track("/b", "B", 0), track("/a", "A1", 0), track("/a", "A2", 0)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orderByRows(tc.saved, tc.order); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("orderByRows = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// d, ] and s in the playlist manager write the playlist in one locked update.
// A track or a tag that another writer added after the manager opened the
// playlist survives the edit. d matches a row by its path and by the count
// of earlier rows with that path.
func TestPlaylistManagerEditsKeepOtherWriters(t *testing.T) {
	a := playlist.Track{Path: "/music/a.mp3", Title: "A"}
	b := playlist.Track{Path: "/music/b.mp3", Title: "B"}
	added := playlist.Track{Path: "/music/added.mp3", Title: "Added"}
	addTrack := func(t *testing.T, other *local.Provider) {
		if _, _, err := other.AddTracks("Mix", []playlist.Track{added}); err != nil {
			t.Fatal(err)
		}
	}
	setAlbum := func(t *testing.T, other *local.Provider) {
		err := other.UpdatePlaylist("Mix", func(tracks []playlist.Track) ([]playlist.Track, error) {
			for i := range tracks {
				if tracks[i].Path == a.Path {
					tracks[i].Album = "Enriched"
				}
			}
			return tracks, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	remove := func(m *Model) { m.plManager.cursor = 0; m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "d"}) }
	move := func(m *Model) { m.plManager.cursor = 0; m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "]"}) }
	sortTitle := func(m *Model) { m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "s"}) }
	for _, tc := range []struct {
		name      string
		file      []playlist.Track
		other     func(t *testing.T, other *local.Provider)
		edit      func(m *Model)
		wantMix   []string
		wantTitle []string
		wantAlbum string // the album of a in the file after the edit
	}{
		{name: "remove", file: []playlist.Track{b, a}, edit: remove, wantMix: []string{a.Path}},
		{name: "remove after an add", file: []playlist.Track{b, a}, other: addTrack, edit: remove, wantMix: []string{a.Path, added.Path}},
		{name: "remove after an album", file: []playlist.Track{b, a}, other: setAlbum, edit: remove, wantMix: []string{a.Path}, wantAlbum: "Enriched"},
		{
			name:      "remove the second row of one path",
			file:      []playlist.Track{{Path: a.Path, Title: "A1"}, b, {Path: a.Path, Title: "A2"}},
			edit:      func(m *Model) { m.plManager.cursor = 2; m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "d"}) },
			wantMix:   []string{a.Path, b.Path},
			wantTitle: []string{"A1", "B"},
		},
		{name: "move", file: []playlist.Track{b, a}, edit: move, wantMix: []string{a.Path, b.Path}},
		{name: "move after an add", file: []playlist.Track{b, a}, other: addTrack, edit: move, wantMix: []string{a.Path, b.Path, added.Path}},
		{name: "move after an album", file: []playlist.Track{b, a}, other: setAlbum, edit: move, wantMix: []string{a.Path, b.Path}, wantAlbum: "Enriched"},
		{name: "sort", file: []playlist.Track{b, a}, edit: sortTitle, wantMix: []string{a.Path, b.Path}},
		{name: "sort after an add", file: []playlist.Track{b, a}, other: addTrack, edit: sortTitle, wantMix: []string{a.Path, b.Path, added.Path}},
		{name: "sort after an album", file: []playlist.Track{b, a}, other: setAlbum, edit: sortTitle, wantMix: []string{a.Path, b.Path}, wantAlbum: "Enriched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			lp := local.New(nil, nil)
			if err := lp.SavePlaylist("Mix", tc.file); err != nil {
				t.Fatal(err)
			}
			tracks, err := lp.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			m := Model{
				player:        &playbackFakeEngine{},
				playlist:      playlist.New(),
				vis:           ui.NewVisualizer(44100),
				provider:      lp,
				localProvider: lp,
				plManager: plManagerState{
					visible:     true,
					screen:      plMgrScreenTracks,
					selPlaylist: "Mix",
				},
			}
			m.plMgrLoadTracks(tracks)
			if tc.other != nil {
				tc.other(t, local.New(nil, nil))
			}

			tc.edit(&m)
			if m.status.kind == feedbackError || m.status.kind == feedbackWarning {
				t.Fatalf("unexpected status: %s", m.status.text)
			}
			mix, err := lp.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := trackPaths(mix); !reflect.DeepEqual(got, tc.wantMix) {
				t.Fatalf("Mix after the edit = %v, want %v", got, tc.wantMix)
			}
			if tc.wantTitle != nil {
				var titles []string
				for _, track := range mix {
					titles = append(titles, track.Title)
				}
				if !reflect.DeepEqual(titles, tc.wantTitle) {
					t.Fatalf("Mix titles = %v, want %v", titles, tc.wantTitle)
				}
			}
			for _, track := range mix {
				if track.Path == a.Path && track.Album != tc.wantAlbum {
					t.Fatalf("album of a = %q, want %q", track.Album, tc.wantAlbum)
				}
			}
		})
	}
}

// Favorites can hold a radio station, because f on a station row in a saved
// playlist adds it there. A key load of Favorites keeps the list as the saved
// list of the ♥ rule, so f on that row removes it from Favorites and does not
// toggle the station favorite. Favorites is still no playlist file.
func TestStationRowInFavoritesTogglesTrackFavorite(t *testing.T) {
	m, _, tracks := radioFavoriteTestModel(t)
	station := tracks[0]
	favs := favorites.New()
	lp := local.New(favs, nil)
	if _, err := favs.Toggle(station); err != nil {
		t.Fatal(err)
	}
	m.provider, m.localProvider, m.favStore = lp, lp, favs
	m.refreshFavSet()

	m.requests.tracks = 1
	updated, _ := m.Update(fetchTracksCmd(lp, favorites.PlaylistName, 1)())
	m = updated.(Model)
	row, ok := m.playlist.Track(0)
	if !ok || row.Path != station.Path || !row.Realtime || row.Meta("radio.name") == "" {
		t.Fatalf("Favorites row = %+v, want the station with its radio metadata", row)
	}
	if m.loadedPlaylist != favorites.PlaylistName || m.writableLoadedPlaylist() != "" {
		t.Fatalf("loaded %q, writable %q; want Favorites as the saved list and no writable name", m.loadedPlaylist, m.writableLoadedPlaylist())
	}
	if !m.playlistTrackFavorited(row) {
		t.Fatal("the station row in Favorites shows no ♥")
	}

	m.focus = focusPlaylist
	m.plCursor = 0
	m.handleKey(tea.KeyPressMsg{Text: "f"})
	if favs.IsFavorited(station.Path) {
		t.Fatal("f did not remove the station from Favorites")
	}
	if m.radioFavorites.Count() != 0 {
		t.Fatal("f toggled the station favorite for a row in Favorites")
	}
	if m.playlistTrackFavorited(row) {
		t.Fatal("the row still shows a ♥ after f")
	}
}

// The runtime snapshot names the loaded local list, or else the provider
// list of the last IPC load as key:id. cliamp status --json shows it. A
// queue change that drops the loaded list drops the name too.
func TestV2SnapshotNamesTheLoadedList(t *testing.T) {
	type step struct {
		op     string
		params ipc.Request
	}
	loadRemote := step{"provider.load", ipc.Request{Provider: "navidrome", Playlist: "42"}}
	loadMix := step{"load", ipc.Request{Playlist: "Mix"}}
	for _, tc := range []struct {
		name  string
		steps []step
		want  string
	}{
		{name: "load", steps: []step{loadMix}, want: "Mix"},
		{name: "local playlist", steps: []step{{"provider.load", ipc.Request{Provider: "local", Playlist: "Mix"}}}, want: "Mix"},
		{name: "local history", steps: []step{{"provider.load", ipc.Request{Provider: "local", Playlist: history.PlaylistName}}}, want: "local:" + history.PlaylistName},
		{name: "remote playlist", steps: []step{loadRemote}, want: "navidrome:42"},
		{name: "remote album", steps: []step{{"provider.load_album", ipc.Request{Provider: "navidrome", Album: "7"}}}, want: "navidrome:album:7"},
		{name: "remote then local", steps: []step{loadRemote, loadMix}, want: "Mix"},
		{name: "local then remote", steps: []step{loadMix, loadRemote}, want: "navidrome:42"},
		{name: "remote then queue", steps: []step{loadRemote, {"queue", ipc.Request{Path: "/music/c.mp3"}}}},
		{name: "remote then clear", steps: []step{loadRemote, {"queue.clear", ipc.Request{}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracks := []playlist.Track{{Path: "/music/a.mp3", Title: "A"}, {Path: "/music/b.mp3", Title: "B"}}
			local := fixedTracksProvider{commandsTestProvider{name: "Local"}, tracks}
			navidrome := remoteListProvider{commandsTestProvider{name: "Navidrome"}, tracks}
			m := Model{
				player:        &playbackFakeEngine{},
				playlist:      playlist.New(),
				vis:           ui.NewVisualizer(44100),
				localProvider: local,
				providers: []provider.Entry{
					{Key: "local", Name: "Local", Provider: local},
					{Key: "navidrome", Name: "Navidrome", Provider: navidrome},
				},
			}

			for _, s := range tc.steps {
				if response := runV2(t, &m, s.op, s.params); !response.OK {
					t.Fatalf("%s response = %+v, want OK", s.op, response)
				}
			}
			if got := m.runtimeSnapshot().Playlist; got != tc.want {
				t.Fatalf("snapshot playlist = %q, want %q", got, tc.want)
			}
			if got := m.runtimeFingerprint().playlist; got != tc.want {
				t.Fatalf("fingerprint playlist = %q, want %q", got, tc.want)
			}
		})
	}
}

// A rename of the loaded playlist, by the manager key or by IPC, moves the
// loaded list to the new name. Ctrl+Z and later queue edits then write to
// the renamed file and create no file under the old name.
func TestRenameMovesTheLoadedPlaylist(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rename func(t *testing.T, m *Model)
	}{
		{name: "manager key", rename: func(t *testing.T, m *Model) {
			m.plManager.renameOldName, m.plManager.renameName = "Mix", "New"
			if !m.plMgrCommitRename() {
				t.Fatalf("rename failed: %s", m.plManager.inputErr)
			}
		}},
		{name: "IPC", rename: func(t *testing.T, m *Model) {
			if response := runV2(t, m, "playlist.rename", ipc.Request{Provider: "local", Playlist: "Mix", NewName: "New"}); !response.OK {
				t.Fatalf("playlist.rename = %+v", response)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, lp, _ := queueOpModel(t, false, "Mix", 1)
			next, _ := m.Update(tea.KeyPressMsg{Text: "x"})
			m = next.(Model)
			tc.rename(t, &m)
			if m.loadedPlaylist != "New" {
				t.Fatalf("loadedPlaylist = %q, want New", m.loadedPlaylist)
			}
			next, _ = m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			m = next.(Model)
			next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
			m = next.(Model)
			if m.status.kind == feedbackError {
				t.Fatalf("unexpected error: %s", m.status.text)
			}
			if got := queueOpPaths(m.playlist.Tracks()); got != "a c b" {
				t.Fatalf("queue = %q, want a c b", got)
			}
			saved, err := lp.Tracks("New")
			if err != nil {
				t.Fatal(err)
			}
			if got := queueOpPaths(saved); got != "a c b" {
				t.Fatalf("New = %q, want a c b", got)
			}
			if got := dirFiles(t, filepath.Join(os.Getenv("CLIAMP_CONFIG_DIR"), "playlists")); !reflect.DeepEqual(got, []string{"New.toml"}) {
				t.Fatalf("playlist files = %v, want only New.toml", got)
			}
		})
	}
}

// A load from the playlist manager marks the loaded list as the provider
// pane and IPC provider.load do. Recently Played is no saved list, so the ♥
// rule and the snapshot do not read it as one.
func TestPlaylistManagerLoadMarksTheLoadedList(t *testing.T) {
	for _, tc := range []struct {
		list, want string
	}{
		{list: "Mix", want: "Mix"},
		{list: favorites.PlaylistName, want: favorites.PlaylistName},
		{list: history.PlaylistName},
	} {
		t.Run(tc.list, func(t *testing.T) {
			m, _, _ := queueOpModel(t, false, "Other", 0)
			m.plManager = plManagerState{visible: true, selPlaylist: tc.list, tracks: []playlist.Track{{Path: "/music/b.mp3"}}}
			m.plMgrLoadAndPlay(0)
			if m.loadedPlaylist != tc.want {
				t.Fatalf("loadedPlaylist = %q, want %q", m.loadedPlaylist, tc.want)
			}
			if got := m.runtimeSnapshot().Playlist; got != tc.want {
				t.Fatalf("snapshot playlist = %q, want %q", got, tc.want)
			}
		})
	}
}
