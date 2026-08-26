package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// dirSourceTestProvider is a local provider that also implements
// provider.PlaylistDirSourceManager, so the manager's directory-sources screen
// can be exercised without touching the filesystem.
type dirSourceTestProvider struct {
	commandsTestProvider
	dirs          []playlist.DirSource
	removed       []string
	setRec        []dirSetRecCall
	added         []string
	failOn        map[string]error // dirs that AddDirSource should fail on
	historyTracks []playlist.Track // served for the Recently Played playlist
	favPaths      map[string]struct{}
}

type dirSetRecCall struct {
	dir       string
	recursive bool
}

func (p *dirSourceTestProvider) Tracks(id string) ([]playlist.Track, error) {
	if id == history.PlaylistName {
		return append([]playlist.Track(nil), p.historyTracks...), nil
	}
	if id == favorites.PlaylistName && p.favPaths != nil {
		var out []playlist.Track
		for path := range p.favPaths {
			out = append(out, playlist.Track{Path: path, Title: path})
		}
		return out, nil
	}
	return p.commandsTestProvider.Tracks(id)
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

func (p *dirSourceTestProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	favs := playlist.PlaylistInfo{ID: favorites.PlaylistName, Name: favorites.PlaylistName, Section: favorites.PlaylistName}
	if p.favPaths != nil {
		favs.TrackCount = len(p.favPaths)
	}
	return []playlist.PlaylistInfo{favs, {ID: "music", Name: "music"}}, nil
}

func newDirsScreenTestModel(prov playlist.Provider) Model {
	var favMgr provider.FavoritesManager
	if fm, ok := prov.(provider.FavoritesManager); ok {
		favMgr = fm
	}
	m := Model{
		playlist:      playlist.New(),
		localProvider: prov,
		provider:      prov,
		favMgr:        favMgr,
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

// creatingDirSourceProvider adds PlaylistCreator to the dir-source fake so the
// manager's new-playlist flow can be exercised without touching the filesystem.
type creatingDirSourceProvider struct {
	dirSourceTestProvider
	created bool
}

func (p *creatingDirSourceProvider) CreatePlaylist(_ context.Context, name string) (string, error) {
	p.created = true
	return name, nil
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
	m.plManager.selPlaylist = history.PlaylistName

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

// paneManageProvider records playlist deletions for guard tests.
type paneManageProvider struct {
	commandsTestProvider
	deleted  []string
	restored []string
	docs     map[string][]byte
	// docRestores records payloads passed to RestorePlaylistDocument.
	docRestores map[string][]byte
}

func (p *paneManageProvider) CreatePlaylist(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (p *paneManageProvider) RemoveTrack(_ string, _ int) error { return nil }

func (p *paneManageProvider) SavePlaylist(name string, _ []playlist.Track) error {
	p.restored = append(p.restored, name)
	return nil
}

func (p *paneManageProvider) PlaylistDocument(name string) ([]byte, error) {
	if data, ok := p.docs[name]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func (p *paneManageProvider) RestorePlaylistDocument(name string, data []byte) error {
	if p.docRestores == nil {
		p.docRestores = make(map[string][]byte)
	}
	p.docRestores[name] = data
	p.restored = append(p.restored, name)
	return nil
}

func (p *paneManageProvider) DeletePlaylist(name string) error {
	p.deleted = append(p.deleted, name)
	return nil
}

// Playlists mirrors the file-backed provider: deleted names vanish and
// restored names reappear on the next pull.
func (p *paneManageProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	lists, err := p.commandsTestProvider.Playlists()
	if err != nil {
		return nil, err
	}
	var out []playlist.PlaylistInfo
	for _, pl := range lists {
		deleted := false
		for _, name := range p.deleted {
			if pl.Name == name && !slices.Contains(p.restored, name) {
				deleted = true
				break
			}
		}
		if !deleted {
			out = append(out, pl)
		}
	}
	return out, nil
}

func TestPlMgrDeleteRefreshesProviderPane(t *testing.T) {
	prov := &paneManageProvider{commandsTestProvider: commandsTestProvider{
		name:  "Local",
		lists: []playlist.PlaylistInfo{{ID: "music", Name: "music"}, {ID: "top40", Name: "top40"}},
	}}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = []playlist.PlaylistInfo{
		{ID: "music", Name: "music"},
		{ID: "top40", Name: "top40"},
	}
	m.plManager.cursor = 1

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "d"}) // arm confirmation
	if !m.plManager.confirmDel {
		t.Fatal("confirmDel should be armed after 'd'")
	}
	cmd := m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "y"})
	if len(prov.deleted) != 1 || prov.deleted[0] != "top40" {
		t.Fatalf("deleted = %v, want [top40]", prov.deleted)
	}
	if cmd == nil {
		t.Fatal("delete must schedule a provider-pane refresh")
	}
	// The manager list itself re-pulls immediately.
	found := false
	for _, pl := range m.plManager.playlists {
		if pl.Name == "top40" {
			found = true
		}
	}
	if found {
		t.Fatalf("playlists = %+v, want top40 gone from the manager list", m.plManager.playlists)
	}

	// Undo restores the playlist and schedules a pane refresh too.
	if cmd := m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "u"}); cmd == nil {
		t.Fatal("undo of a playlist must schedule a provider-pane refresh")
	}
	found = false
	for _, pl := range m.plManager.playlists {
		if pl.Name == "top40" {
			found = true
		}
	}
	if !found {
		t.Fatalf("playlists = %+v, want top40 restored in the manager list", m.plManager.playlists)
	}
}

