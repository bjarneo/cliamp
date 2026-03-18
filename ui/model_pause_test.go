package ui

import (
	"testing"

	"cliamp/playlist"
)

func TestShouldReconnectOnUnpause(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
		idx   int
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
			name: "regular stream does not reconnect",
			track: playlist.Track{
				Path:   "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
				Stream: true,
			},
			idx:  0,
			want: false,
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
			if got := shouldReconnectOnUnpause(tt.track, tt.idx); got != tt.want {
				t.Fatalf("shouldReconnectOnUnpause(...) = %v, want %v", got, tt.want)
			}
		})
	}
}
