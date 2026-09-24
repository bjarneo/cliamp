package main

import (
	"errors"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

type daemonPlaybackFake struct {
	player.Engine
	playing     bool
	paused      bool
	drained     bool
	runtimeLive bool
	duration    time.Duration
	playErr     error
	playCalls   []string
	stopCalls   int
	toggleCalls int
}

func (f *daemonPlaybackFake) Play(path string, _ time.Duration) error {
	f.playCalls = append(f.playCalls, path)
	if f.playErr != nil {
		return f.playErr
	}
	f.playing = true
	f.paused = false
	f.drained = false
	return nil
}

func (f *daemonPlaybackFake) PlayYTDL(path string, d time.Duration) error {
	return f.Play(path, d)
}

func (f *daemonPlaybackFake) Stop() {
	f.stopCalls++
	f.playing = false
	f.paused = false
}

func (f *daemonPlaybackFake) TogglePause() {
	f.toggleCalls++
	f.paused = !f.paused
}

func (f *daemonPlaybackFake) IsPlaying() bool         { return f.playing }
func (f *daemonPlaybackFake) IsPaused() bool          { return f.paused }
func (f *daemonPlaybackFake) Drained() bool           { return f.drained }
func (f *daemonPlaybackFake) IsLiveStream() bool      { return f.runtimeLive }
func (f *daemonPlaybackFake) Duration() time.Duration { return f.duration }
func (f *daemonPlaybackFake) Position() time.Duration { return 0 }
func (f *daemonPlaybackFake) PositionAndDuration() (time.Duration, time.Duration) {
	return 0, 0
}
func (f *daemonPlaybackFake) Volume() float64 { return 0 }
func (f *daemonPlaybackFake) Seekable() bool  { return false }

func TestDaemonResumeRestartsLiveStation(t *testing.T) {
	fake := &daemonPlaybackFake{playing: true, paused: true, runtimeLive: true}
	pl := playlist.New()
	pl.Add(
		playlist.Track{Path: "https://radio.example.com/one", Stream: true},
		playlist.Track{Path: "https://radio.example.com/two", Stream: true},
	)
	pl.SetIndex(0)
	d := &daemon{player: fake, playlist: pl}

	d.Send(playback.PlayMsg{})

	if got := pl.Index(); got != 0 {
		t.Fatalf("playlist index = %d, want current station 0", got)
	}
	if fake.stopCalls != 1 || len(fake.playCalls) != 1 || fake.playCalls[0] != "https://radio.example.com/one" {
		t.Fatalf("stop/play calls = %d/%v, want one restart of current station", fake.stopCalls, fake.playCalls)
	}
	if fake.toggleCalls != 0 {
		t.Fatalf("TogglePause calls = %d, want a fresh live connection", fake.toggleCalls)
	}
}

func TestDaemonDrainedLiveStationDoesNotAdvance(t *testing.T) {
	tests := []struct {
		name    string
		playErr error
	}{
		{name: "restart succeeds"},
		{name: "restart fails without repeated advance", playErr: errors.New("offline")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &daemonPlaybackFake{
				playing:     true,
				drained:     true,
				runtimeLive: true,
				playErr:     tt.playErr,
			}
			pl := playlist.New()
			pl.Add(
				playlist.Track{Path: "https://radio.example.com/one", Stream: true},
				playlist.Track{Path: "https://radio.example.com/two", Stream: true},
			)
			pl.SetIndex(0)
			d := &daemon{player: fake, playlist: pl}

			d.tick()

			if got := pl.Index(); got != 0 {
				t.Fatalf("playlist index = %d, want current station 0", got)
			}
			if len(fake.playCalls) != 1 || fake.playCalls[0] != "https://radio.example.com/one" {
				t.Fatalf("play calls = %v, want current station restart", fake.playCalls)
			}
			if tt.playErr != nil && fake.playing {
				t.Fatal("player remains active after failed live restart")
			}
		})
	}
}

// A yt-dlp live flag is set at listing time. While the stream is live the
// player has no duration and a drain restarts it; once the URL serves the
// finished recording the player reports a duration and the drain advances.
func TestDaemonDrainedYTDLLiveFlagFollowsPlayerDuration(t *testing.T) {
	tests := []struct {
		name      string
		duration  time.Duration
		wantIndex int
	}{
		{name: "still live restarts in place", wantIndex: 0},
		{name: "ended recording advances", duration: 90 * time.Minute, wantIndex: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &daemonPlaybackFake{playing: true, drained: true, duration: tt.duration}
			pl := playlist.New()
			pl.Add(
				playlist.Track{Path: "https://music.youtube.com/watch?v=live1", Stream: true, Realtime: true},
				playlist.Track{Path: "https://music.youtube.com/watch?v=next1", Stream: true, DurationSecs: 100},
			)
			pl.SetIndex(0)
			d := &daemon{player: fake, playlist: pl}

			d.tick()

			if got := pl.Index(); got != tt.wantIndex {
				t.Fatalf("playlist index = %d, want %d", got, tt.wantIndex)
			}
		})
	}
}

// cliamp.track.is_live() and the daemon share this rule, so a plugin sees a
// finished recording with a stale yt-dlp live flag as the finite track it is.
func TestPlaysLive(t *testing.T) {
	ytdl := "https://music.youtube.com/watch?v=live1"
	tests := []struct {
		name  string
		track playlist.Track
		fake  daemonPlaybackFake
		want  bool
	}{
		{"yt-dlp live stream", playlist.Track{Path: ytdl, Stream: true, Realtime: true}, daemonPlaybackFake{}, true},
		{"yt-dlp recording with a stale live flag", playlist.Track{Path: ytdl, Stream: true, Realtime: true}, daemonPlaybackFake{duration: 90 * time.Minute}, false},
		{"radio station", playlist.Track{Path: "https://example.com/radio", Stream: true, Realtime: true}, daemonPlaybackFake{duration: time.Minute}, true},
		{"stream the player detected as live", playlist.Track{Path: "https://example.com/stream", Stream: true}, daemonPlaybackFake{runtimeLive: true}, true},
		{"ordinary track", playlist.Track{Path: "/music/song.flac"}, daemonPlaybackFake{duration: 3 * time.Minute}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := playsLive(tt.track, &tt.fake); got != tt.want {
				t.Fatalf("playsLive() = %v, want %v", got, tt.want)
			}
		})
	}
}