// A local playlist mutation while a remote provider is active must not start
// a fetch for that remote provider (or surface its errors).
func TestPlMgrDeleteSkipsPaneFetchWhenRemoteActive(t *testing.T) {
	local := &paneManageProvider{commandsTestProvider: commandsTestProvider{
		name:  "Local",
		lists: []playlist.PlaylistInfo{{ID: "top40", Name: "top40"}},
	}}
	remote := &commandsTestProvider{name: "Navidrome", lists: []playlist.PlaylistInfo{{ID: "nd", Name: "nd"}}}
	m := newDirsScreenTestModel(local)
	m.provider = remote
	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = []playlist.PlaylistInfo{{ID: "top40", Name: "top40"}}
	m.plManager.cursor = 0

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "d"})
	if cmd := m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "y"}); cmd != nil {
		t.Fatal("delete with a remote provider active must not schedule a pane refresh")
	}
	if len(local.deleted) != 1 || local.deleted[0] != "top40" {
		t.Fatalf("deleted = %v, want [top40] on the local provider", local.deleted)
	}
}

func TestPlMgrDeleteGuardsRecentlyPlayed(t *testing.T) {
	prov := &paneManageProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = []playlist.PlaylistInfo{
		{ID: "music", Name: "music"},
		{ID: history.PlaylistName, Name: history.PlaylistName},
	}
	m.plManager.cursor = 1

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "d"})
	if m.plManager.confirmDel {
		t.Fatal("d on Recently Played must not ask for confirmation")
	}
	if !strings.Contains(m.status.text, "cannot be deleted") {
		t.Fatalf("status = %q, want a protection notice", m.status.text)
	}

	// Even if the confirm flag is somehow set, y must not delete it.
	m.plManager.confirmDel = true
	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "y"})
	if len(prov.deleted) != 0 {
		t.Fatalf("deleted = %v, want Recently Played untouched", prov.deleted)
	}
}

