package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

type localInlineFake struct {
	commandsTestProvider
}

func (p *localInlineFake) Tracks(string) ([]playlist.Track, error) { return nil, nil }

func newLocalInlineModel() Model {
	local := &localInlineFake{commandsTestProvider{name: "Local", lists: []playlist.PlaylistInfo{
		{ID: "music", Name: "music"},
	}}}
	remote := commandsTestProvider{name: "Spotify"}
	return Model{
		playlist:      playlist.New(),
		provider:      local,
		localProvider: local,
		providers: []provider.Entry{
			{Key: "local", Name: "Local", Provider: local},
			{Key: "spotify", Name: "Spotify", Provider: remote},
		},
		provPillIdx: 0,
		focus:       focusProvider,
		vis:         ui.NewVisualizer(48000),
	}
}

func TestLocalSourceShowsPageNotManager(t *testing.T) {
	m := newLocalInlineModel()
	m.plManager.visible = false

	m.switchProvider(0)

	if m.plManager.visible {
		t.Fatal("switching to local should show the provider page, not the playlist manager")
	}
	if m.focus != focusProvider {
		t.Fatalf("focus = %v, want focusProvider", m.focus)
	}
}

func TestNonLocalSourceKeepsPane(t *testing.T) {
	m := newLocalInlineModel()

	m.switchProvider(1)

	if m.plManager.visible {
		t.Fatal("switching to a remote provider must not open the playlist")
	}
}

func TestStartInLocalKeepsPage(t *testing.T) {
	m := newLocalInlineModel()
	m.plManager.visible = false

	m.StartInProvider()

	if m.plManager.visible {
		t.Fatal("starting in Local should show the provider page, not the playlist manager")
	}
}

func TestEscFromPlaylistOpensLocalPage(t *testing.T) {
	m := newLocalInlineModel()
	m.focus = focusPlaylist
	m.plManager.visible = false

	m.handleMainKey(tea.KeyPressMsg{Text: "esc", Code: tea.KeyEscape})

	if m.focus != focusProvider {
		t.Fatalf("focus = %v, want focusProvider", m.focus)
	}
	if m.plManager.visible {
		t.Fatal("esc to Local should show the provider page, not the playlist manager")
	}
	if m.fileBrowser.visible {
		t.Fatal("esc with no file-browser session should not open the file browser")
	}
}

func TestClosingPlaylistManagerStaysOnLocalPage(t *testing.T) {
	m := newLocalInlineModel()
	m.openPlaylistManager()
	m.focus = focusProvider

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "esc", Code: tea.KeyEscape})

	if m.plManager.visible {
		t.Fatal("esc should close the playlist manager")
	}
	if m.focus != focusProvider {
		t.Fatalf("focus = %v, want focusProvider", m.focus)
	}
}

func TestLocalPageListsBrowseAndManager(t *testing.T) {
	m := newLocalInlineModel()
	m.replaceProviderLists([]playlist.PlaylistInfo{
		{ID: "Favorites", Name: "Favorites", Section: local.SectionPlaylists},
		{ID: "music", Name: "music", Section: local.SectionPlaylists},
	})

	if len(m.provPane.lists) != 2 {
		t.Fatalf("rows = %+v, want browse and playlist manager only", m.provPane.lists)
	}
	if m.provPane.lists[0].ID != local.BrowseFilesID || m.provPane.lists[0].Section != "" {
		t.Fatalf("first row = %+v, want browse without a section", m.provPane.lists[0])
	}
	if m.provPane.lists[1].ID != local.PlaylistManagerID || m.provPane.lists[1].Name != "Playlists" {
		t.Fatalf("second row = %+v", m.provPane.lists[1])
	}
}

func TestLocalPageRowsOmitShortcutSuffix(t *testing.T) {
	m := newLocalInlineModel()
	m.replaceProviderLists(nil)

	for i, wantName := range []string{"Open local files", "Playlists"} {
		row := stripAnsi(m.formatProviderRowLine(m.provPane.lists[i], false))
		if strings.TrimSpace(row) != wantName {
			t.Fatalf("row %d = %q, want %q without shortcut suffix", i, row, wantName)
		}
	}
}

func TestLocalProviderBlankLinesBefore(t *testing.T) {
	lists := localProviderLists()
	if got := localProviderBlankLinesBefore(lists, 0, 0); got != 1 {
		t.Errorf("before browse at scroll 0 = %d, want 1", got)
	}
	if got := localProviderBlankLinesBefore(lists, 0, 1); got != 0 {
		t.Errorf("before browse when scrolled = %d, want 0", got)
	}
	if got := localProviderBlankLinesBefore(lists, 1, 0); got != 1 {
		t.Errorf("before playlist manager = %d, want 1", got)
	}
}

