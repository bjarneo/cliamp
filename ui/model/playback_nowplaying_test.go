package model

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
)

// nowPlayingProv records ReportNowPlaying calls, which nowPlaying issues
// alongside the plugin track.change event.
type nowPlayingProv struct {
	plainProv
	reports chan playlist.Track
}

func newTrackChangeTestPlugin(t *testing.T) (*luaplugin.Manager, <-chan string, func()) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", configDir)
	pluginDir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(pluginDir, "track-change.lua")
	const script = `
local p = plugin.register({name = "track-change", type = "hook"})
p:on("track.change", function(track)
    cliamp.message(track.path .. "\n" .. track.artist .. "\n" .. track.title)
end)
`
	if err := os.WriteFile(pluginPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugintrust.Approve(pluginDir, "track-change", pluginPath); err != nil {
		t.Fatal(err)
	}
	mgr, err := luaplugin.New(nil, nil)
	closePlugins := sync.OnceFunc(mgr.Close)
	t.Cleanup(closePlugins)
	if err != nil {
		t.Fatal(err)
	}
	if !mgr.HasHook(luaplugin.EventTrackChange) {
		t.Fatal("test plugin did not register track.change")
	}
	messages := make(chan string, 4)
	ctx := t.Context()
	mgr.SetUIProvider(luaplugin.UIProvider{
		ShowMessage: func(text string, _ time.Duration) {
			select {
			case messages <- text:
			case <-ctx.Done():
			}
		},
	})
	return mgr, messages, closePlugins
}

type nowPlayingEngine struct {
	playbackFakeEngine
	startErr error
}

func (p *nowPlayingEngine) PlayAt(path string, duration, offset time.Duration) error {
	if p.startErr != nil {
		return p.startErr
	}
	return p.playbackFakeEngine.PlayAt(path, duration, offset)
}

func (p *nowPlayingEngine) PlayAtForGeneration(path string, duration, offset time.Duration, gen uint64) error {
	if gen != p.playGeneration {
		return nil
	}
	return p.PlayAt(path, duration, offset)
}

func (p *nowPlayingEngine) PlayYTDLForGeneration(path string, duration time.Duration, gen uint64) error {
	return p.PlayAtForGeneration(path, duration, 0, gen)
}

func TestPlayTrackEmitsPluginTrackChange(t *testing.T) {
	for _, path := range []string{
		"/music/local.flac",
		"https://example.com/stream.mp3",
		"https://www.youtube.com/watch?v=GBRAnuT48qo",
		"https://music.youtube.com/watch?v=GBRAnuT48qo",
		"https://soundcloud.com/artist/track",
	} {
		for _, outcome := range []string{"started", "failed", "buffering", "superseded"} {
			if !playlist.IsURL(path) && (outcome == "buffering" || outcome == "superseded") {
				continue
			}
			for _, reporter := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/reporter=%t", path, outcome, reporter), func(t *testing.T) {
					mgr, messages, closePlugins := newTrackChangeTestPlugin(t)
					track := playlist.Track{Path: path, Title: "Title", Artist: "Artist", Stream: playlist.IsURL(path)}
					pl := playlist.New()
					pl.Add(track)
					engine := &nowPlayingEngine{}
					if outcome == "failed" {
						engine.startErr = errors.New("playback startup failed")
					}
					m := Model{player: engine, playlist: pl, luaMgr: mgr}
					if reporter {
						prov := &nowPlayingProv{reports: make(chan playlist.Track, 1)}
						m.providers = []ProviderEntry{{Key: "p", Name: "P", Provider: prov}}
					}
					cmd := m.playTrack(track)
					if track.Stream && outcome != "buffering" {
						if outcome == "superseded" {
							// A second request for the same path must invalidate the first.
							m.playTrack(track)
						}
						if cmd == nil {
							t.Fatal("missing stream playback command")
						}
						msg, ok := cmd().(streamPlayedMsg)
						if !ok {
							t.Fatal("playback command did not return streamPlayedMsg")
						}
						updated, _ := m.Update(msg)
						m = updated.(Model)
					}
					if outcome == "failed" && !errors.Is(m.err, engine.startErr) {
						t.Fatalf("playback error = %v, want %v", m.err, engine.startErr)
					}
					// Close waits for every asynchronous Lua callback before assertions.
					closePlugins()
					if outcome == "started" {
						select {
						case got := <-messages:
							if want := track.Path + "\n" + track.Artist + "\n" + track.Title; got != want {
								t.Fatalf("plugin received %q, want %q", got, want)
							}
						default:
							t.Fatal("plugin did not receive track.change")
						}
					}
					select {
					case got := <-messages:
						t.Fatalf("unexpected track.change: %q", got)
					default:
					}
				})
			}
		}
	}
}

func (p *nowPlayingProv) CanReportPlayback(playlist.Track) bool { return true }

func (p *nowPlayingProv) ReportNowPlaying(t playlist.Track, _ time.Duration, _ bool) error {
	p.reports <- t
	return nil
}

func (p *nowPlayingProv) ReportScrobble(playlist.Track, time.Duration, time.Duration, bool) error {
	return nil
}

// TestPlayTrackFiresNowPlayingForEverySource checks provider reporting after
// both synchronous and asynchronous playback starts.
func TestPlayTrackFiresNowPlayingForEverySource(t *testing.T) {
	for _, path := range []string{
		"/music/local.flac",
		"https://example.com/stream.mp3",
		"https://www.youtube.com/watch?v=GBRAnuT48qo",
		"https://music.youtube.com/watch?v=GBRAnuT48qo",
		"https://soundcloud.com/artist/track",
	} {
		t.Run(path, func(t *testing.T) {
			prov := &nowPlayingProv{reports: make(chan playlist.Track, 1)}
			pl := playlist.New()
			track := playlist.Track{Path: path, Title: "T", Stream: playlist.IsURL(path)}
			pl.Add(track)
			m := Model{
				player:    &playbackFakeEngine{},
				playlist:  pl,
				providers: []ProviderEntry{{Key: "p", Name: "P", Provider: prov}},
			}
			cmd := m.playTrack(track)
			if track.Stream {
				if cmd == nil {
					t.Fatal("missing stream playback command")
				}
				m.Update(cmd())
			}
			select {
			case got := <-prov.reports:
				if got.Path != path {
					t.Fatalf("now-playing reported %q, want %q", got.Path, path)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("playTrack did not fire nowPlaying")
			}
		})
	}
}
