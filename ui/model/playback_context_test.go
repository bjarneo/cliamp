package model

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

type savedPlaybackContext struct {
	track    playlist.Track
	tracks   []playlist.Track
	index    int
	position int
}

func (s *savedPlaybackContext) save(track playlist.Track, position int, tracks []playlist.Track, index int) {
	s.track, s.position, s.tracks, s.index = track, position, tracks, index
}

func (s savedPlaybackContext) check(t *testing.T, tracks []playlist.Track, index int) {
	t.Helper()
	if s.index != index || s.track.Path != tracks[index].Path || !reflect.DeepEqual(s.tracks, tracks) {
		t.Fatalf("saved track %q, index %d, context %+v; want index %d in %+v", s.track.Path, s.index, s.tracks, index, tracks)
	}
}

func advancePlaybackContext(m Model, engine *playbackFakeEngine, gapless bool) Model {
	if gapless {
		engine.gaplessAdvanced = true
		updated, _ := m.Update(tickMsg(time.Now()))
		return updated.(Model)
	}
	m.nextTrack()
	return m
}

func TestPlaybackContextOverlappingReplacement(t *testing.T) {
	for _, gapless := range []bool{false, true} {
		t.Run(fmt.Sprintf("gapless=%t", gapless), func(t *testing.T) {
			old := []playlist.Track{{Path: "a.mp3"}, {Path: "b.mp3"}}
			pl := playlist.New()
			pl.Add(old...)
			engine := &playbackFakeEngine{position: 19 * time.Second}
			m := Model{player: engine, playlist: pl, provider: commandsTestProvider{name: "Test"}, vis: ui.NewVisualizer(44100)}
			m.SetVisualizer("none")
			var saved savedPlaybackContext
			m.SetResumeSaver(saved.save)
			m.playCurrentTrack()
			saved.check(t, old, 0)

			// Even an entry carrying the old source must get fresh replacement provenance.
			replacement := []playlist.Track{pl.Tracks()[1], {Path: "c.mp3"}}
			updated, _ := m.Update(tracksLoadedMsg{tracks: replacement, providerName: "Test"})
			m = updated.(Model)
			if !m.playbackDetached {
				t.Fatal("replacement did not detach the playing track")
			}
			m.cachedPos = engine.position
			m.tickResumeSave(m.lastResumeSave.Add(resumeSaveInterval))
			saved.check(t, old, 0)
			detached := m
			detached.quit()
			if context, index := detached.ResumeContext(); index != 0 || !reflect.DeepEqual(context, old) {
				t.Fatalf("detached quit context = (%+v, %d), want old source", context, index)
			}

			m = advancePlaybackContext(m, engine, gapless)
			if m.playbackDetached || m.playingTrack.Path != "b.mp3" {
				t.Fatalf("replacement activation = detached:%t track:%q", m.playbackDetached, m.playingTrack.Path)
			}
			saved.check(t, []playlist.Track{old[1], {Path: "c.mp3"}}, 0)
		})
	}
}

func TestBrowserPlaybackContextSurvivesQueuedAlbum(t *testing.T) {
	for _, gapless := range []bool{false, true} {
		t.Run(fmt.Sprintf("gapless=%t", gapless), func(t *testing.T) {
			album := []playlist.Track{{Path: "a.mp3"}, {Path: "b.mp3"}, {Path: "c.mp3"}}
			otherAlbum := []playlist.Track{{Path: "w.mp3"}, {Path: "x.mp3"}, {Path: "y.mp3"}}
			engine := &playbackFakeEngine{}
			m := Model{
				player: engine, playlist: playlist.New(), vis: ui.NewVisualizer(44100),
				navBrowser: navBrowserState{
					prov: commandsTestProvider{name: "Test"}, visible: true,
					mode: navBrowseModeByAlbum, screen: navBrowseScreenTracks, tracks: album, cursor: 1,
				},
			}
			m.SetVisualizer("none")
			var saved savedPlaybackContext
			m.SetResumeSaver(saved.save)
			m.handleNavBrowserKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			saved.check(t, album, 1)
			if tracks := m.playlist.Tracks(); len(tracks) != 2 || tracks[0].Path != "b.mp3" || tracks[1].Path != "c.mp3" {
				t.Fatalf("live list = %+v, want [B C]", tracks)
			}

			m.navBrowser.tracks = otherAlbum
			m.handleNavBrowserKey(tea.KeyPressMsg{Text: "q"})
			if m.playlist.QueueLen() != 1 {
				t.Fatal("browser q did not queue the interlude")
			}
			saved.check(t, album, 1)
			m = advancePlaybackContext(m, engine, gapless)
			saved.check(t, otherAlbum, 1)
			m = advancePlaybackContext(m, engine, gapless)
			saved.check(t, album, 2)
			if m.playingTrack.Path != "c.mp3" {
				t.Fatalf("playing %q after interlude, want C", m.playingTrack.Path)
			}
		})
	}
}

func TestNavPlaybackContextUsesDisplayedList(t *testing.T) {
	for _, tc := range []struct {
		key   string
		index int
		count int
	}{
		{key: "enter", index: 1, count: 2},
		{key: "q", index: 1, count: 1},
		{key: "a", index: 0, count: 3},
		{key: "R", index: 0, count: 3},
	} {
		t.Run(tc.key, func(t *testing.T) {
			tracks := []playlist.Track{{Path: "a.mp3"}, {Path: "b.mp3"}, {Path: "a.mp3"}, {Path: "c.mp3"}}
			m := Model{
				player: &playbackFakeEngine{}, playlist: playlist.New(), vis: ui.NewVisualizer(44100),
				navBrowser: navBrowserState{tracks: tracks, search: "filtered", searchIdx: []int{1, 2, 3}, cursor: 1},
			}
			var saved savedPlaybackContext
			m.SetResumeSaver(saved.save)
			key := tea.KeyPressMsg{Text: tc.key}
			if tc.key == "enter" {
				key = tea.KeyPressMsg{Code: tea.KeyEnter}
			}
			m.handleNavTrackListKey(key)
			saved.check(t, tracks[1:], tc.index)
			if got := m.playlist.Len(); got != tc.count {
				t.Fatalf("live list length = %d, want %d", got, tc.count)
			}
		})
	}
}

