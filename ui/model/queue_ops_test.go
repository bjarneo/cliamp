package model

import (
	"cmp"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// queueOpState is the part of the Model that a queue edit changes.
type queueOpState struct {
	queue, saved   string // the paths of the queue and of the saved Mix
	cursor, index  int
	loaded         string
	headers        bool
	headerSegments int
	// undo is true when the edit records a Ctrl+Z undo, and undoHint when
	// the status line offers it.
	undo, undoHint bool
	stops          int
	// preload is true when an armed or loading preload holds the track that
	// plays next.
	preload bool
}

// A queue edit follows one rule, whether a key, IPC or a Lua plugin starts
// it. Each row runs one edit through a key press, runV2 and PluginQueueMsg
// and expects the same end state. Only the x key records an undo. A remote
// or plugin removal records none.
func TestQueueEditsFollowOneRule(t *testing.T) {
	d := playlist.Track{Path: "/music/d.mp3"}
	for _, tc := range []struct {
		name    string
		shuffle bool
		// loaded is the list that the queue mirrors. It is Mix when empty.
		loaded string
		cursor int
		setup  func(m *Model)
		// key, v2 and plugin run the edit from each entry point.
		key    func(m *Model) tea.Msg
		v2Op   string
		v2     ipc.Request
		plugin PluginQueueMsg
		// wantV2Err is the start of the error of the V2 job, or "" when it
		// succeeds.
		wantV2Err string
		want      func(s queueOpState) bool
	}{
		{
			name:   "move",
			cursor: 1,
			key:    func(*Model) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift} },
			v2Op:   "queue.move", v2: ipc.Request{Index: 1, To: 2},
			plugin: PluginQueueMsg{Op: "move", Index: 1, To: 2},
			want: func(s queueOpState) bool {
				return s.queue == "a c b" && s.saved == "a c b" && s.cursor == 2 && s.loaded == "Mix" && s.headerSegments == 3 && s.preload
			},
		},
		{
			name:    "move under shuffle",
			shuffle: true,
			cursor:  1,
			key:     func(*Model) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift} },
			v2Op:    "queue.move", v2: ipc.Request{Index: 1, To: 2},
			plugin:    PluginQueueMsg{Op: "move", Index: 1, To: 2},
			wantV2Err: ipc.V2ErrorCodeConflict + ": " + errQueueShuffled.Error(),
			want: func(s queueOpState) bool {
				return s.queue == "a b c" && s.saved == "a b c" && s.cursor == 1 && s.preload
			},
		},
		{
			// The file is gone, so the save fails and the queue keeps its
			// order, as a failed removal does.
			name:   "move when the save fails",
			cursor: 1,
			setup: func(m *Model) {
				// want checks that Mix is gone.
				_ = m.localProvider.(*local.Provider).DeletePlaylist("Mix")
			},
			key:  func(*Model) tea.Msg { return tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift} },
			v2Op: "queue.move", v2: ipc.Request{Index: 1, To: 2},
			plugin:    PluginQueueMsg{Op: "move", Index: 1, To: 2},
			wantV2Err: ipc.V2ErrorCodeInternal + ": ",
			want: func(s queueOpState) bool {
				return s.queue == "a b c" && s.saved == "" && s.cursor == 1 && s.loaded == "Mix" && s.headerSegments == 2 && s.preload
			},
		},
		{
			name:   "remove",
			cursor: 1,
			key:    func(*Model) tea.Msg { return tea.KeyPressMsg{Text: "x"} },
			v2Op:   "queue.remove", v2: ipc.Request{Index: 1},
			plugin: PluginQueueMsg{Op: "remove", Index: 1},
			want: func(s queueOpState) bool {
				return s.queue == "a c" && s.saved == "a c" && s.cursor == 1 && s.loaded == "Mix" && s.undo && s.undoHint && s.headerSegments == 2 && s.preload
			},
		},
		{
			name:   "remove a track of a directory source",
			cursor: 1,
			setup: func(m *Model) {
				track, _ := m.playlist.Track(1)
				track.DirSourced = true
				m.playlist.SetTrack(1, track)
			},
			key:  func(*Model) tea.Msg { return tea.KeyPressMsg{Text: "x"} },
			v2Op: "queue.remove", v2: ipc.Request{Index: 1},
			plugin:    PluginQueueMsg{Op: "remove", Index: 1},
			wantV2Err: ipc.V2ErrorCodeConflict + ": " + errQueueDirTrack.Error(),
			want: func(s queueOpState) bool {
				return s.queue == "a b c" && s.saved == "a b c" && s.loaded == "Mix" && !s.undo && s.preload
			},
		},
		{
			// After an append the queue mirrors no playlist, so no file
			// keeps the track and the row goes.
			name:   "remove a track of a directory source from an unsaved queue",
			cursor: 1,
			setup: func(m *Model) {
				track, _ := m.playlist.Track(1)
				track.DirSourced = true
				m.playlist.SetTrack(1, track)
				m.appendTracks(d)
			},
			key:  func(*Model) tea.Msg { return tea.KeyPressMsg{Text: "x"} },
			v2Op: "queue.remove", v2: ipc.Request{Index: 1},
			plugin: PluginQueueMsg{Op: "remove", Index: 1},
			want: func(s queueOpState) bool {
				return s.queue == "a c d" && s.saved == "a b c" && s.cursor == 1 && s.loaded == "" && s.undo && s.undoHint && s.preload
			},
		},
		{
			// Favorites is not a playlist file, so only the queue changes.
			name:   "remove from a loaded Favorites",
			loaded: favorites.PlaylistName,
			cursor: 1,
			key:    func(*Model) tea.Msg { return tea.KeyPressMsg{Text: "x"} },
			v2Op:   "queue.remove", v2: ipc.Request{Index: 1},
			plugin: PluginQueueMsg{Op: "remove", Index: 1},
			want: func(s queueOpState) bool {
				return s.queue == "a c" && s.saved == "a b c" && s.loaded == favorites.PlaylistName && s.undo && s.undoHint && s.preload
			},
		},
		{
			name:   "remove the playing track",
			cursor: 0,
			key:    func(*Model) tea.Msg { return tea.KeyPressMsg{Text: "x"} },
			v2Op:   "queue.remove", v2: ipc.Request{Index: 0},
			plugin: PluginQueueMsg{Op: "remove", Index: 0},
			want: func(s queueOpState) bool {
				return s.queue == "b c" && s.saved == "b c" && s.stops == 1 && s.undo && s.undoHint && !s.preload
			},
		},
		{
			// A provider list replaced the queue while a.mp3 plays, so a.mp3
			// plays detached from the new list. Its selected row is not the
			// playing track, so the removal does not stop playback.
			name:   "remove the selected row while a detached track plays",
			cursor: 0,
			setup: func(m *Model) {
				m.replacePlayerPlaylist([]playlist.Track{{Path: "/music/p.mp3"}, {Path: "/music/q.mp3"}})
			},
			key:  func(*Model) tea.Msg { return tea.KeyPressMsg{Text: "x"} },
			v2Op: "queue.remove", v2: ipc.Request{Index: 0},
			plugin: PluginQueueMsg{Op: "remove", Index: 0},
			want: func(s queueOpState) bool {
				return s.queue == "q" && s.saved == "a b c" && s.loaded == "" && s.stops == 0 && s.undo && s.undoHint && s.preload
			},
		},
		{
			name:   "append",
			cursor: 1,
			key: func(m *Model) tea.Msg {
				m.plManager = plManagerState{visible: true, screen: plMgrScreenTracks, selPlaylist: "Other", tracks: []playlist.Track{d}}
				return tea.KeyPressMsg{Text: "A"}
			},
			v2Op: "queue", v2: ipc.Request{Path: d.Path},
			plugin: PluginQueueMsg{Op: "add_track", Track: d},
			want: func(s queueOpState) bool {
				return s.queue == "a b c d" && s.saved == "a b c" && s.cursor == 1 && s.loaded == "" && s.headerSegments == 3 && s.preload
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			states := map[string]queueOpState{}
			for _, entry := range []string{"key", "V2", "plugin"} {
				m, lp, engine := queueOpModel(t, tc.shuffle, cmp.Or(tc.loaded, "Mix"), tc.cursor)
				if tc.setup != nil {
					tc.setup(&m)
				}
				switch entry {
				case "key":
					msg := tc.key(&m)
					next, _ := m.Update(msg)
					m = next.(Model)
				case "V2":
					response := runV2(t, &m, tc.v2Op, tc.v2)
					if response.OK != (tc.wantV2Err == "") || !strings.HasPrefix(response.Error, tc.wantV2Err) {
						t.Fatalf("V2 %s = %+v, want error %q", tc.v2Op, response, tc.wantV2Err)
					}
				case "plugin":
					next, _ := m.Update(tc.plugin)
					m = next.(Model)
				}
				states[entry] = queueOpStateOf(t, m, lp, engine)
			}
			want := states["key"]
			if !tc.want(want) {
				t.Fatalf("key state = %+v, want the edit done by the rule", want)
			}
			for entry, got := range states {
				wantEntry := want
				if entry != "key" {
					wantEntry.undo, wantEntry.undoHint = false, false
				}
				if got != wantEntry {
					t.Errorf("%s: state = %+v, want %+v", entry, got, wantEntry)
				}
			}
		})
	}
}