func TestPlMgrNKeyRemovesRowFromFavoritesScreen(t *testing.T) {
	prov := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	if _, err := prov.ToggleFavorite(playlist.Track{Path: "/a.mp3", Title: "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := prov.ToggleFavorite(playlist.Track{Path: "/b.mp3", Title: "B"}); err != nil {
		t.Fatal(err)
	}
	m := newDirsScreenTestModel(prov)
	m.plManager.selPlaylist = favorites.PlaylistName
	m.plMgrLoadTracks([]playlist.Track{{Path: "/a.mp3"}, {Path: "/b.mp3"}})

	cmd := m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "n"})

	if prov.IsFavorited("/a.mp3") {
		t.Fatal("n should unfavorite the highlighted track")
	}
	if len(m.plManager.tracks) != 1 || m.plManager.tracks[0].Path != "/b.mp3" {
		t.Fatalf("tracks = %+v, want only /b.mp3", m.plManager.tracks)
	}
	if !strings.HasPrefix(m.status.text, favRemovedMark) {
		t.Fatalf("status = %q, want dimmed heart prefix %q", m.status.text, favRemovedMark)
	}
	if cmd == nil {
		t.Fatal("expected a provider-playlist refresh command")
	}
	// The manager list must already show the updated Favorites count.
	m.plMgrRefreshList()
	found := false
	for _, pl := range m.plManager.playlists {
		if pl.Name == favorites.PlaylistName && pl.TrackCount == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("playlists = %+v, want Favorites · 1 tracks after unfavorite", m.plManager.playlists)
	}
}

func TestNKeyFromQueueRefreshesFavoritesCount(t *testing.T) {
	prov := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := newDirsScreenTestModel(prov)
	m.focus = focusPlaylist
	m.loadedPlaylist = "music"
	m.playlist = playlist.New()
	m.playlist.Add(playlist.Track{Path: "/song.mp3", Title: "Song"})
	m.plCursor = 0
	m.plManager.visible = false
	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = nil

	if cmd := m.handleKey(tea.KeyPressMsg{Text: "n"}); cmd == nil {
		t.Fatal("expected a provider-playlist refresh command")
	}
	if !prov.IsFavorited("/song.mp3") {
		t.Fatal("track should be favorited after n")
	}
	// Opening the manager must show the updated Favorites count.
	m.openPlaylistManager()
	found := false
	for _, pl := range m.plManager.playlists {
		if pl.Name == favorites.PlaylistName && pl.TrackCount == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("playlists = %+v, want Favorites · 1 tracks after favorite", m.plManager.playlists)
	}
}

func TestPlMgrDeleteGuardsFavorites(t *testing.T) {
	prov := &paneManageProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = []playlist.PlaylistInfo{
		{ID: favorites.PlaylistName, Name: favorites.PlaylistName},
	}
	m.plManager.cursor = 0

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "d"})
	if m.plManager.confirmDel {
		t.Fatal("d on Favorites must not ask for confirmation")
	}
	if !strings.Contains(m.status.text, "cannot be deleted") {
		t.Fatalf("status = %q, want a protection notice", m.status.text)
	}

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "r"})
	if m.plManager.screen == plMgrScreenRename {
		t.Fatal("r on Favorites must not open the rename input")
	}

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "D"})
	if m.fileBrowser.visible {
		t.Fatal("D on Favorites must not open the directory browser")
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
	// with nothing selected or highlighted, the browsing dir becomes the source.
	m.plManager.screen = plMgrScreenDirs
	m.fileBrowser.visible = true
	m.fileBrowser.targetPlaylist = "music"
	m.fileBrowser.dir = "/home/me/Music"

	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "D"})

	if !m.fileBrowser.visible {
		t.Fatal("file browser should stay open for adding more folders")
	}
	if len(prov.added) != 1 || prov.added[0] != "/home/me/Music" {
		t.Fatalf("added = %v, want [/home/me/Music]", prov.added)
	}
	if len(m.plManager.dirs) != 1 {
		t.Fatalf("manager dirs after add = %+v, want refreshed to 1", m.plManager.dirs)
	}
}

