package player

import (
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
)

const (
	livePrefetchBufferDuration = 4 * time.Second
	livePrefetchResumeDuration = 500 * time.Millisecond
	livePrefetchFadeSamples    = 64
)

// livePrefetchStreamer decodes a live network stream away from the speaker
// callback. Its Stream method never waits for network data: it returns buffered
// samples or silence, then waits for a small refill before resuming after an
// underrun so slow chunk delivery does not alternate rapidly between both.
type livePrefetchStreamer struct {
	src beep.Streamer

	mu         sync.Mutex
	cond       *sync.Cond
	buf        [][2]float64
	r          int
	w          int
	full       bool
	resumeAt   int
	consumed   int64
	sampleRate beep.SampleRate
	sourceErr  error
	err        error
	done       bool
	closed     bool

	buffering bool
	wasSilent bool
	fillDone  chan struct{}
}

func newLivePrefetchStreamer(src beep.Streamer, sampleRate beep.SampleRate) *livePrefetchStreamer {
	capacity := max(sampleRate.N(livePrefetchBufferDuration), 1)
	resumeAt := min(max(sampleRate.N(livePrefetchResumeDuration), 1), capacity)
	p := &livePrefetchStreamer{
		src:        src,
		buf:        make([][2]float64, capacity),
		resumeAt:   resumeAt,
		sampleRate: sampleRate,
		buffering:  true,
		fillDone:   make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	go p.fill()
	return p
}

func (p *livePrefetchStreamer) fill() {
	defer close(p.fillDone)
	chunk := make([][2]float64, 4096)
	for {
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return
		}

		n, ok := p.src.Stream(chunk)
		if n > 0 && !p.write(chunk[:n]) {
			return
		}
		if ok {
			continue
		}

		err := p.src.Err()
		p.mu.Lock()
		p.done = true
		p.sourceErr = err
		p.cond.Broadcast()
		p.mu.Unlock()
		return
	}
}

func (p *livePrefetchStreamer) write(samples [][2]float64) bool {
	for len(samples) > 0 {
		p.mu.Lock()
		for !p.closed && p.availableToWriteLocked() == 0 {
			p.cond.Wait()
		}
		if p.closed {
			p.mu.Unlock()
			return false
		}

		n := min(len(samples), p.availableToWriteLocked())
		for i := range n {
			p.buf[p.w] = samples[i]
			p.w = (p.w + 1) % len(p.buf)
		}
		if n > 0 && p.w == p.r {
			p.full = true
		}
		samples = samples[n:]
		p.cond.Broadcast()
		p.mu.Unlock()
	}
	return true
}

func (p *livePrefetchStreamer) availableToWriteLocked() int {
	if p.full {
		return 0
	}
	if p.w >= p.r {
		return len(p.buf) - (p.w - p.r)
	}
	return p.r - p.w
}

func (p *livePrefetchStreamer) availableToReadLocked() int {
	if p.full {
		return len(p.buf)
	}
	if p.w >= p.r {
		return p.w - p.r
	}
	return len(p.buf) - (p.r - p.w)
}

// Stream returns immediately because Beep calls it while holding the global
// speaker mutex. Waiting here would freeze both audio output and UI controls.
func (p *livePrefetchStreamer) Stream(samples [][2]float64) (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	available := p.availableToReadLocked()
	if p.buffering && !p.done && available < p.resumeAt {
		clear(samples)
		p.wasSilent = true
		return len(samples), true
	}
	if available == 0 {
		if p.done {
			p.err = p.sourceErr
			return 0, false
		}
		clear(samples)
		p.buffering = true
		p.wasSilent = true
		return len(samples), true
	}
	p.buffering = false

	n := min(len(samples), available)
	for i := range n {
		samples[i] = p.buf[p.r]
		p.r = (p.r + 1) % len(p.buf)
	}
	p.consumed += int64(n)
	p.full = false
	p.cond.Broadcast()

	if p.wasSilent {
		fadeLen := min(n, livePrefetchFadeSamples)
		for i := range fadeLen {
			gain := float64(i) / float64(fadeLen)
			samples[i][0] *= gain
			samples[i][1] *= gain
		}
	}

	short := len(samples) - n
	if short > 0 && n > 0 {
		fadeLen := min(n, livePrefetchFadeSamples)
		for i := range fadeLen {
			gain := float64(fadeLen-1-i) / float64(fadeLen)
			idx := n - fadeLen + i
			samples[idx][0] *= gain
			samples[idx][1] *= gain
		}
	}
	clear(samples[n:])
	p.wasSilent = short > 0
	if short > 0 && !p.done {
		p.buffering = true
	}
	return len(samples), true
}

func (p *livePrefetchStreamer) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *livePrefetchStreamer) Position() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sampleRate.D(int(p.consumed))
}

// Close stops buffered writes. The pipeline closes the decoder immediately
// afterward to unblock fill if it is waiting in a network read.
func (p *livePrefetchStreamer) Close() {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *livePrefetchStreamer) Wait() {
	<-p.fillDone
}

var _ beep.Streamer = (*livePrefetchStreamer)(nil)
