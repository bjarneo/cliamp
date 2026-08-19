package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestRenderQueueBodyPreservesVisibleQueuePositions(t *testing.T) {
	withFrameWidth(t, 80)
	p := playlist.New()
	for i := range 10 {
		p.Add(playlist.Track{Title: fmt.Sprintf("Track %d", i+1)})
		p.Queue(i)
	}
	m := Model{
		playlist:  p,
		plVisible: 3,
		queue: queueOverlay{
			cursor: 5,
			scroll: 4,
		},
	}

	got := strings.Split(stripAnsi(m.renderQueueBody()), "\n")
	want := []string{
		"  5. Track 5",
		"> 6. Track 6",
		"  7. Track 7",
	}
	if len(got) != len(want) {
		t.Fatalf("renderQueueBody() rows = %d, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("renderQueueBody() row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPlaylistManagerCachesMissingLocalStateOutsideRender(t *testing.T) {
	withFrameWidth(t, 80)
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.mp3")
	missing := filepath.Join(dir, "missing.mp3")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	m := Model{plVisible: 5, plManager: plManagerState{screen: plMgrScreenTracks}}
	m.plMgrLoadTracks([]playlist.Track{
		{Path: existing, Title: "Existing"},
		{Path: missing, Title: "Missing"},
		{Path: "https://example.com/live", Title: "URL"},
		{Path: "/not/local", Title: "Stream", Stream: true},
		{Path: "ssh://example.com/music.mp3", Title: "SSH"},
	})
	wantMissing := []bool{false, true, false, false, false}
	for i, want := range wantMissing {
		if got := m.plManager.missingLocal[i]; got != want {
			t.Fatalf("missingLocal[%d] = %t, want %t", i, got, want)
		}
	}

	before := stripAnsi(m.renderPlMgrTracksBody())
	if !strings.Contains(before, "! 2. Missing") {
		t.Fatalf("missing track marker absent from render: %q", before)
	}
	if strings.Contains(before, "! 1. Existing") {
		t.Fatalf("existing track marked missing: %q", before)
	}

	if err := os.WriteFile(missing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := stripAnsi(m.renderPlMgrTracksBody()); got != before {
		t.Fatalf("render refreshed filesystem state instead of using cache\nbefore: %q\nafter:  %q", before, got)
	}

	m.plMgrRefreshMissingLocal()
	afterCreate := stripAnsi(m.renderPlMgrTracksBody())
	if strings.Contains(afterCreate, "! 2. Missing") {
		t.Fatalf("cache refresh still marks created track missing: %q", afterCreate)
	}

	if err := os.Remove(existing); err != nil {
		t.Fatal(err)
	}
	if got := stripAnsi(m.renderPlMgrTracksBody()); got != afterCreate {
		t.Fatalf("render refreshed removed file instead of using cache\nbefore: %q\nafter:  %q", afterCreate, got)
	}
	m.plMgrRefreshMissingLocal()
	if got := stripAnsi(m.renderPlMgrTracksBody()); !strings.Contains(got, "! 1. Existing") {
		t.Fatalf("cache refresh did not mark removed track missing: %q", got)
	}
}

func TestPlaylistManagerEditsPreserveCachedMissingState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Model)
	}{
		{name: "remove", mutate: func(m *Model) { m.plManager.cursor = 2; m.plMgrRemoveSelectedTracks() }},
		{name: "move", mutate: func(m *Model) { m.plManager.cursor = 0; m.plMgrMoveTrack(1) }},
		{name: "sort", mutate: func(m *Model) { m.plMgrSortTracks() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			existing := filepath.Join(dir, "existing.mp3")
			missing := filepath.Join(dir, "missing.mp3")
			if err := os.WriteFile(existing, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			local := &playlistManagerTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}}
			m := Model{
				localProvider: local,
				plManager: plManagerState{
					screen:      plMgrScreenTracks,
					selPlaylist: "mix",
				},
			}
			m.plMgrLoadTracks([]playlist.Track{
				{Path: existing, Title: "B"},
				{Path: missing, Title: "A"},
				{Path: "https://example.com/stream", Title: "C", Stream: true},
			})

			if err := os.Remove(existing); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(missing, nil, 0o600); err != nil {
				t.Fatal(err)
			}

			tt.mutate(&m)
			assertMissingStateByPath(t, m.plManager.tracks, m.plManager.missingLocal, map[string]bool{
				existing: false,
				missing:  true,
			})

			m.plMgrUndoLast()
			assertMissingStateByPath(t, m.plManager.tracks, m.plManager.missingLocal, map[string]bool{
				existing: false,
				missing:  true,
			})
		})
	}
}

func assertMissingStateByPath(t *testing.T, tracks []playlist.Track, missingLocal []bool, want map[string]bool) {
	t.Helper()
	if len(missingLocal) != len(tracks) {
		t.Fatalf("missing state length = %d, want %d", len(missingLocal), len(tracks))
	}
	for i, track := range tracks {
		missing, ok := want[track.Path]
		if !ok {
			continue
		}
		if missingLocal[i] != missing {
			t.Errorf("missing state for %q = %t, want %t", track.Path, missingLocal[i], missing)
		}
	}
}