func TestFileBrowserDGrabsHighlightedFolder(t *testing.T) {
	prov := &dirSourceTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
	}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenDirs
	m.fileBrowser.visible = true
	m.fileBrowser.targetPlaylist = "music"
	m.fileBrowser.dir = "/home/me/Music"
	m.fileBrowser.entries = []fbEntry{
		{name: "..", path: "/home/me", isDir: true, isParent: true},
		{name: "Metal", path: "/home/me/Music/Metal", isDir: true},
		{name: "song.mp3", path: "/home/me/Music/song.mp3", isAudio: true},
	}
	m.fileBrowser.cursor = 1 // highlight Metal, nothing selected

	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "D"})

	if len(prov.added) != 1 || prov.added[0] != "/home/me/Music/Metal" {
		t.Fatalf("added = %v, want the highlighted folder", prov.added)
	}
	if m.fileBrowser.dir != "/home/me/Music" {
		t.Fatalf("browser navigated to %q, want it to stay put", m.fileBrowser.dir)
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

func TestFbConfirmSelectedDirBecomesSource(t *testing.T) {
	prov := &dirSourceTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
	}
	m := newDirsScreenTestModel(prov)
	m.fileBrowser.visible = true
	m.fileBrowser.targetPlaylist = "music"
	m.fileBrowser.entries = []fbEntry{
		{name: "Album", path: "/music/Album", isDir: true},
	}
	m.fileBrowser.selected = map[string]bool{"/music/Album": true}

	cmd := m.fbConfirm(false)

	if cmd != nil {
		t.Fatal("confirm with only directories selected must not resolve tracks")
	}
	if m.fileBrowser.visible {
		t.Fatal("file browser should close after confirm")
	}
	if len(prov.added) != 1 || prov.added[0] != "/music/Album" {
		t.Fatalf("added = %v, want [/music/Album]", prov.added)
	}
}

func TestFbConfirmMixedSelectionSplitsDirsAndFiles(t *testing.T) {
	prov := &dirSourceTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
	}
	m := newDirsScreenTestModel(prov)
	m.fileBrowser.visible = true
	m.fileBrowser.targetPlaylist = "music"
	m.fileBrowser.entries = []fbEntry{
		{name: "Album", path: "/music/Album", isDir: true},
		{name: "song.mp3", path: "/music/song.mp3", isAudio: true},
	}
	m.fileBrowser.selected = map[string]bool{"/music/Album": true, "/music/song.mp3": true}

	cmd := m.fbConfirm(false)

	if cmd == nil {
		t.Fatal("confirm with audio files selected should return a track-resolution command")
	}
	if len(prov.added) != 1 || prov.added[0] != "/music/Album" {
		t.Fatalf("added = %v, want the directory as a source", prov.added)
	}
}

func TestProviderPanePOpensPlaylistManager(t *testing.T) {
	prov := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := newDirsScreenTestModel(prov)
	m.focus = focusProvider
	m.plManager.visible = false

	m.handleKey(tea.KeyPressMsg{Text: "p"})

	if !m.plManager.visible {
		t.Fatal("p should open the playlist manager from the provider pane")
	}
	if m.plManager.screen != plMgrScreenList {
		t.Fatalf("screen = %v, want plMgrScreenList", m.plManager.screen)
	}
}

func TestPlMgrAKeyOpensNewPlaylistInput(t *testing.T) {
	prov := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenList

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "a"})

	if m.plManager.screen != plMgrScreenNewName {
		t.Fatalf("screen = %v, want plMgrScreenNewName", m.plManager.screen)
	}
	if m.plManager.newName != "" {
		t.Fatalf("newName = %q, want empty", m.plManager.newName)
	}
}

func TestPlMgrDKeyOnListOpensBrowserForPlaylist(t *testing.T) {
	prov := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = []playlist.PlaylistInfo{{ID: "music", Name: "music"}}
	m.plManager.cursor = 0

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "D"})

	if !m.fileBrowser.visible {
		t.Fatal("file browser should open for the highlighted playlist")
	}
	if m.fileBrowser.targetPlaylist != "music" {
		t.Fatalf("targetPlaylist = %q, want music", m.fileBrowser.targetPlaylist)
	}
}

