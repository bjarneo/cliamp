package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestShouldReconnectOnUnpause(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
		idx   int
		pause time.Duration
		want  bool
	}{
		{
			name: "live http stream reconnects",
			track: playlist.Track{
				Path:     "https://radio.example.com/stream",
				Stream:   true,
				Realtime: true,
			},
			idx:  0,
			want: true,
		},
		{
			name: "short-paused yt-dlp stream does not reconnect",
			track: playlist.Track{
				Path:   "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
				Stream: true,
			},
			idx:   0,
			pause: ytdlReconnectPauseThreshold - time.Second,
			want:  false,
		},
		{
			name: "long-paused yt-dlp stream reconnects",
			track: playlist.Track{
				Path:   "https://music.youtube.com/watch?v=dQw4w9WgXcQ",
				Stream: true,
			},
			idx:   0,
			pause: ytdlReconnectPauseThreshold,
			want:  true,
		},
		{
			name: "invalid current index does not reconnect",
			track: playlist.Track{
				Path:     "https://radio.example.com/stream",
				Stream:   true,
				Realtime: true,
			},
			idx:  -1,
			want: false,
		},
		{
			name: "known duration live stream still reconnects",
			track: playlist.Track{
				Path:         "https://radio.example.com/show.mp3",
				Stream:       true,
				Realtime:     true,
				DurationSecs: 120,
			},
			idx:  0,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldReconnectOnUnpause(tt.track, tt.idx, tt.pause); got != tt.want {
				t.Fatalf("shouldReconnectOnUnpause(...) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTogglePlayPauseRestartsRuntimeLiveStationInPlace(t *testing.T) {
	player := &playbackFakeEngine{playing: true, paused: true, live: true}
	p := playlist.New()
	p.Add(
		playlist.Track{Title: "One", Path: "https://radio.example.com/one", Stream: true},
		playlist.Track{Title: "Two", Path: "https://radio.example.com/two", Stream: true},
		playlist.Track{Title: "Three", Path: "https://radio.example.com/three", Stream: true},
	)
	p.SetIndex(1)
	m := Model{player: player, playlist: p}

	cmd := m.togglePlayPause()
	if cmd == nil {
		t.Fatal("togglePlayPause() returned nil, want current-station restart command")
	}
	if got := p.Index(); got != 1 {
		t.Fatalf("playlist index = %d, want current station 1", got)
	}
	if player.stopCalls != 1 {
		t.Fatalf("Stop calls = %d, want 1 before reconnect", player.stopCalls)
	}
	if !m.buffering || m.playingTrack.Path != "https://radio.example.com/two" {
		t.Fatalf("restart state = buffering %v, track %q; want station two buffering", m.buffering, m.playingTrack.Path)
	}
}

// A flagged yt-dlp track whose player reports a duration is a finished
// recording: a short pause resumes it instead of reconnecting.
func TestTogglePlayPauseYTDLLiveFlagFollowsPlayerDuration(t *testing.T) {
	tests := []struct {
		name         string
		duration     time.Duration
		wantResume   bool
		wantSeekYTDL int
	}{
		{name: "still live reconnects", wantSeekYTDL: 1},
		{name: "finished recording resumes", duration: 90 * time.Minute, wantResume: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := &playbackFakeEngine{playing: true, paused: true, ytdlSeek: true, duration: tt.duration}
			p := playlist.New()
			p.Add(playlist.Track{Title: "Live", Path: "https://music.youtube.com/watch?v=live1", Stream: true, Realtime: true})
			p.SetIndex(0)
			m := Model{player: player, playlist: p, pausedAt: time.Now().Add(-time.Second)}

			cmd := m.togglePlayPause()
			if cmd != nil {
				cmd()
			}

			if got := len(player.seekYTDLCalls); got != tt.wantSeekYTDL {
				t.Fatalf("SeekYTDL calls = %d, want %d", got, tt.wantSeekYTDL)
			}
			if tt.wantResume && player.paused {
				t.Fatal("player still paused, want buffered audio resumed")
			}
		})
	}
}
