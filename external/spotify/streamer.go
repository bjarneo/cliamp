// Package spotify integrates Spotify playback into cliamp via go-librespot.
package spotify

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/applog"
)

const (
	spotifySampleRate = 44100
	spotifyChannels   = 2

	// spotifyRecoverAttempts bounds how often one recovery tries to reopen
	// the stream after the connection drops.
	spotifyRecoverAttempts = 5
	// spotifyMaxRecoveries bounds recoveries per track, so a stream that
	// fails at the same point every time cannot loop forever.
	spotifyMaxRecoveries = 3
)

// spotifyRecoverBackoff returns the wait before reopen attempt n+1. Tests
// replace it to run without delays.
var spotifyRecoverBackoff = func(attempt int) time.Duration {
	return time.Second << attempt
}

// spotifyReopenFunc opens a new stream for the same track at positionMs.
type spotifyReopenFunc func(ctx context.Context, positionMs int64) (*librespotPlayer.Stream, context.CancelFunc, error)

// spotifyStreamer bridges a go-librespot AudioSource to beep.StreamSeekCloser.
// go-librespot outputs interleaved stereo float32 at 44100Hz; this converts
// to Beep's [][2]float64 sample format.
//
// When the connection drops mid-track and reopen is set, the streamer plays
// silence while a goroutine reopens the stream at the same position. This
// keeps the gapless streamer from treating the drop as the end of the track.
type spotifyStreamer struct {
	mu         sync.Mutex
	source     librespot.AudioSource
	buf        []float32
	durationMs int64
	err        error
	closing    atomic.Bool
	closed     bool

	cancelMu sync.Mutex
	cancel   func() // cancels the I/O of the stream that feeds source

	reopen     spotifyReopenFunc // nil disables recovery
	recovering bool
	recoveries int
	resumeMs   int64 // position to reopen at, and to report, while recovering
	life       context.Context
	stop       context.CancelFunc // stops a recovery when the streamer closes
}

// newSpotifyStreamer wraps a go-librespot Stream as a beep.StreamSeekCloser.
func newSpotifyStreamer(stream *librespotPlayer.Stream, cancel func()) *spotifyStreamer {
	var dur int64
	if stream.Media != nil {
		dur = int64(stream.Media.Duration())
	}
	if cancel == nil {
		cancel = func() {}
	}
	life, stop := context.WithCancel(context.Background())
	return &spotifyStreamer{
		source:     stream.Source,
		durationMs: dur,
		cancel:     cancel,
		life:       life,
		stop:       stop,
	}
}

