package model

import (
	"testing"
	"time"
)

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
