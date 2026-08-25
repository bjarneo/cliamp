package resolve

import (
	"strings"
	"testing"
)

func TestParseYTDLPlaylistFeed(t *testing.T) {
	input := `
{"_type": "url", "id": "PL12345", "title": "My Favorite Tracks", "playlist_count": 25}
{"_type": "url", "id": "VLPL67890", "title": "Chill Vibes", "item_count": 10}
{"_type": "url", "url": "https://www.youtube.com/playlist?list=PLabcde", "title": "Rock Classics", "playlist_count": null, "n_entries": 50}
{"_type": "url", "id": "PL12345", "title": "Duplicate Playlist", "playlist_count": 25}
{"_type": "url", "id": "VLLM", "title": "Liked Music", "playlist_count": 100}
{"_type": "url", "id": "", "title": "No ID"}
invalid json line
`
	pls, err := parseYTDLPlaylistFeed(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseYTDLPlaylistFeed unexpected error: %v", err)
	}

	expected := []struct {
		id    string
		name  string
		count int
	}{
		{id: "PL12345", name: "My Favorite Tracks", count: 0},
		{id: "PL67890", name: "Chill Vibes", count: 0},
		{id: "PLabcde", name: "Rock Classics", count: 0},
		{id: "LM", name: "Liked Music", count: 0},
	}

	if len(pls) != len(expected) {
		t.Fatalf("got %d playlists, want %d", len(pls), len(expected))
	}

	for i, exp := range expected {
		if pls[i].ID != exp.id {
			t.Errorf("[%d] ID = %q, want %q", i, pls[i].ID, exp.id)
		}
		if pls[i].Name != exp.name {
			t.Errorf("[%d] Name = %q, want %q", i, pls[i].Name, exp.name)
		}
		if pls[i].TrackCount != exp.count {
			t.Errorf("[%d] TrackCount = %d, want %d", i, pls[i].TrackCount, exp.count)
		}
	}
}

func TestParseYTDLPlaylistFeed_Empty(t *testing.T) {
	pls, err := parseYTDLPlaylistFeed(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pls) != 0 {
		t.Fatalf("expected 0 playlists, got %d", len(pls))
	}
}
