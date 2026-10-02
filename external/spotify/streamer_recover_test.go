package spotify

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	librespotPlayer "github.com/devgianlu/go-librespot/player"
)

// droppingSource fails every read, as a source does after the network drops.
type droppingSource struct {
	position int64
}

func (*droppingSource) Read([]float32) (int, error) {
	return 0, errors.New("connection reset by peer")
}
func (*droppingSource) SetPositionMs(int64) error { return errors.New("connection reset by peer") }
func (s *droppingSource) PositionMs() int64       { return s.position }

// valueSource fills every read with one sample value.
type valueSource struct {
	value    float32
	position atomic.Int64
}

func (s *valueSource) Read(p []float32) (int, error) {
	for i := range p {
		p[i] = s.value
	}
	return len(p), nil
}
func (s *valueSource) SetPositionMs(ms int64) error {
	s.position.Store(ms)
	return nil
}
func (s *valueSource) PositionMs() int64 { return s.position.Load() }

func noRecoverBackoff(t *testing.T) {
	t.Helper()
	original := spotifyRecoverBackoff
	spotifyRecoverBackoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { spotifyRecoverBackoff = original })
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (s *spotifyStreamer) isRecovering() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recovering
}

func TestSpotifyStreamerRecoversAfterConnectionDrop(t *testing.T) {
	noRecoverBackoff(t)
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: &droppingSource{position: 42_000}}, nil)
	opened := make(chan int64, 1)
	s.reopen = func(_ context.Context, positionMs int64) (*librespotPlayer.Stream, context.CancelFunc, error) {
		opened <- positionMs
		return &librespotPlayer.Stream{Source: &valueSource{value: 0.5}}, func() {}, nil
	}

	samples := make([][2]float64, 4)
	samples[0] = [2]float64{1, 1}
	n, ok := s.Stream(samples)
	if !ok || n != len(samples) {
		t.Fatalf("Stream() during drop = (%d, %v), want silence that keeps the track alive", n, ok)
	}
	if samples[0] != [2]float64{} {
		t.Fatalf("samples[0] = %v, want silence", samples[0])
	}

	select {
	case got := <-opened:
		if got != 42_000 {
			t.Fatalf("reopen position = %d ms, want 42000", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream was not reopened")
	}
	waitFor(t, "recovery", func() bool { return !s.isRecovering() })

	if n, ok := s.Stream(samples); !ok || n != len(samples) || samples[0][0] != 0.5 {
		t.Fatalf("Stream() after recovery = (%d, %v, %v), want samples from the new source", n, ok, samples[0])
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}

func TestSpotifyStreamerEndsTrackWhenReconnectFails(t *testing.T) {
	noRecoverBackoff(t)
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: &droppingSource{}}, nil)
	var calls atomic.Int32
	s.reopen = func(context.Context, int64) (*librespotPlayer.Stream, context.CancelFunc, error) {
		calls.Add(1)
		return nil, nil, errors.New("no route to host")
	}

	s.Stream(make([][2]float64, 4))
	waitFor(t, "reconnect to give up", func() bool { return s.Err() != nil })

	if got := calls.Load(); got != spotifyRecoverAttempts {
		t.Fatalf("reopen calls = %d, want %d", got, spotifyRecoverAttempts)
	}
	if n, ok := s.Stream(make([][2]float64, 4)); ok || n != 0 {
		t.Fatalf("Stream() after failed reconnect = (%d, %v), want the track to end", n, ok)
	}
}

func TestSpotifyStreamerWithoutReopenEndsTrack(t *testing.T) {
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: &droppingSource{}}, nil)
	if n, ok := s.Stream(make([][2]float64, 4)); ok || n != 0 {
		t.Fatalf("Stream() = (%d, %v), want the track to end", n, ok)
	}
	if s.Err() == nil {
		t.Fatal("Err() = nil, want the read error")
	}
}

func TestSpotifyStreamerSeekDuringRecoveryMovesResumePosition(t *testing.T) {
	noRecoverBackoff(t)
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: &droppingSource{position: 1_000}}, nil)
	positions := make(chan int64, 2)
	proceed := make(chan struct{})
	var calls atomic.Int32
	s.reopen = func(_ context.Context, positionMs int64) (*librespotPlayer.Stream, context.CancelFunc, error) {
		positions <- positionMs
		if calls.Add(1) == 1 {
			<-proceed
			return nil, nil, errors.New("timeout")
		}
		return &librespotPlayer.Stream{Source: &valueSource{}}, func() {}, nil
	}

	s.Stream(make([][2]float64, 4))
	if got := <-positions; got != 1_000 {
		t.Fatalf("first reopen position = %d ms, want 1000", got)
	}
	if err := s.Seek(10 * spotifySampleRate); err != nil {
		t.Fatalf("Seek() during recovery = %v, want nil", err)
	}
	if got := s.Position(); got != 10*spotifySampleRate {
		t.Fatalf("Position() during recovery = %d, want %d", got, 10*spotifySampleRate)
	}
	close(proceed)
	if got := <-positions; got != 10_000 {
		t.Fatalf("second reopen position = %d ms, want 10000", got)
	}
}

func TestSpotifyStreamerCloseDuringRecoveryReleasesNewStream(t *testing.T) {
	noRecoverBackoff(t)
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: &droppingSource{}}, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var cancelled atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	s.reopen = func(context.Context, int64) (*librespotPlayer.Stream, context.CancelFunc, error) {
		defer wg.Done()
		close(started)
		<-release
		return &librespotPlayer.Stream{Source: &valueSource{}}, func() { cancelled.Store(true) }, nil
	}

	s.Stream(make([][2]float64, 4))
	<-started
	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	close(release)
	wg.Wait()
	waitFor(t, "the late stream to be cancelled", cancelled.Load)
	if err := s.Err(); err != nil {
		t.Fatalf("Err() after Close = %v, want nil", err)
	}
}

func TestSpotifyStreamerSeekDuringSuccessfulReopenMovesNewStream(t *testing.T) {
	noRecoverBackoff(t)
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: &droppingSource{position: 1_000}}, nil)
	opening := make(chan struct{})
	proceed := make(chan struct{})
	replacement := &valueSource{}
	s.reopen = func(_ context.Context, positionMs int64) (*librespotPlayer.Stream, context.CancelFunc, error) {
		replacement.position.Store(positionMs)
		close(opening)
		<-proceed
		return &librespotPlayer.Stream{Source: replacement}, func() {}, nil
	}

	s.Stream(make([][2]float64, 4))
	<-opening
	if err := s.Seek(20 * spotifySampleRate); err != nil {
		t.Fatalf("Seek() during reopen = %v, want nil", err)
	}
	close(proceed)
	waitFor(t, "recovery", func() bool { return !s.isRecovering() })

	if got := replacement.PositionMs(); got != 20_000 {
		t.Fatalf("new stream position = %d ms, want 20000 from the seek", got)
	}
}
