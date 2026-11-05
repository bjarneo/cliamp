package lyrics

import (
	"slices"
	"testing"
	"time"
)

func TestParseLRCBasic(t *testing.T) {
	lrc := "[00:12.34]Hello world\n[01:05.00]Second line"
	lines := parseLRC(lrc)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	want0 := 12*time.Second + 340*time.Millisecond
	if lines[0].Start != want0 {
		t.Fatalf("line[0].Start = %v, want %v", lines[0].Start, want0)
	}
	if lines[0].Text != "Hello world" {
		t.Fatalf("line[0].Text = %q, want %q", lines[0].Text, "Hello world")
	}

	want1 := 1*time.Minute + 5*time.Second
	if lines[1].Start != want1 {
		t.Fatalf("line[1].Start = %v, want %v", lines[1].Start, want1)
	}
}

func TestParseLRCThreeDigitMs(t *testing.T) {
	lrc := "[00:01.123]Test"
	lines := parseLRC(lrc)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	want := 1*time.Second + 123*time.Millisecond
	if lines[0].Start != want {
		t.Fatalf("Start = %v, want %v", lines[0].Start, want)
	}
}

func TestParseLRCTwoDigitMs(t *testing.T) {
	// Two-digit ms should be scaled (e.g. 50 → 500ms)
	lrc := "[00:01.50]Test"
	lines := parseLRC(lrc)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	want := 1*time.Second + 500*time.Millisecond
	if lines[0].Start != want {
		t.Fatalf("Start = %v, want %v", lines[0].Start, want)
	}
}

func TestParseLRCEmpty(t *testing.T) {
	lines := parseLRC("")
	if len(lines) != 0 {
		t.Fatalf("got %d lines for empty input, want 0", len(lines))
	}
}

func TestParseLRCSkipsNonTimestamped(t *testing.T) {
	lrc := "[ti:Song Title]\n[ar:Artist]\n[00:05.00]Actual lyric"
	lines := parseLRC(lrc)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (should skip metadata tags)", len(lines))
	}
	if lines[0].Text != "Actual lyric" {
		t.Fatalf("Text = %q, want %q", lines[0].Text, "Actual lyric")
	}
}

func TestParseLRCEmptyText(t *testing.T) {
	lrc := "[00:10.00]"
	lines := parseLRC(lrc)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if lines[0].Text != "" {
		t.Fatalf("Text = %q, want empty", lines[0].Text)
	}
}

func TestParseEmbeddedPrefersSyncedLRC(t *testing.T) {
	lines := ParseEmbedded("[00:01.50]Hello\nplain fallback")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 synced line", len(lines))
	}
	if lines[0].Start != 1500*time.Millisecond || lines[0].Text != "Hello" {
		t.Fatalf("line = %+v, want synced Hello at 1.5s", lines[0])
	}
}

func TestParseEmbeddedPlainText(t *testing.T) {
	lines := ParseEmbedded("Line one\nLine two")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0].Start != 0 || lines[0].Text != "Line one" || lines[1].Text != "Line two" {
		t.Fatalf("lines = %+v, want plain zero-timestamp lines", lines)
	}
}

func TestCleanQuery(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Artist - Song (Official Video)", "Artist - Song"},
		{"Song [Lyric Video]", "Song"},
		{"Song - Official Audio", "Song"},
		{"Song Official Music Video", "Song"},
		{"Clean Title", "Clean Title"},
		// Label words inside a real title must survive (regression cases).
		{"Videotape", "Videotape"},
		{"Audioslave", "Audioslave"},
		{"Video Games", "Video Games"},
		{"No Lyric", "No Lyric"},
		{"", ""},
	}
	for _, tt := range tests {
		got := cleanQuery(tt.input)
		if got != tt.want {
			t.Errorf("cleanQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseLRCRepeatedTimestamps(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Line
	}{
		{
			name:  "repeated tags in time order",
			input: "[00:01.00]One\n[00:03.50][00:09.25]Chorus\n[00:07]Plain\n[00:02.00]Two",
			want: []Line{
				{Start: time.Second, Text: "One"},
				{Start: 2 * time.Second, Text: "Two"},
				{Start: 3500 * time.Millisecond, Text: "Chorus"},
				{Start: 7 * time.Second, Text: "Plain"},
				{Start: 9250 * time.Millisecond, Text: "Chorus"},
			},
		},
		{
			name:  "one-digit fraction",
			input: "[00:01.5]One",
			want:  []Line{{Start: 1500 * time.Millisecond, Text: "One"}},
		},
		{
			name:  "leading BOM",
			input: "\ufeff[00:01.00]One\n[00:02.00]Two",
			want: []Line{
				{Start: time.Second, Text: "One"},
				{Start: 2 * time.Second, Text: "Two"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := parseLRC(tt.input)
			if !slices.Equal(lines, tt.want) {
				t.Fatalf("parseLRC(%q) = %+v, want %+v", tt.input, lines, tt.want)
			}
		})
	}
}
