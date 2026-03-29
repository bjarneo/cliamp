package ui

import (
	"strings"
	"testing"

	"cliamp/playlist"
)

func TestTerminalTitleValuesForTrack(t *testing.T) {
	t.Run("stream title is parsed into logical song fields", func(t *testing.T) {
		values := terminalTitleValuesForTrack(
			playlist.Track{Stream: true, Path: "https://radio.example.test/stream"},
			"Artist - Song",
			true,
			false,
		)

		if values.state != "playing" || values.stateIcon != "▶" {
			t.Fatalf("state = %q/%q, want playing/▶", values.state, values.stateIcon)
		}
		if values.title != "Song" || values.artist != "Artist" {
			t.Fatalf("parsed values = title %q artist %q", values.title, values.artist)
		}
		if values.metadata != "Song - Artist" {
			t.Fatalf("metadata = %q, want %q", values.metadata, "Song - Artist")
		}
		if values.streamTitle != "Artist - Song" {
			t.Fatalf("streamTitle = %q, want raw value", values.streamTitle)
		}
	})

	t.Run("non parsable stream title falls back to raw title", func(t *testing.T) {
		values := terminalTitleValuesForTrack(
			playlist.Track{Stream: true},
			"NTS Live",
			true,
			false,
		)

		if values.title != "NTS Live" || values.artist != "" {
			t.Fatalf("values = title %q artist %q", values.title, values.artist)
		}
		if values.metadata != "NTS Live" {
			t.Fatalf("metadata = %q, want %q", values.metadata, "NTS Live")
		}
	})

	t.Run("stopped clears track metadata", func(t *testing.T) {
		values := terminalTitleValuesForTrack(
			playlist.Track{Title: "Angel", Artist: "Massive Attack", Path: "/music/angel.flac"},
			"",
			false,
			false,
		)

		if values.state != "stopped" {
			t.Fatalf("state = %q, want stopped", values.state)
		}
		if values.metadata != "" || values.title != "" || values.artist != "" || values.path != "" {
			t.Fatalf("stopped values should be empty, got %+v", values)
		}
	})
}

func TestRenderTerminalTitle(t *testing.T) {
	track := playlist.Track{Title: "Angel", Artist: "Massive Attack"}

	t.Run("playing", func(t *testing.T) {
		got := renderTerminalTitle(terminalTitleValuesForTrack(track, "", true, false))
		want := "▶ Angel - Massive Attack | cliamp"
		if got != want {
			t.Fatalf("render(playing) = %q, want %q", got, want)
		}
	})

	t.Run("paused", func(t *testing.T) {
		got := renderTerminalTitle(terminalTitleValuesForTrack(track, "", true, true))
		want := "⏸ Angel - Massive Attack | cliamp"
		if got != want {
			t.Fatalf("render(paused) = %q, want %q", got, want)
		}
	})

	t.Run("stopped", func(t *testing.T) {
		got := renderTerminalTitle(terminalTitleValuesForTrack(track, "", false, false))
		if got != baseTerminalTitle {
			t.Fatalf("render(stopped) = %q, want %q", got, baseTerminalTitle)
		}
	})
}

func TestTerminalTitleIntroSequence(t *testing.T) {
	state := initialTerminalTitleState()
	frames := []string{currentTerminalTitle(state, 0, terminalTitleStateValues(false, false))}

	for state.introActive {
		advanceTerminalTitleState(&state, 0)
		title := currentTerminalTitle(state, 0, terminalTitleStateValues(false, false))
		if title != frames[len(frames)-1] {
			frames = append(frames, title)
		}
	}

	if got, want := frames[0], strings.Repeat(" ", titleIntroViewportDefault-4)+"It r"; got != want {
		t.Fatalf("first intro frame = %q, want %q", got, want)
	}
	if got, wantSuffix := frames[1], "It rea"; !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("second intro frame = %q, want suffix %q", got, wantSuffix)
	}
	if got, want := frames[len(frames)-2], strings.Repeat(" ", titleIntroViewportDefault); got != want {
		t.Fatalf("last intro frame = %q, want %q", got, want)
	}
	if got := frames[len(frames)-1]; got != baseTerminalTitle {
		t.Fatalf("post-intro title = %q, want %q", got, baseTerminalTitle)
	}
}

func TestInitialTerminalTitle(t *testing.T) {
	got := InitialTerminalTitle()
	want := strings.Repeat(" ", titleIntroViewportDefault-4) + "It r"
	if got != want {
		t.Fatalf("InitialTerminalTitle() = %q, want %q", got, want)
	}
}

func TestCurrentTerminalTitleSanitizesRenderedTitle(t *testing.T) {
	tests := []struct {
		name   string
		values terminalTitleValues
		want   string
	}{
		{
			name: "drops control bytes",
			values: terminalTitleValues{
				state:     "playing",
				stateIcon: "▶",
				metadata:  "Song\a\x1b[31m - Artist\r\nName",
			},
			want: "▶ Song[31m - Artist Name | cliamp",
		},
		{
			name: "collapses control whitespace",
			values: terminalTitleValues{
				metadata: "Song\r\n\tArtist",
			},
			want: "Song Artist | cliamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := currentTerminalTitle(terminalTitleState{}, 0, tt.values); got != tt.want {
				t.Fatalf("currentTerminalTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTitleIntroViewportForWidth(t *testing.T) {
	tests := []struct {
		width    int
		introLen int
		want     int
	}{
		{width: 0, introLen: len(defaultTerminalTitleIntroRunes), want: titleIntroViewportDefault},
		{width: 40, introLen: len(defaultTerminalTitleIntroRunes), want: titleIntroViewportMin},
		{width: 80, introLen: len(defaultTerminalTitleIntroRunes), want: 26},
		{width: 160, introLen: len(defaultTerminalTitleIntroRunes), want: len(defaultTerminalTitleIntroRunes)},
	}

	for _, tt := range tests {
		if got := titleIntroViewportForWidth(tt.width, tt.introLen); got != tt.want {
			t.Fatalf("titleIntroViewportForWidth(%d, %d) = %d, want %d", tt.width, tt.introLen, got, tt.want)
		}
	}
}
