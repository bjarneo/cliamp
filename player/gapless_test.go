package player

import (
	"sync"
	"testing"

	"github.com/gopxl/beep/v2"
)

type blockingEndStreamer struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingEndStreamer) Stream([][2]float64) (int, bool) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return 0, false
}

func (*blockingEndStreamer) Err() error { return nil }

func TestGaplessManualReplaceWinsOverExhaustedStream(t *testing.T) {
	old := &blockingEndStreamer{started: make(chan struct{}), release: make(chan struct{})}
	manual := &playbackTestDecoder{}
	g := &gaplessStreamer{}
	g.Replace(old)

	done := make(chan struct{})
	go func() {
		var samples [1][2]float64
		g.Stream(samples[:])
		close(done)
	}()
	<-old.started

	// A manual track switch must not be overwritten when the old Stream call
	// subsequently reports EOF.
	g.Replace(manual)
	close(old.release)
	<-done

	g.mu.Lock()
	current := g.current
	g.mu.Unlock()
	if current != manual {
		t.Fatal("manual replacement was overwritten by stale gapless transition")
	}
}

func TestPlayerIgnoresStaleGaplessCallback(t *testing.T) {
	oldDecoder := newPlaybackTestDecoder()
	manualDecoder := newPlaybackTestDecoder()
	preloadedDecoder := newPlaybackTestDecoder()
	old := &trackPipeline{decoder: oldDecoder, stream: oldDecoder}
	manual := &trackPipeline{decoder: manualDecoder, stream: manualDecoder}
	preloaded := &trackPipeline{decoder: preloadedDecoder, stream: preloadedDecoder, gaplessToken: 2}
	p := &Player{current: manual, nextPipeline: preloaded}

	// Token 1 belonged to the old preloaded track. It must not close or replace
	// the manually selected track after a later replacement has won.
	p.handleGaplessSwap(1)
	if p.current != manual {
		t.Fatal("stale callback replaced the manually selected pipeline")
	}
	select {
	case <-manualDecoder.closed:
		t.Fatal("stale callback closed the manually selected pipeline")
	default:
	}

	p.current = old
	p.handleGaplessSwap(2)
	if p.current != preloaded || p.nextPipeline != nil {
		t.Fatal("matching callback did not promote the preloaded pipeline")
	}
	select {
	case <-oldDecoder.closed:
	default:
		t.Fatal("matching callback did not close the old pipeline")
	}
}

var _ beep.Streamer = (*blockingEndStreamer)(nil)
