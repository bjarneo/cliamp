package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestLyricsSyncable(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
		want  bool
	}{
		{
			name:  "local file",
			track: playlist.Track{Title: "Local", Path: "/tmp/a.mp3", DurationSecs: 180},
			want:  true,
		},
		{
			name:  "youtube music finite track",
			track: playlist.Track{Title: "Song", Path: "https://music.youtube.com/watch?v=abc", Stream: true, DurationSecs: 240},
			want:  true,
		},
		{
			name:  "youtube finite track",
			track: playlist.Track{Title: "Song", Path: "https://www.youtube.com/watch?v=abc", Stream: true, DurationSecs: 240},
			want:  true,
		},
		{
			name:  "yt-dlp track (soundcloud) finite",
			track: playlist.Track{Title: "SC", Path: "https://soundcloud.com/x/y", Stream: true, DurationSecs: 120},
			want:  true,
		},
		{
			name:  "youtube live (no duration)",
			track: playlist.Track{Title: "Live", Path: "https://music.youtube.com/watch?v=live", Stream: true, DurationSecs: 0},
			want:  false,
		},
		{
			name:  "icy radio stream without provider metadata",
			track: playlist.Track{Title: "Radio", Path: "https://radio.example/stream", Stream: true},
			want:  false,
		},
		{
			name:  "navidrome provider stream",
			track: playlist.Track{Title: "Nav", Path: "https://nav.example/stream", Stream: true, DurationSecs: 200, ProviderMeta: map[string]string{"navidrome": "id"}},
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := playlist.New()
			p.Replace([]playlist.Track{tt.track})
			p.SetIndex(0)
			m := Model{playlist: p}
			if got := m.lyricsSyncable(); got != tt.want {
				t.Fatalf("lyricsSyncable() = %v, want %v", got, tt.want)
			}
		})
	}
}
