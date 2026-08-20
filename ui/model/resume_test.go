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
			// applyResume clears the hint once playback starts, so a failed
			// PlayAt can still be retried at the same position.
			if again := m.startPosition(tc.track)(); again != tc.want {
				t.Errorf("second startPosition() = %v, want %v", again, tc.want)
			}
		})
	}
}

type zeroPositionProv struct{ plainProv }

func (p *zeroPositionProv) TrackPosition(playlist.Track) time.Duration { return 0 }

// A provider's 0 means "start over", so it must win over a stale startup hint
// rather than being read as "no answer".
func TestStartPositionProviderZeroWins(t *testing.T) {
	track := playlist.Track{Path: "http://example/item", Stream: true}
	m := Model{provider: &zeroPositionProv{}}
	m.resume.path = track.Path
	m.resume.secs = 95

	if got := m.startPosition(track)(); got != 0 {
		t.Errorf("startPosition() = %v, want 0", got)
	}
}

// Resolving a position must not spend the hint, so a failed PlayAt can be
// retried at the same position; applyResume clears it once playback starts.
func TestStartPositionKeepsHintUntilPlaybackStarts(t *testing.T) {
	track := playlist.Track{Path: "http://example/item", Stream: true}
	m := Model{provider: &zeroPositionProv{}}
	m.resume.path = track.Path
	m.resume.secs = 95

	m.startPosition(track)()

	if m.resume.secs != 95 {
		t.Errorf("resume.secs = %d, want 95", m.resume.secs)
	}
}