// Stream reads interleaved float32 from the AudioSource and converts to
// [][2]float64 stereo pairs for Beep's audio pipeline.
func (s *spotifyStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	if s.closing.Load() {
		return 0, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.source == nil || s.err != nil {
		return 0, false
	}
	if s.recovering {
		clear(samples)
		return len(samples), true
	}

	// Each stereo sample pair needs 2 float32 values (L, R).
	needed := len(samples) * spotifyChannels
	if len(s.buf) < needed {
		s.buf = make([]float32, needed)
	}

	nRead, err := s.source.Read(s.buf[:needed])
	if err != nil && err != io.EOF {
		if s.closing.Load() {
			return 0, false
		}
		if s.startRecoveryLocked(err) {
			clear(samples)
			return len(samples), true
		}
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

// startRecoveryLocked starts a goroutine that reopens the stream at the
// current position. It returns false when recovery is off or used up.
// s.mu must be held.
func (s *spotifyStreamer) startRecoveryLocked(cause error) bool {
	if s.reopen == nil || s.recoveries >= spotifyMaxRecoveries {
		return false
	}
	s.recoveries++
	s.recovering = true
	s.resumeMs = s.source.PositionMs()
	go s.recover(cause)
	return true
}

// recover reopens the stream with backoff. On success the new source
// replaces the old one. On failure the streamer ends with an error, so
// playback moves to the next track.
func (s *spotifyStreamer) recover(cause error) {
	applog.UserWarn("spotify: stream connection lost (%v), reconnecting...", cause)
	err := cause
	for attempt := range spotifyRecoverAttempts {
		if attempt > 0 {
			select {
			case <-s.life.Done():
				return
			case <-time.After(spotifyRecoverBackoff(attempt - 1)):
			}
		}
		s.mu.Lock()
		positionMs := s.resumeMs
		s.mu.Unlock()

		stream, cancel, openErr := s.reopen(s.life, positionMs)
		if openErr == nil {
			if s.adopt(stream, cancel, positionMs) {
				applog.Status("spotify: stream reconnected")
			}
			return
		}
		if s.life.Err() != nil {
			return
		}
		err = openErr
	}

	s.mu.Lock()
	s.recovering = false
	closing := s.closing.Load()
	if !closing {
		s.err = fmt.Errorf("spotify: stream reconnect failed: %w", err)
	}
	s.mu.Unlock()
	if !closing {
		applog.UserError("spotify: stream reconnect failed, skipping the track: %v", err)
	}
}

// adopt swaps in a stream that was reopened at openedMs. A seek during the
// reopen moved resumeMs, so adopt seeks the new stream to it. It returns
// false and releases the stream when the streamer closed while the stream
// was opening, or when that seek fails.
func (s *spotifyStreamer) adopt(stream *librespotPlayer.Stream, cancel context.CancelFunc, openedMs int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.closed {
		cancel()
		return false
	}
	if s.resumeMs != openedMs {
		if err := stream.Source.SetPositionMs(s.resumeMs); err != nil {
			cancel()
			s.recovering = false
			s.err = fmt.Errorf("spotify: seek reconnected stream: %w", err)
			applog.UserError("spotify: stream reconnect failed, skipping the track: %v", s.err)
			return false
		}
	}
	s.source = stream.Source
	s.recovering = false

	s.cancelMu.Lock()
	old := s.cancel
	s.cancel = cancel
	s.cancelMu.Unlock()
	old()
	// Close sets closing before it cancels, so a Close that cancelled the old
	// stream before the swap is visible here.
	if s.closing.Load() {
		cancel()
	}
	return true
}

// cancelStream cancels the I/O of the current stream.
func (s *spotifyStreamer) cancelStream() {
	s.cancelMu.Lock()
	cancel := s.cancel
	s.cancelMu.Unlock()
	cancel()
}

func (s *spotifyStreamer) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Len returns the total number of sample pairs (at 44100Hz stereo).
func (s *spotifyStreamer) Len() int {
	return int(s.durationMs * spotifySampleRate / 1000)
}

// Position returns the current playback position in sample pairs.
func (s *spotifyStreamer) Position() int {
	if s.closing.Load() {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.source == nil {
		return 0
	}
	if s.recovering {
		return int(s.resumeMs * spotifySampleRate / 1000)
	}
	return int(s.source.PositionMs() * spotifySampleRate / 1000)
}

// Seek moves to sample position p (in sample pairs at 44100Hz).
func (s *spotifyStreamer) Seek(p int) error {
	if s.closing.Load() {
		return net.ErrClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.source == nil {
		return net.ErrClosed
	}
	ms := int64(p) * 1000 / spotifySampleRate
	if s.recovering {
		// The old source is broken. Reopen at the new position instead.
		s.resumeMs = ms
		return nil
	}
	return s.source.SetPositionMs(ms)
}

// Close cancels stream I/O and releases cliamp's references. AudioSource does
// not expose Close intentionally; calling the concrete C-backed decoder Close
// methods during a track handoff can crash go-librespot.
func (s *spotifyStreamer) Close() error {
	if s.closing.CompareAndSwap(false, true) {
		// A decoder read may be waiting on a chunk request while holding mu.
		// Cancel it before waiting to close the decoder.
		s.stop()
		s.cancelStream()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	s.source = nil
	s.buf = nil
	return nil
}

// Format returns the Beep audio format for Spotify streams.
func (s *spotifyStreamer) Format() beep.Format {
	return beep.Format{
		SampleRate:  beep.SampleRate(spotifySampleRate),
		NumChannels: spotifyChannels,
		Precision:   4, // float32 = 4 bytes
	}
}

// Duration returns the track duration.
func (s *spotifyStreamer) Duration() time.Duration {
	return time.Duration(s.durationMs) * time.Millisecond
}
