package resolve

import (
	"strings"
	"testing"
)

func TestParseM3UBasic(t *testing.T) {
	input := `#EXTM3U
#EXTINF:120,Artist - Song One
http://example.com/song1.mp3
#EXTINF:180,Artist - Song Two
http://example.com/song2.mp3
`
	entries, err := parseM3U(strings.NewReader(input), "")
	if err != nil {
		t.Fatalf("parseM3U error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	if entries[0].Title != "Artist - Song One" {
		t.Errorf("entry[0].Title = %q, want %q", entries[0].Title, "Artist - Song One")
	}
	if entries[0].Duration != 120 {
		t.Errorf("entry[0].Duration = %d, want 120", entries[0].Duration)
	}
	if entries[0].Path != "http://example.com/song1.mp3" {
		t.Errorf("entry[0].Path = %q", entries[0].Path)
	}

	if entries[1].Title != "Artist - Song Two" {
		t.Errorf("entry[1].Title = %q", entries[1].Title)
	}
	if entries[1].Duration != 180 {
		t.Errorf("entry[1].Duration = %d, want 180", entries[1].Duration)
	}
}

func TestParseM3UNoHeader(t *testing.T) {
	// M3U without #EXTM3U header should still work
	input := `http://example.com/stream1.mp3
http://example.com/stream2.mp3
`
	entries, err := parseM3U(strings.NewReader(input), "")
	if err != nil {
		t.Fatalf("parseM3U error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Duration != -1 {
		t.Errorf("bare entry Duration = %d, want -1", entries[0].Duration)
	}
}

func TestParseM3UBOM(t *testing.T) {
	// UTF-8 BOM prefix should be stripped
	input := "\xef\xbb\xbf#EXTM3U\n#EXTINF:60,Song\nhttp://example.com/song.mp3\n"
	entries, err := parseM3U(strings.NewReader(input), "")
	if err != nil {
		t.Fatalf("parseM3U error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Title != "Song" {
		t.Errorf("Title = %q, want Song", entries[0].Title)
	}
}

func TestParseM3USkipsComments(t *testing.T) {
	input := `#EXTM3U
# This is a comment
#EXTINF:60,Song
http://example.com/song.mp3
#EXTVLCOPT:some-option
`
	entries, err := parseM3U(strings.NewReader(input), "")
	if err != nil {
		t.Fatalf("parseM3U error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
}

func TestParseM3URelativePaths(t *testing.T) {
	input := `#EXTM3U
#EXTINF:60,Song
music/song.mp3
`
	entries, err := parseM3U(strings.NewReader(input), "/home/user")
	if err != nil {
		t.Fatalf("parseM3U error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Path != "/home/user/music/song.mp3" {
		t.Errorf("Path = %q, want /home/user/music/song.mp3", entries[0].Path)
	}
}

func TestParseM3UAbsolutePaths(t *testing.T) {
	input := "/absolute/path/song.mp3\n"
	entries, err := parseM3U(strings.NewReader(input), "/home/user")
	if err != nil {
		t.Fatalf("parseM3U error: %v", err)
	}
	if entries[0].Path != "/absolute/path/song.mp3" {
		t.Errorf("Path = %q, want /absolute/path/song.mp3", entries[0].Path)
	}
}

func TestParseM3UEmpty(t *testing.T) {
	entries, err := parseM3U(strings.NewReader(""), "")
	if err != nil {
		t.Fatalf("parseM3U error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
}

func TestParseM3URadioStream(t *testing.T) {
	// Radio stream with -1 duration
	input := `#EXTM3U
#EXTINF:-1,Radio Station
http://radio.example.com/stream
`
	entries, err := parseM3U(strings.NewReader(input), "")
	if err != nil {
		t.Fatalf("parseM3U error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Duration != -1 {
		t.Errorf("Duration = %d, want -1", entries[0].Duration)
	}
	if entries[0].Title != "Radio Station" {
		t.Errorf("Title = %q, want Radio Station", entries[0].Title)
	}
}

func TestM3UEntryToTrackStream(t *testing.T) {
	e := m3uEntry{
		Path:     "http://radio.example.com/stream",
		Title:    "Radio Station",
		Duration: -1,
	}
	tr := m3uEntryToTrack(e)

	if !tr.Stream {
		t.Error("Stream should be true for URL")
	}
	if !tr.Realtime {
		t.Error("Realtime should be true for stream with -1 duration")
	}
	if tr.DurationSecs != 0 {
		t.Errorf("DurationSecs = %d, want 0 (negative clamped)", tr.DurationSecs)
	}
	if tr.Title != "Radio Station" {
		t.Errorf("Title = %q, want Radio Station", tr.Title)
	}
}

func TestM3UEntryToTrackFile(t *testing.T) {
	e := m3uEntry{
		Path:     "/home/user/song.mp3",
		Title:    "My Song",
		Duration: 180,
	}
	tr := m3uEntryToTrack(e)

	if tr.Stream {
		t.Error("Stream should be false for local file")
	}
	if tr.Realtime {
		t.Error("Realtime should be false for local file")
	}
	if tr.DurationSecs != 180 {
		t.Errorf("DurationSecs = %d, want 180", tr.DurationSecs)
	}
}
