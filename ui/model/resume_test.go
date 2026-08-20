package model

import (
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestTakeResume(t *testing.T) {
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
			if got := m.takeResume(tc.track); got != tc.want {
				t.Fatalf("takeResume() = %v, want %v", got, tc.want)
			}
			// Taking it clears it, so applyResume does not seek again.
			if again := m.takeResume(tc.track); again != 0 {
				t.Errorf("second takeResume() = %v, want 0", again)
			}
		})
	}
}
