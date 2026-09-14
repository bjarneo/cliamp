package model

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

func TestMetadataFields(t *testing.T) {
	for _, tt := range []struct {
		name  string
		track playlist.Track
		want  []metadataField
	}{
		{
			name: "music sanitizes terminal input",
			track: playlist.Track{
				Title: "\x1b[31mSong\x1b[0m\r\nName", Artist: " Artist\tName\x00 ",
				Album: "Album\aName", Genre: "Jazz\x01Fusion", Year: 2026, TrackNumber: 3, DurationSecs: 245,
			},
			want: []metadataField{{"Title", "Song Name"}, {"Artist", "Artist Name"}, {"Album", "Album Name"},
				{"Genre", "Jazz Fusion"}, {"Year", "2026"}, {"Track", "3"}, {"Length", "4:05"}},
		},
		{
			name: "podcast date overrides year and duplicate show is omitted",
			track: playlist.Track{
				Title: "Episode title", Artist: "The Show", Album: "The Show", Year: 2025,
				TrackNumber: 12, DurationSecs: 3661, Stream: true,
				ProviderMeta: map[string]string{"podcast.feed": "https://example.invalid/feed", "podcast.published": "2026-09-08"},
			},
			want: []metadataField{{"Title", "Episode title"}, {"Show", "The Show"}, {"Date", "2026-09-08"},
				{"Episode", "12"}, {"Length", "1:01:01"}},
		},
		{
			name: "radio optional fields",
			track: playlist.Track{
				Title: "Station", Genre: "jazz, soul", Stream: true, Realtime: true,
				ProviderMeta: map[string]string{"radio.country": "Norway", "radio.state": "Oslo", "radio.codec": "MP3", "radio.bitrate": "192"},
			},
			want: []metadataField{{"Title", "Station"}, {"Tags", "jazz, soul"}, {"Country", "Norway"},
				{"Region", "Oslo"}, {"Codec", "MP3"}, {"Bitrate", "192 kbps"}, {"Type", "Live radio"}},
		},
		{
			name:  "radio without optional fields",
			track: playlist.Track{Title: "Station", Stream: true, Realtime: true},
			want:  []metadataField{{"Title", "Station"}, {"Type", "Live radio"}},
		},
		{
			name:  "empty and unknown fields omitted",
			track: playlist.Track{Title: "\x1b[32m\x1b[0m\r\n", Artist: "\t\x00", Year: -1, TrackNumber: -1, DurationSecs: -1},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.playlist.Add(tt.track)
			if got := m.metadataFields(); !slices.Equal(got, tt.want) {
				t.Fatalf("metadata fields = %#v, want %#v", got, tt.want)
			}
		})
	}
}

type metadataTestProvider struct {
	commandsTestProvider
	fetches int
}

func (p *metadataTestProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.fetches++
	return nil, nil
}

func (p *metadataTestProvider) Tracks(string) ([]playlist.Track, error) {
	p.fetches++
	return nil, nil
}