func TestLocalPageRendersBlankSeparators(t *testing.T) {
	m := newLocalInlineModel()
	m.focus = focusProvider
	m.plVisible = 12
	m.replaceProviderLists(nil)

	rows := strings.Split(stripAnsi(m.renderProviderList()), "\n")
	if len(rows) < 5 {
		t.Fatalf("body rows = %d, want room for blanks: %q", len(rows), rows)
	}
	if rows[0] != "" {
		t.Errorf("row 0 = %q, want blank under Local header", rows[0])
	}
	if rows[2] != "" {
		t.Errorf("row 2 = %q, want blank before Playlists", rows[2])
	}
}

func TestLocalPagePlaylistManagerOpensOverlay(t *testing.T) {
	m := newLocalInlineModel()
	m.replaceProviderLists(nil)

	m.openProviderList(1)

	if !m.plManager.visible || m.plManager.screen != plMgrScreenList {
		t.Fatalf("manager visible=%v screen=%v, want list screen", m.plManager.visible, m.plManager.screen)
	}
}

func TestLocalPageEnterOpensFileBrowser(t *testing.T) {
	m := newLocalInlineModel()
	dir := t.TempDir()
	m.fileBrowser.dir = dir
	m.replaceProviderLists(nil)

	m.openProviderList(0)

	if !m.fileBrowser.visible || !m.fileBrowser.resumeLocal {
		t.Fatalf("browser visible=%v resume=%v, want both true", m.fileBrowser.visible, m.fileBrowser.resumeLocal)
	}
	if m.fileBrowser.dir != dir {
		t.Fatalf("dir = %q, want %q", m.fileBrowser.dir, dir)
	}
}

func TestMainNOpensLocalFileBrowser(t *testing.T) {
	m := newLocalInlineModel()
	m.focus = focusPlaylist
	m.fileBrowser.dir = t.TempDir()

	m.handleMainKey(tea.KeyPressMsg{Text: "N"})

	if !m.fileBrowser.visible || !m.fileBrowser.resumeLocal {
		t.Fatal("N on Local should open the file browser")
	}
}

func TestEscRestoresLocalFileBrowser(t *testing.T) {
	m := newLocalInlineModel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.mp3"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.mp3"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.fileBrowser.dir = dir
	m.openFileBrowser()
	m.fileBrowser.cursor = 2
	m.fileBrowser.scroll = 1
	m.fileBrowser.selected = map[string]bool{filepath.Join(dir, "b.mp3"): true}

	if cmd := m.fbConfirm(false); cmd == nil {
		t.Fatal("fbConfirm returned nil")
	}
	if m.fileBrowser.visible {
		t.Fatal("playing should close the file browser")
	}
	if !m.fileBrowser.resumeLocal {
		t.Fatal("playing from the Local browser should keep the return point")
	}

	m.focus = focusPlaylist
	m.handleMainKey(tea.KeyPressMsg{Text: "esc", Code: tea.KeyEscape})

	if !m.fileBrowser.visible {
		t.Fatal("esc should reopen the file browser")
	}
	if m.fileBrowser.dir != dir {
		t.Fatalf("dir = %q, want %q", m.fileBrowser.dir, dir)
	}
	if m.fileBrowser.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.fileBrowser.cursor)
	}
	if m.focus != focusProvider {
		t.Fatalf("focus = %v, want focusProvider under the browser", m.focus)
	}

	m.handleFileBrowserKey(tea.KeyPressMsg{Text: "esc", Code: tea.KeyEscape})
	if m.fileBrowser.visible || m.fileBrowser.resumeLocal {
		t.Fatal("esc inside the browser should return to the Local page")
	}

	m.focus = focusPlaylist
	m.handleMainKey(tea.KeyPressMsg{Text: "esc", Code: tea.KeyEscape})
	if m.fileBrowser.visible {
		t.Fatal("esc after leaving the browser should open the Local page, not the browser")
	}
	if m.focus != focusProvider {
		t.Fatalf("focus = %v, want focusProvider", m.focus)
	}
}

func TestSwitchToLocalClearsFileResume(t *testing.T) {
	m := newLocalInlineModel()
	m.fileBrowser.resumeLocal = true
	m.focus = focusPlaylist

	m.switchProvider(0)

	if m.fileBrowser.resumeLocal {
		t.Fatal("opening the Local page should clear the file-browser return point")
	}
}

func TestOpenLocalNewPlaylistOpensNamePrompt(t *testing.T) {
	m := newLocalInlineModel()
	m.openLocalNewPlaylist()
	if m.plManager.screen != plMgrScreenNewName {
		t.Fatalf("screen = %v, want plMgrScreenNewName", m.plManager.screen)
	}
	if !m.plManager.visible {
		t.Fatal("new playlist prompt should open the playlist manager overlay")
	}
}
