package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// dirSourceTestProvider is a local provider that also implements
// provider.PlaylistDirSourceManager, so the manager's directory-sources screen
// can be exercised without touching the filesystem.
type dirSourceTestProvider struct {
	commandsTestProvider
	dirs     []playlist.DirSource
	removed  []string
	setRec   []dirSetRecCall
	added    []string
	failOn   map[string]error // dirs that AddDirSource should fail on
	favPaths map[string]struct{}
}

type dirSetRecCall struct {
	dir       string
	recursive bool
}

func (p *dirSourceTestProvider) DirSources(string) ([]playlist.DirSource, error) {
	return p.dirs, nil
}
func (p *dirSourceTestProvider) AddDirSource(_, dir string) (bool, error) {
	if p.failOn != nil {
		if err, ok := p.failOn[dir]; ok {
			return false, err
		}
	}
	for _, d := range p.dirs {
		if d.Path == dir {
			return false, nil
		}
	}
	p.dirs = append(p.dirs, playlist.DirSource{Path: dir, Recursive: true})
	p.added = append(p.added, dir)
	return true, nil
}
func (p *dirSourceTestProvider) RemoveDirSource(_, dir string) error {
	p.removed = append(p.removed, dir)
	out := p.dirs[:0]
	for _, d := range p.dirs {
		if d.Path != dir {
			out = append(out, d)
		}
	}
	p.dirs = out
	return nil
}
func (p *dirSourceTestProvider) SetDirRecursive(_, dir string, recursive bool) error {
	p.setRec = append(p.setRec, dirSetRecCall{dir, recursive})
	for i := range p.dirs {
		if p.dirs[i].Path == dir {
			p.dirs[i].Recursive = recursive
		}
	}
	return nil
}

func (p *dirSourceTestProvider) ToggleFavorite(track playlist.Track) (bool, error) {
	if p.favPaths == nil {
		p.favPaths = make(map[string]struct{})
	}
	if _, ok := p.favPaths[track.Path]; ok {
		delete(p.favPaths, track.Path)
		return false, nil
	}
	p.favPaths[track.Path] = struct{}{}
	return true, nil
}

func (p *dirSourceTestProvider) IsFavorited(path string) bool {
	if p.favPaths == nil {
		return false
	}
	_, ok := p.favPaths[path]
	return ok
}

func (p *dirSourceTestProvider) FavoritesCount() int {
	return len(p.favPaths)
}

func (p *dirSourceTestProvider) Tracks(id string) ([]playlist.Track, error) {
	if id == "Favorites" && p.favPaths != nil {
		var out []playlist.Track
		for path := range p.favPaths {
			out = append(out, playlist.Track{Path: path, Title: path})
		}
		return out, nil
	}
	return nil, nil
}

func newDirsScreenTestModel(prov *dirSourceTestProvider) Model {
	m := Model{
		playlist:      playlist.New(),
		localProvider: prov,
		provider:      prov,
		favMgr:        prov,
		vis:           ui.NewVisualizer(48000),
		plManager: plManagerState{
			visible:     true,
			screen:      plMgrScreenTracks,
			selPlaylist: "music",
			tracks:      []playlist.Track{{Path: "/a.mp3", Title: "A"}},
		},
	}
	return m
}

func TestPlMgrDKeyOpensDirsScreen(t *testing.T) {
	prov := &dirSourceTestProvider{
		dirs: []playlist.DirSource{{Path: "/home/me/Music", Recursive: true}},
	}
	m := newDirsScreenTestModel(prov)

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "D"})

	if m.plManager.screen != plMgrScreenDirs {
		t.Fatalf("screen = %v, want plMgrScreenDirs", m.plManager.screen)
	}
	if len(m.plManager.dirs) != 1 || m.plManager.dirs[0].Path != "/home/me/Music" {
		t.Fatalf("dirs = %+v, want the loaded source", m.plManager.dirs)
	}
}