func TestMetadataFollowsSelectionWithoutFetching(t *testing.T) {
	m := newColumnTestModel(100, 30)
	p := &metadataTestProvider{commandsTestProvider: commandsTestProvider{name: "Fixture"}}
	m.provider, m.providers[0].Provider = p, p
	playing := playlist.Track{Path: "/playing.mp3", Title: "Playing track"}
	m.player.(*playbackFakeEngine).playing = true
	m.setPlaybackTrack(playing)
	m.playlist.SetTrack(0, playing)
	for i, title := range []string{"First selection", "Next selection"} {
		m.playlist.SetTrack(i+1, playlist.Track{Path: fmt.Sprintf("https://example.invalid/%d.mp3", i), Title: title, Stream: true})
	}
	m.plCursor = 1
	m.SetShowMetadata(true)
	saver := &recordingConfigSaver{}
	m.configSaver = saver
	tracks := m.playlist.Tracks()

	for _, title := range []string{"First selection", "Next selection"} {
		for range 2 {
			pane := ansi.Strip(m.renderSettingsPane(m.effectivePlaylistVisible()))
			if !strings.Contains(pane, title) || strings.Contains(pane, playing.Title) {
				t.Fatalf("sidebar does not follow %q:\n%s", title, pane)
			}
			m.View()
		}
		if !m.showMetadata || m.showInfo || len(saver.values) != 0 || p.fetches != 0 {
			t.Fatalf("render changed metadata state or performed I/O: shown=%v info=%v saves=%v fetches=%d", m.showMetadata, m.showInfo, saver.values, p.fetches)
		}
		if got, _ := m.currentPlaybackTrack(); got.Path != playing.Path {
			t.Fatalf("inspection changed playback to %q", got.Path)
		}
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if !reflect.DeepEqual(m.playlist.Tracks(), tracks) || len(m.player.(*playbackFakeEngine).playCalls) != 0 {
		t.Fatal("inspection mutated tracks or started playback")
	}
}

func TestMetadataRadioNowPlaying(t *testing.T) {
	for _, tt := range []struct {
		name    string
		cursor  int
		playing bool
		title   string
		want    string
	}{
		{"selected active stream", 0, true, "Artist\n- \x1b[32mSong\x1b[0m", "Artist - Song"},
		{"different selected stream", 1, true, "Artist - Song", ""},
		{"stopped stream", 0, false, "Artist - Song", ""},
		{"no now-playing data", 0, true, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			station := playlist.Track{Path: "https://example.invalid/live", Title: "Station", Stream: true, Realtime: true}
			m.playlist.Add(station, playlist.Track{Path: "https://example.invalid/other", Title: "Other", Stream: true, Realtime: true})
			m.playlist.SetIndex(0)
			m.setPlaybackTrack(station)
			m.player.(*playbackFakeEngine).playing = true
			m.plCursor, m.streamTitle = tt.cursor, tt.title
			if !tt.playing {
				m.handleKey(tea.KeyPressMsg{Text: "s"})
			}
			var got string
			for _, field := range m.metadataFields() {
				if field.label == "Playing" {
					got = field.value
				}
			}
			if got != tt.want {
				t.Fatalf("Playing = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMetadataMissingSelectionAndPathPrivacy(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*Model)
		path  string
	}{
		{"nil playlist", func(m *Model) { m.playlist = nil }, ""},
		{"empty playlist", func(m *Model) { m.playlist = playlist.New() }, ""},
		{"invalid cursor", func(m *Model) { m.plCursor = m.playlist.Len() }, ""},
		{"local path only", nil, "/private/music/file.mp3"},
		{"URL only", nil, "https://example.invalid/stream?token=private"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newLayoutTestModel(100, 30)
			if tt.setup != nil {
				tt.setup(&m)
			} else {
				m.playlist.SetTrack(0, playlist.Track{Path: tt.path})
			}
			if fields := m.metadataFields(); len(fields) != 0 {
				t.Fatalf("missing metadata produced fields: %#v", fields)
			}
			pane := ansi.Strip(strings.Join(m.renderMetadataPane(8), "\n"))
			if !strings.Contains(pane, "No metadata available") || strings.Contains(pane, "Path") || tt.path != "" && strings.Contains(pane, tt.path) {
				t.Fatalf("missing-metadata sidebar = %q", pane)
			}
			info := ansi.Strip(strings.Join(m.infoLines(), "\n"))
			if tt.path != "" && !strings.Contains(info, "Path: "+tt.path) {
				t.Fatalf("full info lost path %q: %q", tt.path, info)
			}
		})
	}
}

func TestMetadataToggleDefaultsAndPersistence(t *testing.T) {
	m := newColumnTestModel(80, 24)
	saver := &recordingConfigSaver{}
	m.configSaver = saver
	before := m.layout
	if m.showMetadata || m.metadataPaneRows(20) != 0 || strings.Contains(ansi.Strip(m.View().Content), "Metadata [Ctrl+I]") {
		t.Fatal("metadata is not collapsed by default")
	}
	for _, want := range []bool{true, false} {
		updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i', Mod: tea.ModCtrl})
		m = updated.(Model)
		if cmd != nil || m.showMetadata != want || m.showInfo {
			t.Fatalf("toggle: shown=%v info=%v command=%v, want shown=%v without overlay or command", m.showMetadata, m.showInfo, cmd != nil, want)
		}
		if len(saver.values) != 1 || saver.values["show_metadata"] != strconv.FormatBool(want) {
			t.Fatalf("saved config = %v, want only show_metadata=%v", saver.values, want)
		}
		if got := strings.Contains(ansi.Strip(m.View().Content), "Metadata [Ctrl+I]"); got != want {
			t.Fatalf("metadata header visible = %v, want %v", got, want)
		}
	}
	if m.layout != before {
		t.Fatalf("closing metadata changed layout: %+v, want %+v", m.layout, before)
	}
	for _, key := range []string{"i", "ctrl+i"} {
		if !ReservedKeys()[key] {
			t.Errorf("metadata key %q is not reserved", key)
		}
	}
	if ReservedKeys()["I"] {
		t.Error("the removed Shift+I shortcut is still reserved")
	}
}

func TestMetadataShortcutLeavesTabNavigationIntact(t *testing.T) {
	for _, tt := range []struct {
		key  tea.KeyPressMsg
		want focusArea
	}{
		{tea.KeyPressMsg{Code: tea.KeyTab}, focusProvPill},
		{tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, focusSpeed},
		{tea.KeyPressMsg{Text: "I"}, focusPlaylist},
		{tea.KeyPressMsg{Code: 'i', ShiftedCode: 'I', Mod: tea.ModShift}, focusPlaylist},
	} {
		t.Run(tt.key.String(), func(t *testing.T) {
			m := newColumnTestModel(80, 24)
			saver := &recordingConfigSaver{}
			m.configSaver = saver
			updated, _ := m.Update(tt.key)
			m = updated.(Model)
			if m.focus != tt.want || m.showMetadata || m.showInfo || len(saver.values) != 0 {
				t.Fatalf("%s: focus=%s metadata=%v info=%v saves=%v", tt.key.String(), m.focus.label(), m.showMetadata, m.showInfo, saver.values)
			}
		})
	}
}

func TestMetadataLayoutAndFocusBudget(t *testing.T) {
	for _, tt := range []struct {
		width, height, visRows int
		borrow                 bool
	}{
		{80, 24, 12, true}, {100, 30, 12, true}, {160, 48, 12, false}, {80, 24, 1, false},
	} {
		t.Run(fmt.Sprintf("%dx%d/vis=%d", tt.width, tt.height, tt.visRows), func(t *testing.T) {
			m := newColumnTestModel(tt.width, tt.height)
			m.SetVisRows(tt.visRows)
			m.playlist.SetTrack(0, playlist.Track{Title: strings.Repeat("\u754ce\u0301", 40), Artist: "Artist", Album: "Album",
				Genre: "Genre", Year: 2026, TrackNumber: 7, DurationSecs: 245})
			before, visible := m.layout, m.plVisible
			saver := &recordingConfigSaver{}
			m.configSaver = saver
			m.SetShowMetadata(true)
			if !m.layout.twoColumn || m.showInfo || m.visRows != tt.visRows || m.vis.Rows < 1 {
				t.Fatalf("metadata layout: twoColumn=%v info=%v configured=%d canvas=%d", m.layout.twoColumn, m.showInfo, m.visRows, m.vis.Rows)
			}
			if got := m.layout.visualizerRows < before.visualizerRows; got != tt.borrow {
				t.Fatalf("borrowed visualizer rows = %v, want %v", got, tt.borrow)
			}
			if m.layout.bodyRows-before.bodyRows != before.visualizerRows-m.layout.visualizerRows {
				t.Fatal("borrowed visualizer rows did not reach the body")
			}
			rows := m.effectivePlaylistVisible()
			pane := m.renderSettingsPane(rows)
			if !strings.Contains(ansi.Strip(pane), "Metadata [Ctrl+I]") {
				t.Fatalf("metadata absent from sidebar:\n%s", ansi.Strip(pane))
			}
			assertViewFits(t, pane, m.layout.settingsWidth, rows)
			assertViewFits(t, m.renderBodyRegion(), m.layout.panelWidth, rows)
			assertViewFits(t, m.View().Content, tt.width, tt.height)
			want := []focusArea{focusPlaylist, focusProvPill, focusVolume, focusEQ, focusShuffle, focusRepeat, focusSpeed}
			if got := m.mainFocusAreas(); !slices.Equal(got, want) {
				t.Fatalf("metadata changed Tab stops: %v, want %v", got, want)
			}
			for _, reverse := range []bool{false, true} {
				key := tea.KeyPressMsg{Code: tea.KeyTab}
				if reverse {
					key.Mod = tea.ModShift
				}
				for i := 1; i <= len(want); i++ {
					idx := i % len(want)
					if reverse {
						idx = (len(want) - idx) % len(want)
					}
					updated, _ := m.Update(key)
					m = updated.(Model)
					if m.focus != want[idx] {
						t.Fatalf("%s step %d: focus=%s, want %s", key.String(), i, m.focus.label(), want[idx].label())
					}
				}
			}
			m.SetShowMetadata(false)
			if m.layout != before || m.plVisible != visible || m.vis.Rows != before.visualizerRows || m.visRows != tt.visRows || len(saver.values) != 0 {
				t.Fatal("SetShowMetadata did not restore configured layout without saving")
			}
		})
	}
}

func TestMetadataTinyRowBudgets(t *testing.T) {
	for _, tt := range []struct{ providers, rows, metadata int }{
		{2, -1, 0}, {2, 0, 0}, {2, 1, 0}, {2, 2, 0}, {2, 6, 0}, {2, 8, 0},
		{2, 9, 3}, {2, 14, 8}, {1, 7, 0}, {1, 8, 3},
	} {
		t.Run(fmt.Sprintf("providers=%d/rows=%d", tt.providers, tt.rows), func(t *testing.T) {
			m := newColumnTestModel(100, 30)
			m.providers = m.providers[:tt.providers]
			m.SetShowMetadata(true)
			m.plVisible = tt.rows
			if got := m.metadataPaneRows(tt.rows); got != tt.metadata {
				t.Fatalf("metadata rows = %d, want %d", got, tt.metadata)
			}
			pane := ansi.Strip(m.renderSettingsPane(tt.rows))
			if tt.rows <= 0 {
				if pane != "" {
					t.Fatalf("nonpositive budget rendered %q", pane)
				}
				return
			}
			if len(strings.Split(pane, "\n")) != tt.rows || strings.Contains(pane, "Metadata [Ctrl+I]") != (tt.metadata > 0) {
				t.Fatalf("sidebar does not respect %d rows:\n%s", tt.rows, pane)
			}
			for _, setting := range []struct {
				focus focusArea
				label string
			}{{focusProvPill, "SRC"}, {focusVolume, "VOL"}, {focusEQ, "EQ"}, {focusShuffle, "SHF"}, {focusRepeat, "RPT"}, {focusSpeed, "SPD"}} {
				if got, want := m.mainFocusAllowed(setting.focus), strings.Contains(pane, setting.label); got != want {
					t.Errorf("%s focusable=%v, visible=%v", setting.label, got, want)
				}
			}
		})
	}
}

func TestMetadataFallbackAndPreference(t *testing.T) {
	for _, tt := range []struct {
		name          string
		width, height int
		setup         func(*Model)
	}{
		{"compact", 56, 16, func(*Model) {}},
		{"minimal", 40, 10, func(*Model) {}},
		{"simplified", 80, 24, func(m *Model) { m.SetSimplified(true) }},
		{"closed settings", 80, 24, func(m *Model) { m.SetHideSettingsPane(true) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newLayoutTestModel(tt.width, tt.height)
			m.SetVisRows(12)
			tt.setup(&m)
			before := m.layout
			saver := &recordingConfigSaver{}
			m.configSaver = saver
			for _, want := range []bool{true, false, true} {
				updated, _ := m.Update(tea.KeyPressMsg{Code: 'i', Mod: tea.ModCtrl})
				m = updated.(Model)
				if m.showMetadata != want || m.showInfo != want || saver.values["show_metadata"] != strconv.FormatBool(want) {
					t.Fatalf("fallback toggle: metadata=%v info=%v saved=%v, want %v", m.showMetadata, m.showInfo, saver.values, want)
				}
				assertViewFits(t, m.View().Content, tt.width, tt.height)
			}
			updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			m = updated.(Model)
			if m.showInfo || !m.showMetadata || saver.values["show_metadata"] != "true" || m.layout != before || m.visRows != 12 {
				t.Fatal("Esc did not retain preference and restore the non-sidebar layout")
			}
			if strings.Contains(ansi.Strip(m.View().Content), "Metadata [Ctrl+I]") {
				t.Fatal("metadata sidebar appeared outside the full two-column layout")
			}
			updated, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 48})
			m = updated.(Model)
			m.SetSimplified(false)
			m.SetHideSettingsPane(false)
			if m.showInfo || !m.showMetadata || !strings.Contains(ansi.Strip(m.View().Content), "Metadata [Ctrl+I]") {
				t.Fatal("saved preference did not restore metadata in the wide sidebar")
			}
		})
	}
}

func TestMetadataMoreDetailsFromSettingsFocus(t *testing.T) {
	for _, focus := range []focusArea{focusPlaylist, focusProvPill, focusVolume, focusEQ, focusShuffle, focusRepeat, focusSpeed} {
		t.Run(focus.label(), func(t *testing.T) {
			m := newColumnTestModel(80, 24)
			m.focus, m.plCursor = focus, 1
			m.playlist.SetTrack(1, playlist.Track{Path: "/selected.mp3", Title: "Selected", Artist: "Artist", Album: "Album",
				Genre: "Genre", Year: 2026, TrackNumber: 2, DurationSecs: 123})
			m.handleKey(tea.KeyPressMsg{Code: 'i', Mod: tea.ModCtrl})
			if !m.showMetadata || m.showInfo || m.focus != focus {
				t.Fatal("Ctrl+I failed to open metadata without changing setting focus")
			}
			if pane := ansi.Strip(m.renderSettingsPane(m.effectivePlaylistVisible())); !strings.Contains(pane, "i: more details") {
				t.Fatalf("truncated sidebar omitted the full-info hint:\n%s", pane)
			}
			m.infoScroll = 99
			updated, _ := m.Update(tea.KeyPressMsg{Text: "i"})
			m = updated.(Model)
			if !m.showInfo || !m.showMetadata || m.infoScroll != 0 || m.plCursor != 1 {
				t.Fatalf("i from %s: info=%v metadata=%v scroll=%d cursor=%d", focus.label(), m.showInfo, m.showMetadata, m.infoScroll, m.plCursor)
			}
			if info := ansi.Strip(m.renderInfoBody()); !strings.Contains(info, "Title: Selected") {
				t.Fatalf("full info does not describe the highlighted track:\n%s", info)
			}
			for range len(m.infoLines()) {
				updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				m = updated.(Model)
			}
			if info := ansi.Strip(m.renderInfoBody()); m.infoScroll == 0 || !strings.Contains(info, "Path: /selected.mp3") {
				t.Fatalf("full info did not scroll to Path: scroll=%d\n%s", m.infoScroll, info)
			}
			updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			m = updated.(Model)
			if m.showInfo || !m.showMetadata || m.focus != focus || !m.layout.twoColumn {
				t.Fatal("Esc did not return to the focused setting with metadata still shown")
			}
		})
	}
}

func TestMetadataKeepsEQEditingVisible(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}} {
		m := newColumnTestModel(size[0], size[1])
		m.SetShowMetadata(true)
		m.focus = focusEQ
		for _, key := range []tea.KeyPressMsg{{Code: tea.KeyRight}, {Code: tea.KeyUp}} {
			m.handleKey(key)
			want := fmt.Sprintf("%s %+.0fdB", eqBandLabels[m.eqCursor], m.player.EQBands()[m.eqCursor])
			pane := ansi.Strip(m.renderSettingsPane(m.effectivePlaylistVisible()))
			if !strings.Contains(pane, want) {
				t.Fatalf("%dx%d: focused EQ lost selected band/gain %q:\n%s", size[0], size[1], want, pane)
			}
		}
	}
}

