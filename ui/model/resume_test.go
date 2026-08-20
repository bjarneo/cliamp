package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestStartPosition(t *testing.T) {
	tests := []struct {
		name       string
		resumePath string
		resumeSecs int
		track      playlist.Track
		want       time.Duration
	}{
		{
			name:       "matching track",
			resumePath: "/a.flac",
			resumeSecs: 95,
			track:      playlist.Track{Path: "/a.flac"},
			want:       95 * time.Second,
		},
		{
			name:       "different track",
			resumePath: "/a.flac",
			resumeSecs: 95,
			track:      playlist.Track{Path: "/b.flac"},
		},
		{
			name:  "nothing saved",
			track: playlist.Track{Path: "/a.flac"},
		},
		{
			name:       "zero seconds",
			resumePath: "/a.flac",
			resumeSecs: 0,
			track:      playlist.Track{Path: "/a.flac"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{}
			m.resume.path = tc.resumePath
			m.resume.secs = tc.resumeSecs
			if got := m.startPosition(tc.track)(); got != tc.want {
				t.Fatalf("startPosition() = %v, want %v", got, tc.want)
			}
			// The startup hint is consumed, so applyResume does not seek again.
			if again := m.startPosition(tc.track)(); again != 0 {
				t.Errorf("second startPosition() = %v, want 0", again)
			}
		})
	}
}
