package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
)

func TestEmptyStatesNameTheNextStep(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Model)
		body   func(*Model) string
		want   []string
		reject []string
	}{
		{
			name: "no tracks loaded",
			body: (*Model).renderPlaylist,
			want: []string{"No tracks loaded.", "Esc", "Back to provider", "Open file browser", "Load URL", "Ctrl+F"},
		},
		{
			name: "catalog search without results",
			setup: func(m *Model) {
				m.provider = &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Radio"}, results: []playlist.PlaylistInfo{}}
				m.provSearch.query = "zzz"
			},
			body:   func(m *Model) string { return m.renderProviderEmptyState(8) },
			want:   []string{`No results for "zzz".`, "Esc", "to clear the search."},
			reject: []string{"No playlists in"},
		},
		{
			name: "empty playlist manager",
			setup: func(m *Model) {
				m.plManager = plManagerState{visible: true, screen: plMgrScreenList}
			},
			body:   (*Model).renderPlMgrListBody,
			want:   []string{"Press Enter or a to create one.", "Press w to save the loaded tracks"},
			reject: []string{"now-playing"},
		},
		{
			name: "empty saved playlist",
			setup: func(m *Model) {
				m.plManager = plManagerState{visible: true, screen: plMgrScreenTracks, selPlaylist: "Mix"}
			},
			body:   (*Model).renderPlMgrTracksBody,
			want:   []string{"This playlist is empty.", "Press o to add files, or D to add directory sources."},
			reject: []string{"now-playing"},
		},
		{
			name: "empty favorites",
			setup: func(m *Model) {
				m.plManager = plManagerState{visible: true, screen: plMgrScreenTracks, selPlaylist: favorites.PlaylistName}
			},
			body: (*Model).renderPlMgrTracksBody,
			want: []string{"Press f on a track in the playlist to add it here."},
		},
		{
			name: "new playlist name",
			setup: func(m *Model) {
				m.plManager = plManagerState{visible: true, screen: plMgrScreenNewName}
			},
			body:   (*Model).renderPlMgrFormBody,
			want:   []string{"Enter creates an empty playlist, then opens the file browser."},
			reject: []string{"Create & add"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.layout.panelWidth = 80
			m.plVisible = 10
			if tt.setup != nil {
				tt.setup(&m)
			}
			body := stripAnsi(tt.body(&m))
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("body = %q, want %q", body, want)
				}
			}
			for _, reject := range tt.reject {
				if strings.Contains(body, reject) {
					t.Errorf("body = %q, must not contain %q", body, reject)
				}
			}
		})
	}
}

func TestProviderEmptyStateHints(t *testing.T) {
	tests := []struct {
		provider, want string
	}{
		{"Qobuz", "cliamp qobuz reset"},
		{"Tidal", "cliamp tidal reset"},
		{"Mixcloud", "[mixcloud] username"},
		{"lyrion", "Press N to browse."},
		{"Yandex Music", "[yandex] token"},
		{"Radio", "reload the station directory"},
		{"Podcasts", "reload the top shows"},
		{"cliamp radio", "pick another provider"},
		{"YouTube", "yt-dlp"},
		{"YouTube (All)", "yt-dlp"},
		{"YouTube Music", "cliamp setup"},
		{"NetEase", "cliamp setup"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			m := Model{provider: commandsTestProvider{name: tt.provider}}
			if body := stripAnsi(m.renderProviderEmptyState(8)); !strings.Contains(body, tt.want) {
				t.Fatalf("empty state = %q, want %q", body, tt.want)
			}
		})
	}
}

// cliamp has no ytmusic-login command. The YouTube hints name cliamp setup.
// The hint for a YouTube that did not register also names yt-dlp, which the
// YouTube providers need. See issue #428.
func TestYouTubeHintsNameTheSetupPath(t *testing.T) {
	tests := []struct {
		name string
		hint string
		want []string
	}{
		{"shortcut yt", unconfiguredProviderHint("yt"), []string{"cliamp setup", "yt-dlp"}},
		{"shortcut youtube", unconfiguredProviderHint("youtube"), []string{"cliamp setup", "yt-dlp"}},
		{"shortcut ytmusic", unconfiguredProviderHint("ytmusic"), []string{"cliamp setup", "yt-dlp"}},
		{"empty YouTube Music", providerEmptyStateHint["youtube music"], []string{"cliamp setup"}},
		{"empty ytmusic", providerEmptyStateHint["ytmusic"], []string{"cliamp setup"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, want := range tt.want {
				if !strings.Contains(tt.hint, want) {
					t.Errorf("hint = %q, want %q", tt.hint, want)
				}
			}
		})
	}
	t.Run("no hint names ytmusic-login", func(t *testing.T) {
		hints := []string{unconfiguredProviderHint("yt"), unconfiguredProviderHint("youtube"), unconfiguredProviderHint("ytmusic")}
		for _, hint := range providerEmptyStateHint {
			hints = append(hints, hint)
		}
		for _, hint := range hints {
			if strings.Contains(hint, "ytmusic-login") {
				t.Errorf("hint = %q, names the missing command ytmusic-login", hint)
			}
		}
	})
}

// TestEmptyPlaylistManagerKeysDoWhatTheTextSays ties the empty-state text to
// the key handlers: a opens the name input, which creates an empty playlist.
func TestEmptyPlaylistManagerKeysDoWhatTheTextSays(t *testing.T) {
	m := keybindingTestModel()
	m.plManager = plManagerState{visible: true, screen: plMgrScreenList}

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "a"})

	if m.plManager.screen != plMgrScreenNewName || m.plManager.newName != "" {
		t.Fatalf("a on the empty list: screen=%v name=%q, want an empty name input", m.plManager.screen, m.plManager.newName)
	}
}