func TestNavPlaybackContextPrecedesEnqueueLimit(t *testing.T) {
	tracks := make([]playlist.Track, 503)
	for i := range tracks {
		tracks[i].Path = fmt.Sprintf("track-%d.mp3", i)
	}
	m := Model{
		player: &playbackFakeEngine{}, playlist: playlist.New(), vis: ui.NewVisualizer(44100),
		navBrowser: navBrowserState{tracks: tracks, cursor: 1},
	}
	var saved savedPlaybackContext
	m.SetResumeSaver(saved.save)
	m.handleNavTrackListKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	saved.check(t, tracks, 1)
	if got := m.playlist.Len(); got != 500 {
		t.Fatalf("live list length = %d, want 500", got)
	}
}

func TestDuplicatePlaybackContextPersistenceAndRestore(t *testing.T) {
	for _, attached := range []bool{false, true} {
		t.Run(fmt.Sprintf("attached=%t", attached), func(t *testing.T) {
			tracks := []playlist.Track{{Path: "a.mp3"}, {Path: "b.mp3"}, {Path: "a.mp3"}, {Path: "c.mp3"}}
			pl := playlist.New()
			engine := &playbackFakeEngine{}
			m := Model{player: engine, playlist: pl, vis: ui.NewVisualizer(44100)}
			var saved savedPlaybackContext
			if attached {
				pl.Add(tracks...)
			}
			m.SetResumeSaver(saved.save)
			if !attached {
				pl.Add(tracks...)
			}
			pl.SetIndex(2)
			m.playCurrentTrack()
			saved.check(t, tracks, 2)

			// Moving the selection to the other A must not change the active entry.
			pl.SetIndex(0)
			engine.position = 17 * time.Second
			m.cachedPos = engine.position
			m.tickResumeSave(m.lastResumeSave.Add(resumeSaveInterval))
			saved.check(t, tracks, 2)
			if saved.position != 17 {
				t.Fatalf("periodic save position = %d, want 17", saved.position)
			}
			m.quit()
			context, index := m.ResumeContext()
			if index != 2 || !reflect.DeepEqual(context, tracks) {
				t.Fatalf("quit context = (%+v, %d), want duplicate at index 2", context, index)
			}

			data, err := json.Marshal(context)
			if err != nil {
				t.Fatal(err)
			}
			var restored []playlist.Track
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			pl = playlist.New()
			pl.Add(restored...)
			engine = &playbackFakeEngine{}
			m = Model{player: engine, playlist: pl, vis: ui.NewVisualizer(44100)}
			m.SetResumeSaver(saved.save)
			m.SetInitialTrack(index)
			m.SetResume(tracks[index].Path, 17)
			if pl.Index() != 2 || len(engine.playCalls) != 0 {
				t.Fatal("restore did not select the duplicate without playback")
			}
			m.playCurrentTrack()
			saved.check(t, tracks, 2)
			if saved.position != 17 {
				t.Fatalf("restored save position = %d, want 17", saved.position)
			}
			m.nextTrack()
			saved.check(t, tracks, 3)
		})
	}
}

func TestSetResumeSaverPreservesPlaylistState(t *testing.T) {
	pl := playlist.New()
	pl.Add(playlist.Track{Path: "a"}, playlist.Track{Path: "b"}, playlist.Track{Path: "c"}, playlist.Track{Path: "d"})
	pl.SetIndex(2)
	pl.ToggleShuffle()
	pl.SetRepeat(playlist.RepeatAll)
	pl.Queue(3)
	pl.Queue(1)
	pl.Next()
	want := playlist.New()
	want.Restore(pl.Snapshot())
	m := Model{playlist: pl}
	m.SetResumeSaver(func(playlist.Track, int, []playlist.Track, int) {})
	if pl.Index() != want.Index() || !pl.CurrentIsQueued() || !pl.Shuffled() || pl.Repeat() != playlist.RepeatAll || pl.QueueLen() != 1 {
		t.Fatal("enabling context tracking changed playback state")
	}
	for range 4 {
		gotTrack, gotOK := pl.Next()
		wantTrack, wantOK := want.Next()
		if gotOK != wantOK || gotTrack.Path != wantTrack.Path || pl.Index() != want.Index() {
			t.Fatalf("playback order changed: got %q, want %q", gotTrack.Path, wantTrack.Path)
		}
		if context, index := gotTrack.PlaybackContext(); len(context) != 4 || index != pl.Index() {
			t.Fatalf("initial track context = (%+v, %d), want original entry", context, index)
		}
	}
}

func TestPlaybackContextSelectionWithoutSaver(t *testing.T) {
	tracks := []playlist.Track{{Path: "a.mp3"}, {Path: "b.mp3"}, {Path: "a.mp3"}}
	pl := playlist.New()
	pl.Add(tracks...)
	m := Model{player: &playbackFakeEngine{position: time.Second}, playlist: pl}
	m.SetInitialTrack(2)
	pl.SetIndex(0)
	m.playCurrentTrack()
	m.quit()
	if context, index := m.ResumeContext(); index != 0 || !reflect.DeepEqual(context, tracks) {
		t.Fatalf("quit context = (%+v, %d), want active duplicate at index 0", context, index)
	}
}