func TestMetadataKeyStaysLiteralInTextInputs(t *testing.T) {
	for _, tt := range []struct {
		name string
		open func(*Model) *string
	}{
		{"playlist search", func(m *Model) *string { m.search.active = true; return &m.search.query }},
		{"provider filter", func(m *Model) *string { m.provSearch.active = true; return &m.provSearch.query }},
		{"provider search", func(m *Model) *string { m.spotSearch.visible = true; return &m.spotSearch.query }},
		{"URL", func(m *Model) *string { m.urlInputting = true; return &m.urlInput }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			saver := &recordingConfigSaver{}
			m.configSaver = saver
			input := tt.open(&m)
			for _, key := range []tea.KeyPressMsg{{Text: "I"}, {Code: 'i', ShiftedCode: 'I', Mod: tea.ModShift}} {
				if cmd := m.handleKey(key); cmd != nil {
					t.Fatal("typing I scheduled a command")
				}
			}
			m.handleKey(tea.KeyPressMsg{Code: 'i', Mod: tea.ModCtrl})
			if *input != "II" || m.showMetadata || m.showInfo || len(saver.values) != 0 {
				t.Fatalf("input=%q metadata=%v info=%v saves=%v", *input, m.showMetadata, m.showInfo, saver.values)
			}
		})
	}
}

func TestMetadataDoesNotEnableDisabledVisualizer(t *testing.T) {
	m := newColumnTestModel(80, 24)
	m.vis.Mode = ui.VisNone
	m.recomputeLayout()
	before := m.layout
	m.SetShowMetadata(true)
	if m.layout.visualizerRows != 0 || m.layout.bodyRows != before.bodyRows || m.vis.Mode != ui.VisNone {
		t.Fatal("metadata changed a disabled visualizer or its row budget")
	}
	assertViewFits(t, m.View().Content, 80, 24)
}