// queueOpModel returns a Model whose queue holds the tracks of the saved
// playlist Mix of a real local provider and mirrors the list loaded. It
// plays a.mp3, and the preload holds the next track.
func queueOpModel(t *testing.T, shuffle bool, loaded string, cursor int) (Model, *local.Provider, *playbackFakeEngine) {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	tracks := []playlist.Track{
		{Path: "/music/a.mp3", Title: "A", Album: "X", DurationSecs: 180},
		{Path: "/music/b.mp3", Title: "B", Album: "X", DurationSecs: 180},
		{Path: "/music/c.mp3", Title: "C", Album: "Y", DurationSecs: 180},
	}
	lp := local.New(nil, nil)
	if err := lp.SavePlaylist("Mix", tracks); err != nil {
		t.Fatal(err)
	}
	engine := &playbackFakeEngine{playing: true, duration: 180 * time.Second, position: 179 * time.Second, hasPreload: true}
	m := Model{
		player:        engine,
		playlist:      playlist.New(),
		vis:           ui.NewVisualizer(44100),
		focus:         focusPlaylist,
		provider:      lp,
		localProvider: lp,
		providers:     []provider.Entry{{Key: "local", Name: "Local", Provider: lp}},
	}
	m.playlist.Replace(tracks)
	m.playlist.SetIndex(0)
	if shuffle {
		m.playlist.ToggleShuffle()
	}
	m.SetLoadedPlaylist(loaded)
	m.setHeaderStateFromTracks(tracks)
	m.playingTrack, m.playingTrackActive, m.playingTrackStarted = tracks[0], true, true
	next, _ := m.playlist.PeekNext()
	m.preloadFor = next.Path
	m.plCursor = cursor
	return m, lp, engine
}

