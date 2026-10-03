package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
)

func TestWriteHistory(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	remote := history.Entry{
		Track: playlist.Track{
			Path:         "https://music.example.com/rest/stream?id=1",
			Title:        "Song",
			Artist:       "Artist",
			DurationSecs: 200,
			ProviderMeta: map[string]string{"navidrome.id": "1"},
		},
		PlayedAt: now.Add(-3 * time.Minute),
	}
	local := history.Entry{
		Track:    playlist.Track{Path: "/music/a.mp3", Title: "A"},
		PlayedAt: now.Add(-2 * time.Hour),
	}
	restricted := history.Entry{
		Track:    playlist.Track{Path: "https://www.mixcloud.com/creator/show/", Title: "Show", Stream: true, Restricted: true},
		PlayedAt: now.Add(-time.Hour),
	}
	tests := []struct {
		name       string
		entries    []history.Entry
		jsonOutput bool
		want       string
	}{
		{
			name: "empty text states the track-start rule",
			want: "No history yet. cliamp records a track when it starts to play.\n",
		},
		{name: "empty json", jsonOutput: true, want: "[]\n"},
		{
			name:    "text",
			entries: []history.Entry{remote, local},
			want:    "Recently Played (2 tracks)\n\n    1. Artist - Song  (3m ago)\n    2. A  (2h ago)\n",
		},
		{
			name:       "json keeps provider meta",
			entries:    []history.Entry{remote, local},
			jsonOutput: true,
			want: `[
  {
    "played_at": "2026-09-30T11:57:00Z",
    "path": "https://music.example.com/rest/stream?id=1",
    "title": "Song",
    "artist": "Artist",
    "duration_secs": 200,
    "provider_meta": {
      "navidrome.id": "1"
    }
  },
  {
    "played_at": "2026-09-30T10:00:00Z",
    "path": "/music/a.mp3",
    "title": "A"
  }
]
`,
		},
		{
			name:       "json marks a restricted track",
			entries:    []history.Entry{restricted},
			jsonOutput: true,
			want: `[
  {
    "played_at": "2026-09-30T11:00:00Z",
    "path": "https://www.mixcloud.com/creator/show/",
    "title": "Show",
    "restricted": true
  }
]
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			if err := writeHistory(&out, tt.entries, tt.jsonOutput, now); err != nil {
				t.Fatalf("writeHistory: %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("writeHistory output:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}
