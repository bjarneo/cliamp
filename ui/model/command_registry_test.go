package model

import (
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/external/podcast"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func registryContextLabel(t *testing.T, mode commandMode, key, staticLabel string, m Model) string {
	t.Helper()
	for _, command := range commandRegistry {
		if command.Mode&mode == 0 || command.LabelFor == nil || !slices.Contains(command.Keys, key) {
			continue
		}
		if staticLabel != "" && command.Label != staticLabel {
			continue
		}
		return command.LabelFor(m)
	}
	t.Fatalf("no LabelFor for key %q label %q in mode %d", key, staticLabel, mode)
	return ""
}

func TestReservedKeysComeFromCommandRegistry(t *testing.T) {
	reserved := ReservedKeys()
	for _, command := range commandRegistry {
		for _, key := range command.Keys {
			if !reserved[key] {
				t.Fatalf("reserved keys omit registry key %q", key)
			}
		}
	}
}

func TestProviderSearchLabel(t *testing.T) {
	podcasts := &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Podcasts"}}
	m := Model{provider: podcasts}
	if got := registryContextLabel(t, commandModeMain, "ctrl+f", "Search in current provider", m); got != "Search in Podcasts" {
		t.Fatalf("provider search label(Podcasts) = %q, want Search in Podcasts", got)
	}
	m.provider = commandsTestProvider{name: "Radio"}
	if got := registryContextLabel(t, commandModeMain, "ctrl+f", "Search in current provider", m); got != "Search in YouTube" {
		t.Fatalf("provider search label(Radio) = %q, want Search in YouTube", got)
	}
}

func TestMixcloudShortcutRegistryEntry(t *testing.T) {
	for _, command := range commandRegistry {
		if len(command.Keys) == 1 && command.Keys[0] == "X" && command.Mode&commandModeMain != 0 {
			if command.KeyLabel != "X" || command.Label != "Open Mixcloud provider" || !command.Keymap {
				t.Fatalf("Mixcloud command = %+v", command)
			}
			return
		}
	}
	t.Fatal("Mixcloud shortcut is missing from the command registry")
}

func TestPreviousEQPresetRegistryEntry(t *testing.T) {
	for _, command := range commandRegistry {
		if len(command.Keys) == 1 && command.Keys[0] == "alt+e" && command.Mode&commandModeMain != 0 {
			if command.KeyLabel != "Alt+E" || command.Label != "Previous EQ preset" || !command.Keymap {
				t.Fatalf("previous EQ preset command = %+v", command)
			}
			m := keybindingTestModel()
			if !command.enabled(m) {
				t.Fatal("previous EQ preset command is disabled in a full model")
			}
			m.simplified = true
			if command.enabled(m) {
				t.Fatal("previous EQ preset command is enabled in simplified mode")
			}
			return
		}
	}
	t.Fatal("previous EQ preset command is missing from the command registry")
}

func TestPlayPauseContextHelpLabel(t *testing.T) {
	m := keybindingTestModel()
	if got := registryContextLabel(t, commandModeMain, "space", "Pause / Resume", m); got != "Play" {
		t.Fatalf("stopped = %q, want Play", got)
	}
	eng := m.player.(*playbackFakeEngine)
	eng.playing = true
	if got := registryContextLabel(t, commandModeMain, "space", "Pause / Resume", m); got != "Pause" {
		t.Fatalf("playing = %q, want Pause", got)
	}
	eng.paused = true
	if got := registryContextLabel(t, commandModeMain, "space", "Pause / Resume", m); got != "Resume" {
		t.Fatalf("paused = %q, want Resume", got)
	}
}

func TestMainHelpBarPlayTransportHints(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	m.focus = focusPlaylist
	m.playlist.Add(playlist.Track{Title: "One"})
	m.playlist.Add(playlist.Track{Title: "Two"})
	m.playlist.SetIndex(0)
	m.plCursor = 1

	help := m.helpBar(commandModeMain)
	if strings.Contains(help, "Space") {
		t.Fatalf("stopped help should omit Space: %q", help)
	}
	if !strings.Contains(help, "Enter") || !strings.Contains(help, "Play") {
		t.Fatalf("cursor on another row should list Enter Play: %q", help)
	}

	eng := m.player.(*playbackFakeEngine)
	eng.playing = true
	help = m.helpBar(commandModeMain)
	if !strings.Contains(help, "Space") || !strings.Contains(help, "Pause") {
		t.Fatalf("playing with moved cursor should list Space Pause: %q", help)
	}
	if !strings.Contains(help, "Enter") {
		t.Fatalf("playing with moved cursor should still list Enter: %q", help)
	}

	eng.paused = true
	help = m.helpBar(commandModeMain)
	if !strings.Contains(help, "Resume") {
		t.Fatalf("paused help should list Resume: %q", help)
	}

	m.plCursor = m.playlist.Index()
	help = m.helpBar(commandModeMain)
	if strings.Contains(help, "Enter") {
		t.Fatalf("cursor on loaded row should omit Enter: %q", help)
	}
}

