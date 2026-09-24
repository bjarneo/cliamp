package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func appendTestModel() *Model {
	m := &Model{playlist: playlist.New()}
	m.playlist.Replace([]playlist.Track{{Path: "/playing.mp3", Title: "Playing"}})
	m.playlist.Queue(0)
	return m
}

func TestAppendTracksToPlaylistKeepsExistingListAndQueue(t *testing.T) {
	m := appendTestModel()

	m.addTracksToCurrentPlaylist([]playlist.Track{
		{Path: "/a.mp3", Title: "A"},
		{Path: "/b.mp3", Title: "B"},
	}, "Saved List")

	if got := m.playlist.Len(); got != 3 {
		t.Errorf("playlist length = %d, want 3", got)
	}
	if got := m.playlist.QueueLen(); got != 1 {
		t.Errorf("queue length = %d, want the existing entry to survive", got)
	}
	first, _ := m.playlist.Track(0)
	if first.Path != "/playing.mp3" {
		t.Errorf("first track = %q, want the pre-existing one", first.Path)
	}
	last, _ := m.playlist.Track(2)
	if last.Path != "/b.mp3" {
		t.Errorf("last track = %q, want the appended one", last.Path)
	}
}

func TestAppendTracksToPlaylistIgnoresAnEmptyBatch(t *testing.T) {
	m := appendTestModel()

	if _, ok := m.addTracksToCurrentPlaylist(nil, "Saved List"); ok {
		t.Error("an empty batch reported success")
	}
	if got := m.playlist.Len(); got != 1 {
		t.Errorf("playlist length = %d, want it unchanged at 1", got)
	}
}

// Appending marked tracks uses the same selection rule as w: the marked ones,
// or the highlighted one when nothing is marked.
func TestPlMgrAppendSelectedTracks(t *testing.T) {
	tests := []struct {
		name      string
		marked    map[int]bool
		cursor    int
		wantAdded int
		wantFirst string
	}{
		{"marked tracks", map[int]bool{0: true, 2: true}, 1, 2, "/one.mp3"},
		{"nothing marked uses the cursor", nil, 1, 1, "/two.mp3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{playlist: playlist.New()}
			m.plManager = plManagerState{
				visible:     true,
				screen:      plMgrScreenTracks,
				selPlaylist: "Saved List",
				cursor:      tt.cursor,
				marked:      tt.marked,
				tracks: []playlist.Track{
					{Path: "/one.mp3", Title: "One"},
					{Path: "/two.mp3", Title: "Two"},
					{Path: "/three.mp3", Title: "Three"},
				},
			}

			m.addTracksToCurrentPlaylist(m.plMgrSelectedTracks(), "Saved List")

			if got := m.playlist.Len(); got != tt.wantAdded {
				t.Fatalf("playlist length = %d, want %d", got, tt.wantAdded)
			}
			first, _ := m.playlist.Track(0)
			if first.Path != tt.wantFirst {
				t.Errorf("first appended = %q, want %q", first.Path, tt.wantFirst)
			}
		})
	}
}