func TestPlMgrDKeyHistoryShowsNotice(t *testing.T) {
	prov := &dirSourceTestProvider{}
	m := newDirsScreenTestModel(prov)
	m.plManager.selPlaylist = "Recently Played"

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "D"})

	if m.plManager.screen != plMgrScreenTracks {
		t.Fatalf("screen = %v, want to stay on Tracks for the virtual history playlist", m.plManager.screen)
	}
	if len(prov.dirs) != 0 {
		t.Fatalf("DirSources should not be called for history, dirs = %+v", prov.dirs)
	}
	if m.status.text == "" {
		t.Fatal("expected a status notice for the virtual history playlist")
	}
}

func TestPlMgrDKeyNoticeWhenUnsupported(t *testing.T) {
	// A plain commandsTestProvider does not implement PlaylistDirSourceManager,
	// so the D key must show a notice and stay on the tracks screen.
	plain := commandsTestProvider{name: "Local"}
	m := newDirsScreenTestModel(&dirSourceTestProvider{})
	m.localProvider = plain
	m.provider = plain

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "D"})

	if m.plManager.screen != plMgrScreenTracks {
		t.Fatalf("screen = %v, want to stay on Tracks when unsupported", m.plManager.screen)
	}
	if m.status.text == "" {
		t.Fatal("expected a status notice when the provider lacks dir sources")
	}
}

func TestPlMgrDirsToggleRecursive(t *testing.T) {
	prov := &dirSourceTestProvider{
		dirs: []playlist.DirSource{{Path: "/Music", Recursive: true}},
	}
	m := newDirsScreenTestModel(prov)
	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "D"}) // open dirs screen

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "r"})

	if len(prov.setRec) != 1 {
		t.Fatalf("SetDirRecursive calls = %d, want 1", len(prov.setRec))
	}
	if prov.setRec[0].dir != "/Music" || prov.setRec[0].recursive {
		t.Fatalf("toggle call = %+v, want /Music recursive=false", prov.setRec[0])
	}
	if m.plManager.dirs[0].Recursive {
		t.Fatalf("dir in manager state should now be flat, got %+v", m.plManager.dirs[0])
	}
}

func TestPlMgrDirsRemoveConfirmFlow(t *testing.T) {
	prov := &dirSourceTestProvider{
		dirs: []playlist.DirSource{{Path: "/Music", Recursive: true}},
	}
	m := newDirsScreenTestModel(prov)
	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "D"}) // open dirs screen

	// 'd' arms the confirmation prompt; nothing removed yet.
	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "d"})
	if !m.plManager.confirmDel {
		t.Fatal("confirmDel should be armed after 'd'")
	}
	if len(prov.removed) != 0 {
		t.Fatalf("removed = %v before confirm, want none", prov.removed)
	}

	// 'y' confirms and removes the highlighted source.
	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "y"})
	if len(prov.removed) != 1 || prov.removed[0] != "/Music" {
		t.Fatalf("removed = %v, want [/Music]", prov.removed)
	}
	if m.plManager.confirmDel {
		t.Fatal("confirmDel should be cleared after 'y'")
	}
	if len(m.plManager.dirs) != 0 {
		t.Fatalf("dirs after remove = %+v, want empty", m.plManager.dirs)
	}
}

func TestFileBrowserDAddsDirSource(t *testing.T) {
	prov := &dirSourceTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
	}
	m := newDirsScreenTestModel(prov)
	// Reproduce the file-browser state set up by openFileBrowserForPlaylist:
	// the current browsing dir becomes the added source when nothing is selected.
	m.plManager.screen = plMgrScreenDirs
	m.fileBrowser.visible = true
	m.fileBrowser.targetPlaylist = "music"
	m.fileBrowser.dir = "/home/me/Music"

	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "D"})

	if m.fileBrowser.visible {
		t.Fatal("file browser should close after adding a dir source")
	}
	if len(prov.added) != 1 || prov.added[0] != "/home/me/Music" {
		t.Fatalf("added = %v, want [/home/me/Music]", prov.added)
	}
	if len(m.plManager.dirs) != 1 {
		t.Fatalf("manager dirs after add = %+v, want refreshed to 1", m.plManager.dirs)
	}
}