func TestPlMgrNewNameEnterCreatesAndOpensBrowser(t *testing.T) {
	prov := &creatingDirSourceProvider{dirSourceTestProvider: dirSourceTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
	}}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenNewName
	m.plManager.newName = "fresh"

	m.handlePlMgrNewNameKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !prov.created {
		t.Fatal("provider CreatePlaylist was not called")
	}
	if m.plManager.screen != plMgrScreenList {
		t.Fatalf("screen = %v, want back on plMgrScreenList", m.plManager.screen)
	}
	if !m.fileBrowser.visible || m.fileBrowser.targetPlaylist != "fresh" {
		t.Fatalf("browser visible=%v target=%q, want it open targeted at fresh", m.fileBrowser.visible, m.fileBrowser.targetPlaylist)
	}
	if !strings.Contains(m.status.text, "Created") {
		t.Fatalf("status = %q, want a created hint", m.status.text)
	}
}

func TestPlMgrNewNameBrowserStartsAtHome(t *testing.T) {
	prov := &creatingDirSourceProvider{dirSourceTestProvider: dirSourceTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
	}}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenNewName
	m.plManager.newName = "fresh"
	m.fileBrowser.dir = "/var/log"

	m.handlePlMgrNewNameKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	if m.fileBrowser.dir != home {
		t.Fatalf("browser dir = %q, want %q", m.fileBrowser.dir, home)
	}
}

func TestFileBrowserEscDoneCommitsSelection(t *testing.T) {
	prov := &dirSourceTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
	}
	m := newDirsScreenTestModel(prov)
	m.focus = focusPlaylist
	m.fileBrowser.visible = true
	m.fileBrowser.targetPlaylist = "music"
	m.fileBrowser.selected = make(map[string]bool)
	m.fileBrowser.entries = []fbEntry{
		{name: "..", path: "/home/me", isDir: true, isParent: true},
		{name: "Metal/", path: "/home/me/Music/Metal", isDir: true},
	}
	m.fileBrowser.selected["/home/me/Music/Metal"] = true

	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "esc"})

	if m.fileBrowser.visible {
		t.Fatal("Esc should close the browser")
	}
	if len(prov.added) != 1 || prov.added[0] != "/home/me/Music/Metal" {
		t.Fatalf("added = %v, want the pending selection committed on Esc", prov.added)
	}
}

func TestBeginPlaybackTrackRecordsHistoryImmediately(t *testing.T) {
	prov := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{
		name:  "Local",
		lists: []playlist.PlaylistInfo{{ID: "music", Name: "music"}},
	}}
	m := newDirsScreenTestModel(prov)
	m.historyStore = history.NewAt(filepath.Join(t.TempDir(), "history.toml"))
	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = nil

	// Starting a track records it right away: after pressing next, the
	// current song — not the previous one — tops Recently Played.
	_, cmd := m.beginPlaybackTrack(playlist.Track{Path: "/now.mp3", Title: "Now"})
	if cmd == nil {
		t.Fatal("expected a provider-playlist refresh command when history records")
	}
	if len(m.plManager.playlists) == 0 {
		t.Fatal("manager list should be re-pulled when a track starts")
	}

	entries, err := m.historyStore.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Track.Path != "/now.mp3" {
		t.Fatalf("history = %+v, want the starting track recorded", entries)
	}
}

func TestMaybeScrobbleLeavesHistoryToTrackStart(t *testing.T) {
	prov := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := newDirsScreenTestModel(prov)
	m.historyStore = history.NewAt(filepath.Join(t.TempDir(), "history.toml"))
	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = nil

	// Leaving a track (skip/finish) only handles provider scrobbles; the
	// history entry was already written when playback started.
	if cmd := m.maybeScrobble(playlist.Track{Path: "/song.mp3", DurationSecs: 120}, 120*time.Second, 120*time.Second); cmd != nil {
		t.Fatal("scrobble must not schedule a history refresh")
	}
	entries, err := m.historyStore.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("history = %+v, want no entries from scrobble alone", entries)
	}
}

