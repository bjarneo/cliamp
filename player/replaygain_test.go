package player

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/internal/replaygain"
)

// constStreamer yields a constant sample forever.
type constStreamer float64

func (c constStreamer) Stream(s [][2]float64) (int, bool) {
	for i := range s {
		s[i] = [2]float64{float64(c), float64(c)}
	}
	return len(s), true
}
func (constStreamer) Err() error { return nil }

func firstSample(s beep.Streamer) float64 {
	buf := make([][2]float64, 4)
	s.Stream(buf)
	return buf[0][0]
}

// taggedFile writes an MP3 with ReplayGain frames to a temp dir; no frames
// gives a file without tags.
func taggedFile(t *testing.T, frames ...[]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "track.mp3")
	body := bytes.Repeat([]byte{0xff, 0xfb}, 4096)
	if len(frames) > 0 {
		body = id3Tag(0, frames...)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyReplayGain(t *testing.T) {
	tagged := taggedFile(t, id3TXXX("REPLAYGAIN_TRACK_GAIN", "-6 dB"), id3TXXX("REPLAYGAIN_TRACK_PEAK", "0.5"))
	untagged := taggedFile(t)
	tests := []struct {
		name   string
		path   string
		mode   string
		preamp float64
		want   float64
	}{
		{"off leaves the stream alone", tagged, replaygain.ModeOff, 0, 1},
		{"no mode set leaves the stream alone", tagged, "", 0, 1},
		{"track gain from the tags", tagged, replaygain.ModeTrack, 0, math.Pow(10, -6.0/20)},
		{"preamp adds to it", tagged, replaygain.ModeTrack, 3, math.Pow(10, -3.0/20)},
		{"a boost stops at the peak", tagged, replaygain.ModeTrack, 15, 2},
		{"no tags: unchanged", untagged, replaygain.ModeTrack, 0, 1},
		{"a URL without a download: unchanged", "http://nas:4533/rest/stream?id=1", replaygain.ModeTrack, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &Player{}
			p.SetReplayGain(tc.mode, tc.preamp)
			tp := &trackPipeline{path: tc.path, stream: constStreamer(1)}
			p.applyReplayGain(tp)
			if got := firstSample(tp.stream); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("sample = %v, want %v", got, tc.want)
			}
		})
	}
}

// The player reports the gain it applied to the current track, and which value
// it came from.
func TestReplayGainApplied(t *testing.T) {
	p := &Player{}
	p.SetReplayGain(replaygain.ModeAlbum, 0)
	tp := &trackPipeline{path: taggedFile(t, id3TXXX("REPLAYGAIN_TRACK_GAIN", "-6 dB")), stream: constStreamer(1)}
	p.applyReplayGain(tp)
	p.current = tp
	if db, used := p.ReplayGainApplied(); db != -6 || used != replaygain.ModeTrack {
		t.Errorf("ReplayGainApplied = %v, %q; want -6, track (album mode fell back)", db, used)
	}
	p.current = &trackPipeline{path: "/music/untagged.mp3"}
	if _, used := p.ReplayGainApplied(); used != "" {
		t.Errorf("a track without gain reports %q", used)
	}
}
