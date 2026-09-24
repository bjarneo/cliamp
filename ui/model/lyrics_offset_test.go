package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
)

// A positive offset must advance the position used for highlighting, so `]`
// moves the highlight earlier when a source's timestamps run late.
func TestLyricsOffsetAdvancesTheHighlightPosition(t *testing.T) {
	eng := &playbackFakeEngine{position: 10 * time.Second}
	m := Model{player: eng}

	m.lyrics.offset = 500 * time.Millisecond
	if got, want := m.lyricsPlaybackPosition(), 10*time.Second+500*time.Millisecond; got != want {
		t.Fatalf("lyricsPlaybackPosition() = %v, want %v", got, want)
	}
	m.lyrics.offset = -500 * time.Millisecond
	if got, want := m.lyricsPlaybackPosition(), 10*time.Second-500*time.Millisecond; got != want {
		t.Fatalf("lyricsPlaybackPosition() = %v, want %v", got, want)
	}
}

// The offset keys must do nothing for plain lyrics: the registry disables them,
// and the key handler enforces the same rule.
func TestLyricsOffsetKeysIgnoreUnsyncedLyrics(t *testing.T) {
	m := Model{playlist: playlist.New()}
	m.lyrics.visible = true
	m.lyrics.lines = []lyrics.Line{{Text: "plain"}}

	m.handleKey(tea.KeyPressMsg{Text: "]"})
	m.handleKey(tea.KeyPressMsg{Text: "["})

	if m.lyrics.offset != 0 {
		t.Fatalf("offset = %v, want 0 for lyrics without timestamps", m.lyrics.offset)
	}
}

func TestSetLyricsOffset(t *testing.T) {
	m := Model{}
	m.SetLyricsOffset(1500)
	if m.lyrics.offset != 1500*time.Millisecond {
		t.Fatalf("offset = %v, want 1.5s", m.lyrics.offset)
	}

	m.SetLyricsOffset(0)
	if m.lyrics.offset != 0 {
		t.Fatalf("offset = %v, want 0", m.lyrics.offset)
	}

	m.SetLyricsOffset(-800)
	if m.lyrics.offset != -800*time.Millisecond {
		t.Fatalf("offset = %v, want -0.8s", m.lyrics.offset)
	}

	m.SetLyricsOffset(999999)
	if m.lyrics.offset != maxLyricsOffset {
		t.Fatalf("offset = %v, want clamped to %v", m.lyrics.offset, maxLyricsOffset)
	}
}

func TestNudgeLyricsOffsetPersists(t *testing.T) {
	saver := &recordingConfigSaver{}
	m := Model{configSaver: saver}
	m.nudgeLyricsOffset(250 * time.Millisecond)
	m.nudgeLyricsOffset(250 * time.Millisecond)

	if m.lyrics.offset != 500*time.Millisecond {
		t.Fatalf("offset = %v, want 0.5s", m.lyrics.offset)
	}
	if got := saver.values["lyrics_offset_ms"]; got != "500" {
		t.Fatalf("saved value = %q, want 500", got)
	}
}

func TestNudgeLyricsOffsetClamps(t *testing.T) {
	m := Model{}
	m.SetLyricsOffset(9900)
	m.nudgeLyricsOffset(250 * time.Millisecond)
	if m.lyrics.offset != maxLyricsOffset {
		t.Fatalf("offset = %v, want clamped to %v", m.lyrics.offset, maxLyricsOffset)
	}

	m.SetLyricsOffset(-9900)
	m.nudgeLyricsOffset(-250 * time.Millisecond)
	if m.lyrics.offset != -maxLyricsOffset {
		t.Fatalf("offset = %v, want clamped to -%v", m.lyrics.offset, maxLyricsOffset)
	}
}

func TestNudgeLyricsOffsetNoSaver(t *testing.T) {
	m := Model{} // no configSaver; must not panic
	m.nudgeLyricsOffset(-250 * time.Millisecond)
	if m.lyrics.offset != -250*time.Millisecond {
		t.Fatalf("offset = %v, want -0.25s", m.lyrics.offset)
	}
}

func TestFormatLyricsOffset(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "+0.0s"},
		{500 * time.Millisecond, "+0.5s"},
		{-500 * time.Millisecond, "-0.5s"},
		{2500 * time.Millisecond, "+2.5s"},
		{-2500 * time.Millisecond, "-2.5s"},
	}
	for _, tt := range tests {
		if got := formatLyricsOffset(tt.d); got != tt.want {
			t.Errorf("formatLyricsOffset(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
