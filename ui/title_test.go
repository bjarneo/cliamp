package ui

import (
	"strings"
	"testing"
	"time"

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

func TestTerminalTitleFormatRender(t *testing.T) {
	renderer := newTerminalTitleRenderer(TerminalTitleConfig{
		Format: defaultTerminalTitleFormat,
		Intro:  "",
	})
	track := playlist.Track{Title: "Angel", Artist: "Massive Attack"}

	t.Run("playing", func(t *testing.T) {
		got := renderer.render(terminalTitleValuesForTrack(track, "", true, false))
		want := "▶ Angel - Massive Attack | cliamp"
		if got != want {
			t.Fatalf("render(playing) = %q, want %q", got, want)
		}
	})

	t.Run("paused", func(t *testing.T) {
		got := renderer.render(terminalTitleValuesForTrack(track, "", true, true))
		want := "⏸ Angel - Massive Attack | cliamp"
		if got != want {
			t.Fatalf("render(paused) = %q, want %q", got, want)
		}
	})

	t.Run("stopped", func(t *testing.T) {
		got := renderer.render(terminalTitleValuesForTrack(track, "", false, false))
		if got != baseTerminalTitle {
			t.Fatalf("render(stopped) = %q, want %q", got, baseTerminalTitle)
		}
	})
}

func TestTerminalTitleFormatOptionalGroups(t *testing.T) {
	renderer := newTerminalTitleRenderer(TerminalTitleConfig{
		Format: "[%artist% - ]%title%",
		Intro:  "",
	})

	withArtist := renderer.render(terminalTitleValues{
		title:  "Angel",
		artist: "Massive Attack",
	})
	if withArtist != "Massive Attack - Angel" {
		t.Fatalf("render(withArtist) = %q", withArtist)
	}

	withoutArtist := renderer.render(terminalTitleValues{title: "Angel"})
	if withoutArtist != "Angel" {
		t.Fatalf("render(withoutArtist) = %q, want %q", withoutArtist, "Angel")
	}
}

func TestTerminalTitleFormatUnknownAndMalformedAreLiteral(t *testing.T) {
	t.Run("unknown token stays literal", func(t *testing.T) {
		renderer := newTerminalTitleRenderer(TerminalTitleConfig{
			Format: "[%bogus% ]%app%",
			Intro:  "",
		})
		got := renderer.render(terminalTitleStateValues(false, false))
		if got != "%bogus% cliamp" {
			t.Fatalf("render(unknown) = %q, want %q", got, "%bogus% cliamp")
		}
	})

	t.Run("malformed syntax stays literal", func(t *testing.T) {
		renderer := newTerminalTitleRenderer(TerminalTitleConfig{
			Format: "[broken %app%",
			Intro:  "",
		})
		got := renderer.render(terminalTitleStateValues(false, false))
		if got != "[broken %app%" {
			t.Fatalf("render(malformed) = %q, want %q", got, "[broken %app%")
		}
	})
}

func TestTerminalTitleIntroSequence(t *testing.T) {
	renderer := newTerminalTitleRenderer(TerminalTitleConfig{
		Format: defaultTerminalTitleFormat,
		Intro:  defaultTerminalTitleIntro,
	})
	state := initialTerminalTitleState(renderer)
	frames := []string{currentTerminalTitle(state, renderer, 0, terminalTitleStateValues(false, false))}

	for state.introActive {
		advanceTerminalTitleState(&state, renderer, 0)
		title := currentTerminalTitle(state, renderer, 0, terminalTitleStateValues(false, false))
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
	t.Run("configured intro", func(t *testing.T) {
		got := InitialTerminalTitle(TerminalTitleConfig{
			Format: defaultTerminalTitleFormat,
			Intro:  defaultTerminalTitleIntro,
		})
		want := strings.Repeat(" ", titleIntroViewportDefault-4) + "It r"
		if got != want {
			t.Fatalf("InitialTerminalTitle() = %q, want %q", got, want)
		}
	})

	t.Run("empty intro uses steady state title", func(t *testing.T) {
		got := InitialTerminalTitle(TerminalTitleConfig{
			Format: "%app%",
			Intro:  "",
		})
		if got != "cliamp" {
			t.Fatalf("InitialTerminalTitle(empty intro) = %q, want %q", got, "cliamp")
		}
	})
}

func TestCurrentTerminalTitleSanitizesRenderedTitle(t *testing.T) {
	tests := []struct {
		name   string
		cfg    TerminalTitleConfig
		values terminalTitleValues
		want   string
	}{
		{
			name: "drops control bytes",
			cfg: TerminalTitleConfig{
				Format: defaultTerminalTitleFormat,
				Intro:  "",
			},
			values: terminalTitleValues{
				app:       baseTerminalTitle,
				state:     "playing",
				stateIcon: "▶",
				metadata:  "Song\a\x1b[31m - Artist\r\nName",
			},
			want: "▶ Song[31m - Artist Name | cliamp",
		},
		{
			name: "collapses control whitespace",
			cfg: TerminalTitleConfig{
				Format: "%metadata%",
				Intro:  "",
			},
			values: terminalTitleValues{
				metadata: "Song\r\n\tArtist",
			},
			want: "Song Artist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			renderer := newTerminalTitleRenderer(tt.cfg)
			if got := currentTerminalTitle(terminalTitleState{}, renderer, 0, tt.values); got != tt.want {
				t.Fatalf("currentTerminalTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInitialTerminalTitleSanitizesConfiguredIntro(t *testing.T) {
	got := InitialTerminalTitle(TerminalTitleConfig{
		Format: defaultTerminalTitleFormat,
		Intro:  "\aHi\tThere",
	})
	want := strings.Repeat(" ", titleIntroViewportDefault-4) + "Hi "
	if got != want {
		t.Fatalf("InitialTerminalTitle() = %q, want %q", got, want)
	}
}

func TestTitleIntroViewportForWidth(t *testing.T) {
	tests := []struct {
		width    int
		introLen int
		want     int
	}{
		{width: 0, introLen: len([]rune(defaultTerminalTitleIntro)), want: titleIntroViewportDefault},
		{width: 40, introLen: len([]rune(defaultTerminalTitleIntro)), want: titleIntroViewportMin},
		{width: 80, introLen: len([]rune(defaultTerminalTitleIntro)), want: 26},
		{width: 160, introLen: len([]rune(defaultTerminalTitleIntro)), want: len([]rune(defaultTerminalTitleIntro))},
	}

	for _, tt := range tests {
		if got := titleIntroViewportForWidth(tt.width, tt.introLen); got != tt.want {
			t.Fatalf("titleIntroViewportForWidth(%d, %d) = %d, want %d", tt.width, tt.introLen, got, tt.want)
		}
	}
}

func TestTerminalTickInterval(t *testing.T) {
	tests := []struct {
		name        string
		introActive bool
		visualizer  bool
		playing     bool
		paused      bool
		want        int
	}{
		{name: "intro", introActive: true, want: int(tickFast)},
		{name: "playing with visualizer", visualizer: true, playing: true, want: int(tickFast)},
		{name: "playing without visualizer", playing: true, want: int(tickSlow)},
		{name: "paused", playing: true, paused: true, want: int(tickSlow)},
		{name: "stopped", want: int(tickSlow)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terminalTickInterval(tt.introActive, tt.visualizer, tt.playing, tt.paused); got != time.Duration(tt.want) {
				t.Fatalf("terminalTickInterval(%v, %v, %v, %v) = %v, want %v",
					tt.introActive, tt.visualizer, tt.playing, tt.paused, got, time.Duration(tt.want))
			}
		})
	}
}
