//go:build !windows

// Package spotify integrates Spotify playback into cliamp via go-librespot.
package spotify

import (
	"io"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/gopxl/beep/v2"
)

const (
	spotifySampleRate = 44100
	spotifyChannels   = 2
)

// SpotifyStreamer bridges a go-librespot AudioSource to beep.StreamSeekCloser.
// go-librespot outputs interleaved stereo float32 at 44100Hz; this converts
// to Beep's [][2]float64 sample format.
type SpotifyStreamer struct {
	source     librespot.AudioSource
	stream     *librespotPlayer.Stream
	buf        []float32
	durationMs int64
	err        error
}

// NewSpotifyStreamer wraps a go-librespot Stream as a beep.StreamSeekCloser.
func NewSpotifyStreamer(stream *librespotPlayer.Stream) *SpotifyStreamer {
	var dur int64
	if stream.Media != nil {
		dur = int64(stream.Media.Duration())
	}
	return &SpotifyStreamer{
		source:     stream.Source,
		stream:     stream,
		durationMs: dur,
	}
}

// Stream reads interleaved float32 from the AudioSource and converts to
// [][2]float64 stereo pairs for Beep's audio pipeline.
func (s *SpotifyStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	// Each stereo sample pair needs 2 float32 values (L, R).
	needed := len(samples) * spotifyChannels
	if len(s.buf) < needed {
		s.buf = make([]float32, needed)
	}

	nRead, err := s.source.Read(s.buf[:needed])
	if err != nil && err != io.EOF {
		s.err = err
		return 0, false
	}

	// Ensure we only process complete stereo pairs (drop any trailing mono sample).
	nRead -= nRead % spotifyChannels

	// Convert interleaved float32 [L0,R0,L1,R1,...] to [][2]float64 pairs.
	pairs := nRead / spotifyChannels
	for i := range pairs {
		samples[i][0] = float64(s.buf[i*2])
		samples[i][1] = float64(s.buf[i*2+1])
	}

	if pairs == 0 && err == io.EOF {
		return 0, false
	}
	return pairs, true
}

func (s *SpotifyStreamer) Err() error { return s.err }

// Len returns the total number of sample pairs (at 44100Hz stereo).
func (s *SpotifyStreamer) Len() int {
	return int(s.durationMs * spotifySampleRate / 1000)
}

// Position returns the current playback position in sample pairs.
func (s *SpotifyStreamer) Position() int {
	return int(s.source.PositionMs() * spotifySampleRate / 1000)
}

// Seek moves to sample position p (in sample pairs at 44100Hz).
func (s *SpotifyStreamer) Seek(p int) error {
	ms := int64(p) * 1000 / spotifySampleRate
	return s.source.SetPositionMs(ms)
}

// Close releases the stream resources.
// The underlying AudioSource (vorbis.Decoder or flac.Decoder) has a Close()
// method but the AudioSource interface does not expose it. The chunked HTTP
// reader and decryption pipeline will be released when the object is GC'd.
// This is a known limitation for skipped tracks until go-librespot exposes
// Close() on the AudioSource interface.
func (s *SpotifyStreamer) Close() error {
	return nil
}

// Format returns the Beep audio format for Spotify streams.
func (s *SpotifyStreamer) Format() beep.Format {
	return beep.Format{
		SampleRate:  beep.SampleRate(spotifySampleRate),
		NumChannels: spotifyChannels,
		Precision:   4, // float32 = 4 bytes
	}
}

// Duration returns the track duration.
func (s *SpotifyStreamer) Duration() time.Duration {
	return time.Duration(s.durationMs) * time.Millisecond
}