func TestPlayNextToggleContextHelpLabel(t *testing.T) {
	m := keybindingTestModel()
	m.playlist.Add(playlist.Track{Title: "One"})
	m.playlist.Add(playlist.Track{Title: "Two"})
	m.plCursor = 1
	wantBase := "Toggle play next (selected track will be played just after current)"
	if got := registryContextLabel(t, commandModeMain, "a", wantBase, m); got != "Mark play next" {
		t.Fatalf("unmarked = %q", got)
	}
	m.playlist.Queue(1)
	if got := registryContextLabel(t, commandModeMain, "a", wantBase, m); got != "Unmark play next" {
		t.Fatalf("marked = %q", got)
	}
}

func TestMainHelpBarEmptyPlaylist(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	m.focus = focusPlaylist
	help := m.helpBar(commandModeMain)
	for _, label := range []string{"Space", "Shift+↑↓", "Remove", "Favorite", "Add to playlist"} {
		if strings.Contains(help, label) {
			t.Fatalf("empty playlist help should not list %q: %q", label, help)
		}
	}
}

func TestContextHelpMoveAfterPrimaryOnce(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	m.playlist.Add(playlist.Track{Title: "One"})
	help := m.helpBar(commandModeMain)
	if strings.Count(help, "Shift+↑↓") != 1 {
		t.Fatalf("main help should list Move once, got %q", help)
	}
}

func TestMainPaneSaveToPlaylistHintAfterFavorite(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 160
	m.focus = focusPlaylist
	m.playlist.Add(playlist.Track{Title: "One", Path: "/a.mp3"})
	local := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
	hearts := local.useFavorites(t)
	m.localProvider, m.favStore = local, hearts
	help := m.helpBar(commandModeMain)
	if !strings.Contains(help, "Add to playlist") {
		t.Fatalf("main help should advertise Add to playlist: %q", help)
	}
	remove := strings.Index(help, "Remove")
	fav := strings.Index(help, "Favorite")
	save := strings.Index(help, "Add to playlist")
	if remove < 0 || fav < 0 || save < 0 || !(remove < fav && fav < save) {
		t.Fatalf("hint order should be Remove, Favorite, Add to playlist: %q", help)
	}
}

func TestPodcastProviderPaneHelpBar(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	m.provider = podcast.New("")
	m.focus = focusProvider
	m.provPane.lists = []playlist.PlaylistInfo{{ID: "c:https://example.com/feed", Name: "Fresh Show"}}
	m.provPane.cursor = 0
	help := m.helpBar(commandModeProvider)
	for _, want := range []string{"Enter", "Open episodes", "l", "Queue newest", "f", "Subscribe", "Ctrl+r", "Refresh", "/", "Filter", "?"} {
		if !strings.Contains(help, want) {
			t.Fatalf("podcast provider help missing %q: %q", want, help)
		}
	}
	if strings.Contains(help, "Playlists") || strings.Contains(help, "Subscriptions") {
		t.Fatalf("provider help should not show p Playlists or F Subscriptions: %q", help)
	}
	enter := strings.Index(help, "Open episodes")
	latest := strings.Index(help, "Queue newest")
	slash := strings.Index(help, "Filter")
	qm := strings.Index(help, "?")
	if !(enter >= 0 && latest > enter && slash > latest && qm > slash) {
		t.Fatalf("provider help order = %q", help)
	}
	m.provPane.lists[0] = playlist.PlaylistInfo{ID: "f:https://example.com/feed", Name: "Subscribed Show"}
	help = m.helpBar(commandModeProvider)
	if !strings.Contains(help, "Unsubscribe") {
		t.Fatalf("subscribed row should advertise Unsubscribe: %q", help)
	}
}

