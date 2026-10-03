package model

import (
	"fmt"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// overlayOpeners sets the flag that opens each overlay in overlayStack.
var overlayOpeners = map[topLevelScreen]func(*Model){
	screenFullVisualizer:  func(m *Model) { m.fullVis = true },
	screenKeymap:          func(m *Model) { m.keymap.visible = true },
	screenDevicePicker:    func(m *Model) { m.devicePicker.visible = true },
	screenPlaylistPicker:  func(m *Model) { m.plPicker.visible = true },
	screenFileBrowser:     func(m *Model) { m.fileBrowser.visible = true },
	screenSearchOverlay:   func(m *Model) { m.searchOverlay.visible = true },
	screenNavBrowser:      func(m *Model) { m.navBrowser.visible = true },
	screenThemePicker:     func(m *Model) { m.themePicker.visible = true },
	screenVisPicker:       func(m *Model) { m.visPicker.visible = true },
	screenPlaylistManager: func(m *Model) { m.plManager.visible = true },
	screenQueue:           func(m *Model) { m.queue.visible = true },
	screenSubs:            func(m *Model) { m.subs.visible = true },
	screenInfo:            func(m *Model) { m.info.visible = true },
	screenLyrics:          func(m *Model) { m.lyrics.visible = true },
	screenJump:            func(m *Model) { m.jump.active = true },
	screenURLInput:        func(m *Model) { m.urlInput.active = true },
	screenSearch:          func(m *Model) { m.search.active = true },
	screenNetSearch:       func(m *Model) { m.netSearch.active = true },
}

// probeOverlayStack replaces overlayStack for the test with specs that record
// which overlay got a key or a paste and that name their screen number. Each
// probe keeps the screen, and a paste handler, a context and a view only where
// the real spec has one.
func probeOverlayStack(t *testing.T) (keys, pastes *[]topLevelScreen) {
	t.Helper()
	keys, pastes = new([]topLevelScreen), new([]topLevelScreen)
	probes := make([]overlaySpec, len(overlayStack))
	for i, spec := range overlayStack {
		screen := spec.screen
		probe := overlaySpec{
			screen: screen,
			key: func(*Model, tea.KeyPressMsg) tea.Cmd {
				*keys = append(*keys, screen)
				return nil
			},
		}
		if spec.paste != nil {
			probe.paste = func(*Model, string) { *pastes = append(*pastes, screen) }
		}
		if spec.context != nil {
			mode, _ := spec.context(&Model{})
			probe.context = func(*Model) (commandMode, string) { return mode, fmt.Sprint("context ", screen) }
		}
		if spec.view.body != nil {
			probe.view = overlayView{
				header: func(*Model) string { return fmt.Sprint("header ", screen) },
				body:   func(*Model) string { return fmt.Sprint("body ", screen) },
			}
		}
		probes[i] = probe
	}
	saved := overlayStack
	overlayStack = probes
	t.Cleanup(func() { overlayStack = saved })
	return keys, pastes
}

// TestOverlayRoutesAgree opens each overlay alone and each pair of overlays.
// The render, key and paste routes must all pick the overlay nearest the top
// of overlayStack.
func TestOverlayRoutesAgree(t *testing.T) {
	for _, spec := range overlayStack {
		if overlayOpeners[spec.screen] == nil {
			t.Fatalf("overlayOpeners has no opener for screen %d", spec.screen)
		}
	}
	keys, pastes := probeOverlayStack(t)

	type combo struct {
		open []topLevelScreen
		top  overlaySpec
		// under is the overlay below the top one, if any.
		under *overlaySpec
	}
	var combos []combo
	for i, upper := range overlayStack {
		combos = append(combos, combo{open: []topLevelScreen{upper.screen}, top: upper})
		for _, lower := range overlayStack[i+1:] {
			// Open the lower overlay first, as a user would.
			combos = append(combos, combo{[]topLevelScreen{lower.screen, upper.screen}, upper, &lower})
		}
	}

	for _, c := range combos {
		t.Run(fmt.Sprint(c.open), func(t *testing.T) {
			var m Model
			for _, screen := range c.open {
				overlayOpeners[screen](&m)
			}
			want := c.top.screen

			if got := m.activeScreen(); got != want {
				t.Fatalf("activeScreen() = %d, want %d", got, want)
			}
			if c.top.view.body == nil {
				if _, ok := m.activeOverlay(); ok {
					t.Fatal("activeOverlay() ok = true for an overlay that replaces the frame")
				}
			} else {
				for name, got := range map[string]string{
					"header": m.renderPlaylistHeader(),
					"body":   m.renderMainBody(),
				} {
					if w := fmt.Sprint(name, " ", want); got != w {
						t.Errorf("%s = %q, want %q", name, got, w)
					}
				}
			}

			// The top overlay alone decides the frame, whatever lies under it.
			alone := Model{width: 80, height: 30}
			overlayOpeners[want](&alone)
			m.width, m.height = 80, 30
			m.recomputeLayout()
			alone.recomputeLayout()
			if m.layout != alone.layout {
				t.Errorf("layout = %+v, want %+v as with the top overlay alone", m.layout, alone.layout)
			}

			// An overlay with no context leaves the main screen context.
			wantMode, wantName := commandModeMain, "Playlist"
			if c.top.context != nil {
				wantMode, wantName = c.top.context(&m)
			}
			if mode, name := m.commandContext(); mode != wantMode || name != wantName {
				t.Errorf("commandContext() = %v %q, want %v %q", mode, name, wantMode, wantName)
			}
			if got, w := m.renderHelp(), m.commandHelp(wantMode); got != w {
				t.Errorf("renderHelp() = %q, want %q", got, w)
			}
			// The keymap lists the context under it.
			if want == screenKeymap && c.under != nil {
				if _, name := m.keymapContext(); name != fmt.Sprint("context ", c.under.screen) {
					t.Errorf("keymapContext() name = %q, want the context of screen %d", name, c.under.screen)
				}
			}

			*keys, *pastes = nil, nil
			m.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
			if !slices.Equal(*keys, []topLevelScreen{want}) {
				t.Errorf("key went to %v, want [%d]", *keys, want)
			}
			m.handlePaste("x")
			var wantPaste []topLevelScreen
			if c.top.paste != nil {
				wantPaste = []topLevelScreen{want}
			}
			if !slices.Equal(*pastes, wantPaste) {
				t.Errorf("paste went to %v, want %v", *pastes, wantPaste)
			}
		})
	}
}

// TestOverlayStackOrder checks that overlayStack holds each overlay screen
// once and never the main screen. It also pins the stack order where one
// overlay opens from inside another, and the queue and subscriptions order
// that was chosen.
func TestOverlayStackOrder(t *testing.T) {
	counts := map[topLevelScreen]int{}
	for _, spec := range overlayStack {
		counts[spec.screen]++
	}
	if counts[screenMain] != 0 {
		t.Errorf("overlayStack holds the main screen %d times", counts[screenMain])
	}
	for screen := screenMain + 1; screen <= screenFullVisualizer; screen++ {
		if counts[screen] != 1 {
			t.Errorf("overlayStack holds screen %d %d times, want 1", screen, counts[screen])
		}
	}

	index := func(screen topLevelScreen) int {
		t.Helper()
		i := slices.IndexFunc(overlayStack, func(s overlaySpec) bool { return s.screen == screen })
		if i < 0 {
			t.Fatalf("overlayStack has no screen %d", screen)
		}
		return i
	}
	above := []struct {
		name         string
		upper, lower topLevelScreen
	}{
		{"playlist picker over playlist manager", screenPlaylistPicker, screenPlaylistManager},
		{"playlist picker over file browser", screenPlaylistPicker, screenFileBrowser},
		{"file browser over playlist manager", screenFileBrowser, screenPlaylistManager},
		{"provider search over nav browser", screenSearchOverlay, screenNavBrowser},
		{"YouTube search over nav browser", screenNetSearch, screenNavBrowser},
		{"queue over subscriptions", screenQueue, screenSubs},
	}
	for _, tc := range above {
		if index(tc.upper) >= index(tc.lower) {
			t.Errorf("%s: screen %d is not above screen %d", tc.name, tc.upper, tc.lower)
		}
	}
	// The full-screen visualizer replaces the frame, and the keymap opens
	// over any other overlay.
	if overlayStack[0].screen != screenFullVisualizer || overlayStack[1].screen != screenKeymap {
		t.Errorf("overlayStack starts with screens %d and %d, want the full-screen visualizer and the keymap",
			overlayStack[0].screen, overlayStack[1].screen)
	}
}

// TestNavBrowserSearchFallbackOpensOnTop presses Ctrl+F in the nav browser of
// a provider with no search. The YouTube search opens over the browser, as
// provider search does, and Esc returns to the browser.
func TestNavBrowserSearchFallbackOpensOnTop(t *testing.T) {
	p := &readOnlyGenreProvider{}
	m := Model{provider: p, playlist: playlist.New(), plVisible: 10}
	m.openNavBrowserWith(p)

	m.handleKey(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !m.netSearch.active {
		t.Fatal("Ctrl+F did not open the YouTube search")
	}
	if got := m.activeScreen(); got != screenNetSearch {
		t.Fatalf("activeScreen() = %d, want the YouTube search %d", got, screenNetSearch)
	}
	m.handleKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if m.netSearch.query != "a" {
		t.Fatalf("netSearch.query = %q, want %q", m.netSearch.query, "a")
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := m.activeScreen(); got != screenNavBrowser {
		t.Fatalf("activeScreen() after Esc = %d, want the nav browser %d", got, screenNavBrowser)
	}
}

// TestGlobalKeysReachEveryOverlay checks that Ctrl+C and Ctrl+K work over each
// overlay. handleKey handles both keys before it asks the top overlay, so the
// overlay handlers have no clauses for them.
func TestGlobalKeysReachEveryOverlay(t *testing.T) {
	for _, spec := range overlayStack {
		t.Run(fmt.Sprint("ctrl+c ", spec.screen), func(t *testing.T) {
			m := Model{player: &playbackFakeEngine{}, playlist: playlist.New()}
			overlayOpeners[spec.screen](&m)
			if cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil || !m.quitting {
				t.Fatalf("Ctrl+C over screen %d did not quit", spec.screen)
			}
		})
		if spec.screen == screenKeymap {
			continue
		}
		t.Run(fmt.Sprint("ctrl+k ", spec.screen), func(t *testing.T) {
			m := Model{player: &playbackFakeEngine{}, playlist: playlist.New()}
			overlayOpeners[spec.screen](&m)
			m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
			if got := m.activeScreen(); got != screenKeymap {
				t.Fatalf("Ctrl+K over screen %d gave screen %d, want the keymap", spec.screen, got)
			}
		})
	}
}

// The playlist picker opens over the playlist manager to name a new
// playlist. Its name screen keeps the visualizer, as it does alone.
func TestPickerNameScreenKeepsTheVisualizerOverTheManager(t *testing.T) {
	tests := []struct {
		name  string
		under func(m *Model)
	}{
		{name: "alone", under: func(*Model) {}},
		{name: "over the manager tracks", under: func(m *Model) {
			m.plManager = plManagerState{visible: true, screen: plMgrScreenTracks}
		}},
		{name: "over the manager list", under: func(m *Model) {
			m.plManager = plManagerState{visible: true, screen: plMgrScreenList}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{width: 80, height: 30, playlist: playlist.New(), vis: ui.NewVisualizer(44100)}
			tt.under(&m)
			m.plPicker = playlistPickerState{visible: true, screen: plPickerNewName}
			m.recomputeLayout()

			if m.usesContentFirstLayout() {
				t.Fatal("usesContentFirstLayout() = true, want the playback chrome")
			}
			if m.layout.visualizerRows == 0 {
				t.Fatal("visualizerRows = 0, want the visualizer kept")
			}
		})
	}
}