func TestMaybeScrobbleReloadsOpenHistoryTracks(t *testing.T) {
	prov := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	m := newDirsScreenTestModel(prov)
	m.historyStore = history.NewAt(filepath.Join(t.TempDir(), "history.toml"))
	m.plManager.screen = plMgrScreenTracks
	m.plManager.selPlaylist = history.PlaylistName
	m.plMgrLoadTracks([]playlist.Track{{Path: "/old.mp3", Title: "Old"}})
	m.plManager.cursor = 5 // beyond the new list; must clamp

	prov.historyTracks = []playlist.Track{
		{Path: "/new1.mp3", Title: "New1"},
		{Path: "/new2.mp3", Title: "New2"},
	}

	if _, cmd := m.beginPlaybackTrack(playlist.Track{Path: "/song.mp3", Title: "Song"}); cmd == nil {
		t.Fatal("expected a provider-playlist refresh command when a track starts")
	}
	if len(m.plManager.tracks) != 2 || m.plManager.tracks[0].Path != "/new1.mp3" {
		t.Fatalf("tracks = %+v, want the reloaded history entries", m.plManager.tracks)
	}
	if m.plManager.cursor != 1 {
		t.Fatalf("cursor = %d, want clamped to 1", m.plManager.cursor)
	}
}

// Removing the last favorite empties the screen; a stale scroll offset must
// not survive (the scroll adjuster returns early on an empty list).
func TestPlMgrReloadTracksResetsScrollWhenEmpty(t *testing.T) {
	prov := &dirSourceTestProvider{}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenTracks
	m.plManager.selPlaylist = favorites.PlaylistName
	m.plManager.tracks = []playlist.Track{{Path: "/a.mp3", Title: "A"}}
	m.plManager.cursor = 0
	m.plManager.scroll = 5

	m.plMgrReloadTracks(favorites.PlaylistName)

	if len(m.plManager.tracks) != 0 {
		t.Fatalf("tracks = %d, want 0 after reload with no favorites", len(m.plManager.tracks))
	}
	if m.plManager.cursor != 0 || m.plManager.scroll != 0 {
		t.Fatalf("cursor=%d scroll=%d, want both reset to 0", m.plManager.cursor, m.plManager.scroll)
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
	if !strings.HasPrefix(m.status.text, favAddedMark) {
		t.Fatalf("status = %q, want red heart prefix %q", m.status.text, favAddedMark)
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
	if !strings.HasPrefix(m.status.text, favRemovedMark) {
		t.Fatalf("status = %q, want dimmed heart prefix %q", m.status.text, favRemovedMark)
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

// Deleting a playlist whose document contains [[dir]] sources must restore
// those sources verbatim on undo instead of rewriting a tracks-only file.
func TestPlMgrDeleteUndoRestoresDirDocument(t *testing.T) {
	prov := &paneManageProvider{
		commandsTestProvider: commandsTestProvider{
			name:  "Local",
			lists: []playlist.PlaylistInfo{{ID: "mix", Name: "mix"}},
		},
		docs: map[string][]byte{
			"mix": []byte("[[dir]]\npath = \"/music/rock\"\nrecursive = true\n\n[[track]]\npath = \"/music/keep.mp3\"\n"),
		},
	}
	m := newDirsScreenTestModel(prov)
	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = []playlist.PlaylistInfo{{ID: "mix", Name: "mix"}}
	m.plManager.cursor = 0

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "d"}) // arm confirmation
	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "y"}) // delete
	if len(prov.deleted) != 1 || prov.deleted[0] != "mix" {
		t.Fatalf("deleted = %v, want [mix]", prov.deleted)
	}

	if cmd := m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "u"}); cmd == nil {
		t.Fatal("undo of a deleted playlist must schedule a pane refresh")
	}
	got, ok := prov.docRestores["mix"]
	if !ok {
		t.Fatal("undo must restore the raw playlist document")
	}
	if !strings.Contains(string(got), "[[dir]]") || !strings.Contains(string(got), "/music/rock") {
		t.Fatalf("restored doc lost [[dir]] source: %q", got)
	}
}