func queueOpStateOf(t *testing.T, m Model, lp *local.Provider, engine *playbackFakeEngine) queueOpState {
	t.Helper()
	// A deleted Mix reads as no tracks.
	saved, err := lp.Tracks("Mix")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	state := queueOpState{
		queue:          queueOpPaths(m.playlist.Tracks()),
		saved:          queueOpPaths(saved),
		cursor:         m.plCursor,
		index:          m.playlist.Index(),
		loaded:         m.loadedPlaylist,
		headers:        m.showAlbumHeaders,
		headerSegments: m.headerSegments,
		undo:           m.playlistUndo.active,
		undoHint:       strings.Contains(m.status.text, "Ctrl+Z"),
		stops:          engine.stopCalls,
	}
	if next, ok := m.preloadTarget(); ok && (m.preloading || engine.hasPreload) {
		state.preload = m.preloadFor == next.Path
	}
	return state
}

// queueOpPaths returns the base names of the track paths without the .mp3
// extension, joined by spaces.
func queueOpPaths(tracks []playlist.Track) string {
	names := make([]string, len(tracks))
	for i, track := range tracks {
		names[i] = strings.TrimSuffix(strings.TrimPrefix(track.Path, "/music/"), ".mp3")
	}
	return strings.Join(names, " ")
}

