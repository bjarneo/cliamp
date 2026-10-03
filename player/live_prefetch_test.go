package player

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
)

type gatedLiveStreamer struct {
	mu      sync.Mutex
	samples [][2]float64
	pos     int
	gate    <-chan struct{}
	err     error
}

type closeUnblocksStreamer struct {
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newCloseUnblocksStreamer() *closeUnblocksStreamer {
	return &closeUnblocksStreamer{started: make(chan struct{}), release: make(chan struct{})}
}

func (s *closeUnblocksStreamer) Stream([][2]float64) (int, bool) {
	s.startOnce.Do(func() { close(s.started) })
	<-s.release
	return 0, false
}

func (*closeUnblocksStreamer) Err() error     { return nil }
func (*closeUnblocksStreamer) Len() int       { return 0 }
func (*closeUnblocksStreamer) Position() int  { return 0 }
func (*closeUnblocksStreamer) Seek(int) error { return nil }
func (s *closeUnblocksStreamer) Close() error {
	s.closeOnce.Do(func() { close(s.release) })
	return nil
}

func (s *gatedLiveStreamer) Stream(samples [][2]float64) (int, bool) {
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos >= len(s.samples) {
		return 0, false
	}
	n := copy(samples, s.samples[s.pos:])
	s.pos += n
	return n, true
}

func (s *gatedLiveStreamer) Err() error { return s.err }

func waitForLiveSamples(t *testing.T, p *livePrefetchStreamer, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		available := p.availableToReadLocked()
		p.mu.Unlock()
		if available >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("prefetch available samples = %d, want at least %d", available, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLivePrefetchNeverBlocksOnSource(t *testing.T) {
	gate := make(chan struct{})
	src := &gatedLiveStreamer{samples: make([][2]float64, 1), gate: gate}
	p := newLivePrefetchStreamer(src, 1000)

	done := make(chan struct{})
	go func() {
		defer close(done)
		out := make([][2]float64, 128)
		n, ok := p.Stream(out)
		if !ok || n != len(out) {
			t.Errorf("Stream() = (%d, %v), want (%d, true)", n, ok, len(out))
		}
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Stream blocked on a stalled source")
	}
	p.Close()
	close(gate)
	select {
	case <-p.fillDone:
	case <-time.After(time.Second):
		t.Fatal("fill goroutine did not stop after source was released")
	}
}

func TestLivePrefetchBuffersBeforeResuming(t *testing.T) {
	gate := make(chan struct{}, 1)
	samples := make([][2]float64, 600)
	for i := range samples {
		samples[i] = [2]float64{1, 1}
	}
	src := &gatedLiveStreamer{samples: samples, gate: gate}
	p := newLivePrefetchStreamer(src, 1000)
	defer func() {
		p.Close()
		close(gate)
	}()

	initial := make([][2]float64, 100)
	if n, ok := p.Stream(initial); !ok || n != len(initial) {
		t.Fatalf("initial Stream() = (%d, %v), want (%d, true)", n, ok, len(initial))
	}
	for i, sample := range initial {
		if sample != ([2]float64{}) {
			t.Fatalf("initial sample %d = %v, want silence while buffering", i, sample)
		}
	}

	gate <- struct{}{}
	waitForLiveSamples(t, p, 500)
	out := make([][2]float64, 100)
	if n, ok := p.Stream(out); !ok || n != len(out) {
		t.Fatalf("buffered Stream() = (%d, %v), want (%d, true)", n, ok, len(out))
	}
	if out[0] != ([2]float64{}) {
		t.Fatalf("first resumed sample = %v, want fade from silence", out[0])
	}
	if out[len(out)-1] == ([2]float64{}) {
		t.Fatal("buffered audio remained silent after refill threshold")
	}
}

func waitForLiveDone(t *testing.T, p *livePrefetchStreamer) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		done := p.done
		p.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("source did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLivePrefetchReportsSourceErrorAfterDrain(t *testing.T) {
	wantErr := errors.New("stream failed")
	src := &gatedLiveStreamer{samples: [][2]float64{{1, 1}}, err: wantErr}
	p := newLivePrefetchStreamer(src, 1000)
	defer p.Close()
	waitForLiveDone(t, p)

	if _, ok := p.Stream(make([][2]float64, 4)); !ok {
		t.Fatal("Stream reported completion before draining buffered samples")
	}
	if err := p.Err(); err != nil {
		t.Fatalf("Err() = %v while Stream still succeeds, want nil", err)
	}
	if _, ok := p.Stream(make([][2]float64, 4)); ok {
		t.Fatal("Stream still succeeds after buffered samples drained")
	}
	if err := p.Err(); !errors.Is(err, wantErr) {
		t.Fatalf("Err() = %v, want %v", err, wantErr)
	}
}

// A source that ends cleanly hands the rest of the last read to the gapless
// next track, so the track boundary has no gap and no fade. A failed source
// still fades out into silence before the next read moves on.
func TestLivePrefetchEndOfSource(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantNext int // samples of the read that the next track fills
	}{
		{name: "clean end", wantNext: 6},
		{name: "failed source", err: errors.New("stream failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := make([][2]float64, 10)
			for i := range samples {
				samples[i] = [2]float64{1, 1}
			}
			p := newLivePrefetchStreamer(&gatedLiveStreamer{samples: samples, err: tt.err}, 1000)
			defer p.Close()
			waitForLiveDone(t, p)

			next := [2]float64{0.5, 0.5}
			g := &gaplessStreamer{}
			g.Replace(p)
			g.SetNext(beep.StreamerFunc(func(s [][2]float64) (int, bool) {
				for i := range s {
					s[i] = next
				}
				return len(s), true
			}))
			out := make([][2]float64, 16)
			g.Stream(out)

			gotNext := 0
			for _, sample := range out[len(samples):] {
				if sample == next {
					gotNext++
				}
			}
			if gotNext != tt.wantNext {
				t.Fatalf("next track filled %d samples of the read, want %d: %v", gotNext, tt.wantNext, out)
			}
			last := out[len(samples)-1]
			if faded := last != samples[0]; faded != (tt.err != nil) {
				t.Fatalf("last sample of the source = %v, faded = %v, want faded = %v", last, faded, tt.err != nil)
			}
		})
	}
}

func TestLivePrefetchPositionTracksConsumedAudio(t *testing.T) {
	src := &gatedLiveStreamer{samples: make([][2]float64, 600)}
	p := newLivePrefetchStreamer(src, 1000)
	defer p.Close()
	waitForLiveSamples(t, p, 500)

	if _, ok := p.Stream(make([][2]float64, 100)); !ok {
		t.Fatal("Stream returned false with buffered audio")
	}
	if got := p.Position(); got != 100*time.Millisecond {
		t.Fatalf("Position() = %v, want 100ms", got)
	}
}

func TestLivePrefetchCloseStopsBufferedWriter(t *testing.T) {
	src := &gatedLiveStreamer{samples: make([][2]float64, 5000)}
	p := newLivePrefetchStreamer(src, 1000)
	waitForLiveSamples(t, p, len(p.buf))
	p.Close()

	select {
	case <-p.fillDone:
	case <-time.After(time.Second):
		t.Fatal("fill goroutine did not stop after Close")
	}
}

func TestTrackPipelineCloseUnblocksPrefetchDecoder(t *testing.T) {
	decoder := newCloseUnblocksStreamer()
	prefetch := newLivePrefetchStreamer(decoder, 1000)
	tp := &trackPipeline{decoder: decoder, stream: prefetch, livePrefetch: prefetch}
	<-decoder.started

	done := make(chan struct{})
	go func() {
		tp.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("trackPipeline.close did not unblock and join the prefetch decoder")
	}
}

var _ beep.Streamer = (*gatedLiveStreamer)(nil)
var _ beep.StreamSeekCloser = (*closeUnblocksStreamer)(nil)