func TestContextHelpShowsTabCycleControlsUntilMainPane(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	m.focus = focusPlaylist
	if help := m.helpBar(commandModeMain); !strings.Contains(help, "Tab") || !strings.Contains(help, "Settings") {
		t.Fatalf("main help should advertise Tab Settings: %q", help)
	}
	for _, tc := range []struct {
		focus focusArea
		mode  commandMode
	}{
		{focusProvPill, commandModeProviderPill},
		{focusVolume, commandModeVolume},
		{focusEQ, commandModeEQ},
		{focusShuffle, commandModeShuffle},
	} {
		m.focus = tc.focus
		if help := m.helpBar(tc.mode); !strings.Contains(help, "Tab") || !strings.Contains(help, "Settings") {
			t.Fatalf("%s help should advertise Tab Settings: %q", tc.focus.label(), help)
		}
	}
	m.provider = podcast.New("")
	m.focus = focusProvider
	m.provPane.lists = []playlist.PlaylistInfo{{ID: "f:https://example.com/feed", Name: "A Show"}}
	m.provPane.cursor = 0
	if help := m.helpBar(commandModeProvider); strings.Contains(help, "Tab") {
		t.Fatalf("provider help should not show Tab: %q", help)
	}
}

func TestPlaylistManagerHelpBar(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 160
	m.plManager = plManagerState{visible: true, screen: plMgrScreenTracks, selPlaylist: "Mix"}
	m.plMgrLoadTracks([]playlist.Track{{Path: "/a.mp3", Title: "A"}})

	help := m.helpBar(commandModePlaylistManager)
	for _, want := range []string{"Esc", "Enter", "Play", "Space", "Select", "Add to queue", "Shift+↑↓", "d", "Remove", "o", "Add files", "/", "Filter", "?"} {
		if !strings.Contains(help, want) {
			t.Fatalf("tracks help %q missing %q", help, want)
		}
	}

	for _, absent := range []string{"Add dir sources", "Manage dir"} {
		if strings.Contains(help, absent) {
			t.Fatalf("tracks help should not contain %q: %q", absent, help)
		}
	}
	esc := strings.Index(help, "Esc")
	enter := strings.Index(help, "Enter")
	space := strings.Index(help, "Space")
	addQueue := strings.Index(help, "Add to queue")
	bracket := strings.Index(help, "Shift+↑↓")
	d := strings.Index(help, " d ")
	if d < 0 {
		d = strings.Index(help, "d  Remove")
	}
	o := strings.Index(help, " o ")
	slash := strings.Index(help, "/")
	qm := strings.Index(help, "?")
	if !(esc < enter && enter < space && space < addQueue && addQueue < bracket && bracket < d && d < o && o < slash && slash < qm) {
		t.Fatalf("tracks help order = %q", help)
	}

	m.plManager.screen = plMgrScreenList
	m.plManager.playlists = []playlist.PlaylistInfo{{Name: "Mix"}}
	help = m.helpBar(commandModePlaylistManager)
	if strings.Contains(help, "New playlist") {
		t.Fatalf("list help should not show a New playlist: %q", help)
	}
}

func TestFileBrowserPlaylistTargetEnterHint(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	m.fileBrowser.visible = true
	m.fileBrowser.targetPlaylist = "Mix"
	m.fileBrowser.entries = []fbEntry{{name: "song.mp3", path: "/music/song.mp3", isAudio: true}}
	m.fileBrowser.selected = map[string]bool{"/music/song.mp3": true}
	help := m.helpBar(commandModeFileBrowser)
	if strings.Contains(help, "Add to queue") {
		t.Fatalf("playlist-targeted browser should not say Add to queue: %q", help)
	}
	if !strings.Contains(help, "Add to playlist") {
		t.Fatalf("playlist-targeted browser should say Add to playlist: %q", help)
	}

	m.fileBrowser.selected = nil
	m.fileBrowser.cursor = 0
	help = m.helpBar(commandModeFileBrowser)
	if strings.Contains(help, "Play") {
		t.Fatalf("playlist-targeted browser on audio should not say Play: %q", help)
	}
	if !strings.Contains(help, "Add to playlist") {
		t.Fatalf("playlist-targeted browser on audio should say Add to playlist: %q", help)
	}
}