// Ctrl+Z restores the queue from before the last x only while the queue and
// the loaded playlist stay as that x left them. After a later edit, a load
// or a remote removal, the undo is refused and changes nothing.
func TestPlaylistUndoRestoresOnlyTheLastEdit(t *testing.T) {
	d := playlist.Track{Path: "/music/d.mp3"}
	for _, tc := range []struct {
		name string
		// between runs after the x on b and before Ctrl+Z.
		between func(t *testing.T, m *Model, lp *local.Provider)
		refused bool
		// keepsUndo is true when a failed undo stays for a later Ctrl+Z.
		keepsUndo bool
		want      func(s queueOpState) bool
	}{
		{
			name: "right after the edit",
			want: func(s queueOpState) bool {
				return s.queue == "a b c" && s.saved == "a b c" && s.loaded == "Mix"
			},
		},
		{
			// The undo puts back only the removed track, so a track that
			// another writer added to the file is kept.
			name: "after another writer added to the file",
			between: func(t *testing.T, _ *Model, _ *local.Provider) {
				if err := local.New(nil, nil).AddTrack("Mix", d); err != nil {
					t.Fatal(err)
				}
			},
			want: func(s queueOpState) bool {
				return s.queue == "a b c" && s.saved == "a b c d" && s.loaded == "Mix"
			},
		},
		{
			name: "after the file was deleted",
			between: func(t *testing.T, _ *Model, lp *local.Provider) {
				if err := lp.DeletePlaylist("Mix"); err != nil {
					t.Fatal(err)
				}
			},
			refused:   true,
			keepsUndo: true,
			want: func(s queueOpState) bool {
				return s.queue == "a c" && s.saved == "" && s.loaded == "Mix"
			},
		},
		{
			name: "after a Lua append",
			between: func(t *testing.T, m *Model, _ *local.Provider) {
				next, _ := m.Update(PluginQueueMsg{Op: "add_track", Track: d})
				*m = next.(Model)
			},
			refused: true,
			want: func(s queueOpState) bool {
				return s.queue == "a c d" && s.saved == "a c" && s.loaded == ""
			},
		},
		{
			name: "after an IPC removal",
			between: func(t *testing.T, m *Model, _ *local.Provider) {
				if response := runV2(t, m, "queue.remove", ipc.Request{Index: 1}); !response.OK {
					t.Fatalf("queue.remove = %+v", response)
				}
			},
			refused: true,
			want: func(s queueOpState) bool {
				return s.queue == "a" && s.saved == "a" && s.loaded == "Mix"
			},
		},
		{
			name: "after an IPC move",
			between: func(t *testing.T, m *Model, _ *local.Provider) {
				if response := runV2(t, m, "queue.move", ipc.Request{Index: 0, To: 1}); !response.OK {
					t.Fatalf("queue.move = %+v", response)
				}
			},
			refused: true,
			want: func(s queueOpState) bool {
				return s.queue == "c a" && s.saved == "c a" && s.loaded == "Mix"
			},
		},
		{
			name: "after a load of another playlist",
			between: func(t *testing.T, m *Model, lp *local.Provider) {
				other := []playlist.Track{{Path: "/music/b.mp3"}, {Path: "/music/q.mp3"}}
				if err := lp.SavePlaylist("Other", other); err != nil {
					t.Fatal(err)
				}
				m.plManager = plManagerState{selPlaylist: "Other", tracks: other}
				m.plMgrLoadAndPlay(0)
			},
			refused: true,
			want: func(s queueOpState) bool {
				return s.queue == "b q" && s.saved == "a c" && s.loaded == "Other"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, lp, engine := queueOpModel(t, false, "Mix", 1)
			next, _ := m.Update(tea.KeyPressMsg{Text: "x"})
			m = next.(Model)
			if !m.playlistUndo.active {
				t.Fatal("x recorded no undo")
			}
			if tc.between != nil {
				tc.between(t, &m, lp)
			}
			next, _ = m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			m = next.(Model)
			got := queueOpStateOf(t, m, lp, engine)
			if got.undo != tc.keepsUndo || !tc.want(got) {
				t.Fatalf("state after Ctrl+Z = %+v", got)
			}
			if restored := strings.HasPrefix(m.status.text, "Restored"); restored == tc.refused {
				t.Fatalf("status = %q, want refused %v", m.status.text, tc.refused)
			}
			if tc.refused {
				other, err := lp.Tracks("Other")
				if err == nil && queueOpPaths(other) != "b q" {
					t.Fatalf("Other = %q, want b q", queueOpPaths(other))
				}
			}
		})
	}
}