func TestFileBrowserDPartialFailureStillRefreshes(t *testing.T) {
	prov := &dirSourceTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
		failOn:               map[string]error{"/d2": errors.New("boom")},
	}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenDirs
	m.plManager.selPlaylist = "music"
	m.fileBrowser.visible = true
	m.fileBrowser.targetPlaylist = "music"
	// Two selected directories; the second fails partway through the loop.
	m.fileBrowser.entries = []fbEntry{
		{name: "d1", path: "/d1", isDir: true},
		{name: "d2", path: "/d2", isDir: true},
	}
	m.fileBrowser.selected = map[string]bool{"/d1": true, "/d2": true}

	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "D"})

	// The first dir was added even though the second failed.
	if len(prov.added) != 1 || prov.added[0] != "/d1" {
		t.Fatalf("added = %v, want only /d1 (partial success)", prov.added)
	}
	// The open manager must reflect the partial addition, not the pre-add state.
	if len(m.plManager.dirs) != 1 || m.plManager.dirs[0].Path != "/d1" {
		t.Fatalf("manager dirs after partial failure = %+v, want [/d1]", m.plManager.dirs)
	}
	if !strings.Contains(m.status.text, "then failed") {
		t.Fatalf("status = %q, want a partial-failure message", m.status.text)
	}
}

func TestNKeyTogglesFavorite(t *testing.T) {
	prov := &dirSourceTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
	}
	m := newDirsScreenTestModel(prov)
	m.focus = focusPlaylist
	m.loadedPlaylist = "music"
	m.playlist = playlist.New()
	m.playlist.Add(playlist.Track{Path: "/song.mp3", Title: "Song"})
	m.plCursor = 0
	m.plManager.visible = false
	m.favSet = nil

	// Toggle on.
	m.handleKey(tea.KeyPressMsg{Text: "n"})
	if !prov.IsFavorited("/song.mp3") {
		t.Fatal("track should be favorited after n")
	}
	if m.favSet == nil {
		t.Fatal("favSet should be populated after toggle")
	}
	if _, ok := m.favSet["/song.mp3"]; !ok {
		t.Fatal("favSet should contain /song.mp3 after toggle")
	}
	if !strings.Contains(m.status.text, "♥") {
		t.Fatalf("status = %q, want ♥ indicator", m.status.text)
	}

	// Toggle off.
	m.handleKey(tea.KeyPressMsg{Text: "n"})
	if prov.IsFavorited("/song.mp3") {
		t.Fatal("track should be unfavorited after second n")
	}
	if m.favSet != nil {
		if _, ok := m.favSet["/song.mp3"]; ok {
			t.Fatal("favSet should not contain /song.mp3 after toggle off")
		}
	}
	if !strings.Contains(m.status.text, "♡") {
		t.Fatalf("status = %q, want ♡ indicator", m.status.text)
	}
}

func TestNKeyNoopWithoutFavMgr(t *testing.T) {
	plain := commandsTestProvider{name: "Local"}
	m := newDirsScreenTestModel(&dirSourceTestProvider{})
	m.localProvider = plain
	m.provider = plain
	m.favMgr = nil
	m.focus = focusPlaylist
	m.loadedPlaylist = "music"
	m.playlist = playlist.New()
	m.playlist.Add(playlist.Track{Path: "/song.mp3", Title: "Song"})
	m.plCursor = 0

	m.handleKey(tea.KeyPressMsg{Text: "n"})

	// No crash, no status change.
	if m.status.text != "" {
		t.Fatalf("status = %q, want empty (no favMgr)", m.status.text)
	}
}
