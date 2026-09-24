package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
)

// stopSpyPublisher records what plugins publish through p:publish.
type stopSpyPublisher struct{ published chan string }

func (c *stopSpyPublisher) Publish(topic string, data json.RawMessage, _ bool) error {
	c.published <- topic + " " + string(data)
	return nil
}

func (c *stopSpyPublisher) ClearPrefix(string) {}

// TestExplicitStopEmitsPlaybackStop loads a real plugin that republishes the
// playback.stop event and checks that each explicit stop path delivers it,
// while running past the last track does not.
func TestExplicitStopEmitsPlaybackStop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `local p = plugin.register({name = "stopspy", type = "hook"})
p:on("playback.stop", function() p:publish("stopped", {}) end)`
	path := filepath.Join(pluginDir, "stopspy.lua")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugintrust.Approve(pluginDir, "stopspy", path); err != nil {
		t.Fatal(err)
	}
	pub := &stopSpyPublisher{published: make(chan string, 4)}
	mgr, err := luaplugin.New(nil, pub)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Close)
	if !mgr.HasHook(luaplugin.EventPlaybackStop) {
		t.Fatal("spy plugin did not register a playback.stop hook")
	}

	last := playlist.Track{Path: "https://www.youtube.com/watch?v=abc", Title: "Last"}
	newModel := func() Model {
		pl := playlist.New()
		pl.Add(playlist.Track{Path: "/music/first.flac"}, last)
		pl.SetIndex(1)
		m := Model{player: &playbackFakeEngine{playing: true}, playlist: pl, luaMgr: mgr}
		m.setPlaybackTrack(last)
		return m
	}

	stops := []struct {
		name string
		stop func(*Model)
	}{
		{"media controls", func(m *Model) {
			updated, _ := m.Update(playback.StopMsg{})
			*m = updated.(Model)
		}},
		{"stop key", func(m *Model) { m.handleKey(tea.KeyPressMsg{Text: "s"}) }},
		{"IPC", func(m *Model) {
			jobs := ipc.NewJobStore()
			job, err := jobs.Create("stop")
			if err != nil {
				t.Fatal(err)
			}
			updated, _ := m.Update(V2RequestMsg{Request: ipc.V2Request{Operation: "stop"}, Jobs: jobs, JobID: job.ID})
			*m = updated.(Model)
		}},
	}
	for _, tc := range stops {
		m := newModel()
		tc.stop(&m)
		select {
		case got := <-pub.published:
			if !strings.HasPrefix(got, "plugin.") || !strings.Contains(got, ".stopped ") {
				t.Fatalf("%s: published %q, want a stopped topic", tc.name, got)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: playback.stop was not delivered to the plugin", tc.name)
		}
	}

	// Running past the last track stops playback too, but that is the queue
	// ending, not the user stopping, so nothing is published.
	m := newModel()
	if cmd := m.nextTrack(); cmd != nil {
		t.Fatal("nextTrack past the last track returned a command, want nil")
	}
	select {
	case got := <-pub.published:
		t.Fatalf("end of queue published %q, want nothing", got)
	case <-time.After(200 * time.Millisecond):
	}
}
