package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
)

// newTrackRemoveTestModel opens the track screen of the playlist "mix",
// which holds a.mp3, b.mp3 and c.mp3 in the provider and on screen.
func newTrackRemoveTestModel(t *testing.T) (Model, *playlistManagerTestProvider) {
	t.Helper()
	tracks := []playlist.Track{
		{Path: "/music/a.mp3", Title: "A"},
		{Path: "/music/b.mp3", Title: "B"},
		{Path: "/music/c.mp3", Title: "C"},
	}
	prov := &playlistManagerTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Local"},
		saved:                append([]playlist.Track(nil), tracks...),
	}
	m := Model{
		layout:        frameLayout{panelWidth: 74},
		plVisible:     5,
		localProvider: prov,
		provider:      prov,
		plManager: plManagerState{
			visible:     true,
			screen:      plMgrScreenTracks,
			selPlaylist: "mix",
		},
	}
	m.plMgrLoadTracks(tracks)
	return m, prov
}

func pressPlMgr(m *Model, key string) {
	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: key})
}

func screenTitles(m Model) []string {
	titles := make([]string, len(m.plManager.tracks))
	for i, tr := range m.plManager.tracks {
		titles[i] = tr.Title
	}
	return titles
}

// d on the track screen used to remove the highlighted track at once, so a
// press out of habit (d opened the device picker there before v2.4.0) lost
// it (#764). d now asks first, as deleting a playlist does.
func TestPlMgrTrackRemoveAsksFirst(t *testing.T) {
	m, prov := newTrackRemoveTestModel(t)
	m.plManager.cursor = 1

	pressPlMgr(&m, "d")
	if !m.plManager.confirmDel {
		t.Fatal("d should arm the remove prompt")
	}
	if got := strings.Join(screenTitles(m), ","); got != "A,B,C" {
		t.Fatalf("tracks after d = %s, want A,B,C until the prompt is answered", got)
	}
	if prov.saveName != "" {
		t.Fatalf("playlist written before the prompt was answered: %v", prov.saved)
	}
	if body := stripAnsi(m.renderPlMgrTracksBody()); !strings.Contains(body, `Remove "B" from "mix"? [y/n]`) {
		t.Fatalf("track body does not show the prompt on the cursor row:\n%s", body)
	}

	pressPlMgr(&m, "y")
	if m.plManager.confirmDel {
		t.Fatal("y should clear the prompt")
	}
	if got := strings.Join(screenTitles(m), ","); got != "A,C" {
		t.Fatalf("tracks after d y = %s, want A,C", got)
	}
	if len(prov.saved) != 2 || prov.saved[0].Title != "A" || prov.saved[1].Title != "C" {
		t.Fatalf("saved playlist = %v, want A and C", prov.saved)
	}
}

func TestPlMgrTrackRemoveCancelledByAnyOtherKey(t *testing.T) {
	for _, key := range []string{"n", "esc", "d", "j"} {
		t.Run(key, func(t *testing.T) {
			m, prov := newTrackRemoveTestModel(t)
			m.plManager.cursor = 0

			pressPlMgr(&m, "d")
			pressPlMgr(&m, key)
			if m.plManager.confirmDel {
				t.Fatalf("%q should cancel the prompt", key)
			}
			if got := strings.Join(screenTitles(m), ","); got != "A,B,C" {
				t.Fatalf("tracks after d %s = %s, want A,B,C", key, got)
			}
			if prov.saveName != "" {
				t.Fatalf("playlist written after a cancel: %v", prov.saved)
			}
			if !m.plManager.visible || m.plManager.screen != plMgrScreenTracks {
				t.Fatalf("the cancelling key %q also acted: visible %v, screen %d", key, m.plManager.visible, m.plManager.screen)
			}
			if m.plManager.cursor != 0 {
				t.Fatalf("the cancelling key %q moved the cursor to %d", key, m.plManager.cursor)
			}
		})
	}
}

func TestPlMgrTrackRemovePromptNamesMarkedTracks(t *testing.T) {
	m, _ := newTrackRemoveTestModel(t)
	m.plManager.marked = map[int]bool{0: true, 2: true}

	pressPlMgr(&m, "d")
	if body := stripAnsi(m.renderPlMgrTracksBody()); !strings.Contains(body, `Remove 2 marked track(s) from "mix"? [y/n]`) {
		t.Fatalf("track body does not count the marked tracks:\n%s", body)
	}
	pressPlMgr(&m, "Y")
	if got := strings.Join(screenTitles(m), ","); got != "B" {
		t.Fatalf("tracks after d Y = %s, want B", got)
	}
}

// A playlist whose tracks cannot be removed says why at once and never asks.
func TestPlMgrTrackRemoveRefusedWithoutPrompt(t *testing.T) {
	m, _ := newTrackRemoveTestModel(t)
	m.plManager.selPlaylist = history.PlaylistName

	pressPlMgr(&m, "d")
	if m.plManager.confirmDel {
		t.Fatal("d armed a prompt for a playlist that cannot lose tracks")
	}
	if m.status.kind != feedbackWarning || !strings.Contains(m.status.text, "cannot be removed") {
		t.Fatalf("status = %q (kind %v), want the warning that Recently Played tracks cannot be removed", m.status.text, m.status.kind)
	}
}
