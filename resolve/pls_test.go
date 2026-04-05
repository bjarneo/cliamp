package resolve

import (
	"strings"
	"testing"
)

func TestParsePLSBasic(t *testing.T) {
	input := `[playlist]
File1=http://radio.example.com/stream1
Title1=Station One
Length1=-1
File2=http://radio.example.com/stream2
Title2=Station Two
Length2=-1
NumberOfEntries=2
Version=2
`
	entries, err := parsePLS(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parsePLS error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	if entries[0].File != "http://radio.example.com/stream1" {
		t.Errorf("entry[0].File = %q", entries[0].File)
	}
	if entries[0].Title != "Station One" {
		t.Errorf("entry[0].Title = %q", entries[0].Title)
	}
	if entries[0].Num != 1 {
		t.Errorf("entry[0].Num = %d, want 1", entries[0].Num)
	}

	if entries[1].File != "http://radio.example.com/stream2" {
		t.Errorf("entry[1].File = %q", entries[1].File)
	}
	if entries[1].Title != "Station Two" {
		t.Errorf("entry[1].Title = %q", entries[1].Title)
	}
}

func TestParsePLSSortedByNumber(t *testing.T) {
	// Entries out of order should be sorted
	input := `[playlist]
File3=http://example.com/3
Title3=Third
File1=http://example.com/1
Title1=First
File2=http://example.com/2
Title2=Second
`
	entries, err := parsePLS(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parsePLS error: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].Title != "First" {
		t.Errorf("entry[0].Title = %q, want First", entries[0].Title)
	}
	if entries[1].Title != "Second" {
		t.Errorf("entry[1].Title = %q, want Second", entries[1].Title)
	}
	if entries[2].Title != "Third" {
		t.Errorf("entry[2].Title = %q, want Third", entries[2].Title)
	}
}

func TestParsePLSEmpty(t *testing.T) {
	input := `[playlist]
NumberOfEntries=0
Version=2
`
	_, err := parsePLS(strings.NewReader(input))
	if err == nil {
		t.Fatal("parsePLS should return error for empty playlist")
	}
}

func TestParsePLSSkipsSectionAndComments(t *testing.T) {
	input := `[playlist]
; This is a comment
File1=http://example.com/stream
Title1=Station
`
	entries, err := parsePLS(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parsePLS error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
}

func TestStripMirrorSuffix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Groove Salad (#3)", "Groove Salad"},
		{"Station Name (#1)", "Station Name"},
		{"No Mirror Suffix", "No Mirror Suffix"},
		{"", ""},
		{"Station: (#2)", "Station"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := stripMirrorSuffix(tt.input); got != tt.want {
				t.Errorf("stripMirrorSuffix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAllStreams(t *testing.T) {
	tests := []struct {
		name    string
		entries []plsEntry
		want    bool
	}{
		{
			"all URLs",
			[]plsEntry{{File: "http://a.com/s"}, {File: "https://b.com/s"}},
			true,
		},
		{
			"mixed",
			[]plsEntry{{File: "http://a.com/s"}, {File: "/local/file.mp3"}},
			false,
		},
		{
			"all local",
			[]plsEntry{{File: "/a.mp3"}, {File: "/b.mp3"}},
			false,
		},
		{
			"empty",
			[]plsEntry{},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allStreams(tt.entries); got != tt.want {
				t.Errorf("allStreams() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlsEntriesToTracksCollapsesMirrors(t *testing.T) {
	// When all entries are streams, should collapse to a single track
	entries := []plsEntry{
		{Num: 1, File: "http://a.com/stream", Title: "Station (#1)"},
		{Num: 2, File: "http://b.com/stream", Title: "Station (#2)"},
	}

	tracks := plsEntriesToTracks(entries)
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1 (mirrors collapsed)", len(tracks))
	}
	if tracks[0].Title != "Station" {
		t.Errorf("Title = %q, want Station (mirror suffix stripped)", tracks[0].Title)
	}
	if !tracks[0].Stream || !tracks[0].Realtime {
		t.Error("collapsed stream should be Stream=true, Realtime=true")
	}
}

func TestPlsEntriesToTracksMultipleTracks(t *testing.T) {
	// Mixed local/remote entries should produce individual tracks
	entries := []plsEntry{
		{Num: 1, File: "/home/user/song.mp3", Title: "Song One"},
		{Num: 2, File: "/home/user/song2.mp3", Title: "Song Two"},
	}

	tracks := plsEntriesToTracks(entries)
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if tracks[0].Title != "Song One" {
		t.Errorf("track[0].Title = %q, want Song One", tracks[0].Title)
	}
}

func TestHumanizeBasename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"clr-podcast-467", "clr podcast 467"},
		{"no-dashes-here", "no dashes here"},
		{"nodashes", "nodashes"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := humanizeBasename(tt.input); got != tt.want {
				t.Errorf("humanizeBasename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