func TestFileBrowserHelpOrder(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth = 120
	m.fileBrowser.visible = true
	m.fileBrowser.entries = []fbEntry{
		{name: "..", isDir: true, isParent: true},
		{name: "Music", isDir: true},
		{name: "song.mp3", isAudio: true},
	}
	m.fileBrowser.cursor = 1 // folder: Enter hint hidden
	help := m.helpBar(commandModeFileBrowser)
	if strings.Contains(help, "Enter") {
		t.Fatalf("folder row should not advertise Enter: %q", help)
	}
	m.fileBrowser.selected = map[string]bool{"/music/song.mp3": true}
	help = m.helpBar(commandModeFileBrowser)
	if !strings.Contains(help, "Add to queue") {
		t.Fatalf("selection should add to queue: %q", help)
	}
	if !strings.Contains(help, "Replace queue") {
		t.Fatalf("selection should advertise Replace queue: %q", help)
	}
	m.fileBrowser.selected = nil
	m.fileBrowser.cursor = 2
	help = m.helpBar(commandModeFileBrowser)
	if !strings.Contains(help, "Add to queue") {
		t.Fatalf("audio row without selection should say Add to queue: %q", help)
	}
	for _, key := range []string{"Esc", "Space", "Enter", "?"} {
		if !strings.Contains(help, key) {
			t.Fatalf("file browser help %q missing %q", help, key)
		}
	}
	if strings.Contains(help, "Replace queue") {
		t.Fatalf("no selection should hide Replace queue: %q", help)
	}
	if strings.Contains(help, "Add to playlist") {
		t.Fatalf("no selection should hide Add to playlist: %q", help)
	}
	m.fileBrowser.selected = map[string]bool{"/music/song.mp3": true}
	help = m.helpBar(commandModeFileBrowser)
	esc := strings.Index(help, "Esc")
	space := strings.Index(help, "Space")
	enter := strings.Index(help, "Enter")
	r := strings.Index(help, "R")
	qm := strings.Index(help, "?")
	if r < 0 {
		t.Fatalf("file browser help with selection missing R: %q", help)
	}
	if !strings.Contains(help, "Add to playlist") {
		t.Fatalf("selection should show Add to playlist: %q", help)
	}
	slash := strings.Index(help, " / ")
	save := strings.Index(help, "Add to playlist")
	if !(esc < space && space < enter && enter < r && save > r && slash < qm && save < slash) {
		t.Fatalf("file browser help order = %q", help)
	}
	if strings.Contains(help, "Move cursor") || strings.Contains(help, "↑↓") {
		t.Fatalf("file browser help shows navigation: %q", help)
	}
}

func TestContextHelpAdvertisesProviderBrowsing(t *testing.T) {
	browse := trackArtistBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	m := keybindingTestModel()
	m.layout.panelWidth = 100
	m.provider = browse
	m.providers = append(m.providers, provider.Entry{Key: "mixcloud", Name: "Mixcloud", Provider: browse})
	m.playlist.Add(playlist.Track{
		Title: "A Show", Artist: "Creator",
		ProviderMeta: map[string]string{"test.creator": "creator"},
	})
	m.focus = focusPlaylist
	if help := m.helpBar(commandModeMain); strings.Contains(help, "Browse creator") || strings.Contains(help, " N ") {
		t.Fatalf("playlist help bar should not advertise N browse: %q", help)
	}

	m.focus = focusProvider
	paneBrowse := providerPaneBrowseProvider{interactionBrowseProvider{commandsTestProvider{name: "Mixcloud"}}}
	m.provider = paneBrowse
	m.provPane.lists = providerListsWithBrowse(paneBrowse, nil)
	help := m.helpBar(commandModeProvider)
	if !strings.Contains(help, "Open") {
		t.Fatalf("provider help does not advertise browse entry Open: %q", help)
	}
	if strings.Contains(help, "Browse provider") {
		t.Fatalf("provider help bar should not advertise N browse: %q", help)
	}
}

func helpBarSlotBacked(mode commandMode, key string) bool {
	if registryLists(mode, key) {
		return true
	}
	for name, handlerMode := range focusKeyHandlers {
		if handlerMode != mode {
			continue
		}
		if slices.Contains(unlistedKeys[name], key) {
			return true
		}
	}
	return false
}

func TestHelpBarKeyOrderListsEveryKeyedRow(t *testing.T) {
	for mode, keys := range helpBarKeyOrder {
		for _, key := range keys {
			if !helpBarSlotBacked(mode, key) {
				t.Errorf("mode %d helpBarKeyOrder lists %q, but no registry row or focused handler backs it", mode, key)
			}
		}
	}
}
