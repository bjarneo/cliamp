package model

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// A failed resolve reports its error and clears only the loading flag of its
// own request. A buffering stream or a provider load that is still in flight
// keeps its state.
func TestResolveFailureClearsOnlyItsOwnLoadingFlag(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.m3u")
	selectMissing := func(m *Model) {
		m.fileBrowser = fileBrowserState{
			visible:  true,
			entries:  []fbEntry{{name: "missing.m3u", path: missing}},
			selected: map[string]bool{missing: true},
		}
	}
	for _, tc := range []struct {
		name            string
		cmd             func(*Model) tea.Cmd
		wantFeedLoading bool
	}{
		{
			name: "feed track",
			cmd:  func(*Model) tea.Cmd { return resolveFeedTrackCmd("http://127.0.0.1:1/feed.m3u", 0, 0) },
		},
		{
			name: "startup urls",
			cmd:  func(*Model) tea.Cmd { return resolveRemoteCmd([]string{"http://127.0.0.1:1/list.m3u"}, true) },
		},
		{
			name: "typed url",
			cmd:  func(*Model) tea.Cmd { return resolveURLCmd(missing, true) },
		},
		{
			name: "file browser",
			cmd: func(m *Model) tea.Cmd {
				selectMissing(m)
				return m.fbConfirm(false)
			},
			wantFeedLoading: true,
		},
		{
			name: "file browser to playlist",
			cmd: func(m *Model) tea.Cmd {
				selectMissing(m)
				return m.fbConfirmToPlaylist()
			},
			wantFeedLoading: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &playbackFakeEngine{}
			m := Model{
				player:      engine,
				playlist:    playlist.New(),
				vis:         ui.NewVisualizer(float64(engine.SampleRate())),
				provPane:    providerPane{loading: true},
				feedLoading: true,
				buffering:   true,
			}
			cmd := tc.cmd(&m)
			if cmd == nil {
				t.Fatal("no resolve command")
			}

			next, _ := m.Update(cmd())
			m = next.(Model)

			if m.err == nil {
				t.Fatal("err = nil, want the resolve error")
			}
			if m.feedLoading != tc.wantFeedLoading {
				t.Fatalf("feedLoading = %v, want %v", m.feedLoading, tc.wantFeedLoading)
			}
			if !m.provPane.loading || !m.buffering || m.provPane.signIn {
				t.Fatalf("provPane.loading=%v buffering=%v provPane.signIn=%v, want the unrelated flags unchanged",
					m.provPane.loading, m.buffering, m.provPane.signIn)
			}
		})
	}
}
