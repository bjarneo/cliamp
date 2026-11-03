package applemusic

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/bjarneo/cliamp/applog"
)

const (
	ringSeconds     = 4
	prebufferMillis = 150
	firstAudioWait  = 8 * time.Second
	pollInterval    = 400 * time.Millisecond
	endingInterval  = 100 * time.Millisecond
	endMargin       = 0.2 // seconds cut from the end so the next song never leaks
	resyncSeconds   = 1.5
)

// streamer adapts Music's live output to beep.StreamSeekCloser. Stream runs
// under the speaker lock, so it only touches the ring. Anything that talks to
// Music goes through cmds and runs on the run goroutine.
type streamer struct {
	engine  *engine
	req     request
	rate    int
	resolve func(context.Context, request) (string, error)

	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	cmds   chan func(context.Context)

	mu        sync.Mutex
	ring      [][2]float32
	head      int
	count     int
	buffering bool
	waiting   bool // dropping samples until the first audible one
	dropping  bool // dropping samples until Music finishes a seek
	paused    bool
	ended     bool
	position  int
	offset    int // position requested before Music started
	started   bool
	err       error
}

func newStreamer(e *engine, req request, rate int, resolve func(context.Context, request) (string, error)) *streamer {
	ctx, cancel := context.WithCancel(context.Background())
	return &streamer{
		engine:    e,
		req:       req,
		rate:      rate,
		resolve:   resolve,
		ctx:       ctx,
		cancel:    cancel,
		cmds:      make(chan func(context.Context), 16),
		ring:      make([][2]float32, ringSeconds*rate),
		buffering: true,
		waiting:   true,
	}
}

// push adds interleaved stereo samples from the tap. The tap also delivers
// silence while Music is idle, so a pause drops everything and a start skips
// ahead to the first audible sample, which lines position up with the song.
func (s *streamer) push(samples []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused || s.dropping || s.ended {
		return
	}
	for i := 0; i+1 < len(samples); i += 2 {
		l, r := samples[i], samples[i+1]
		if s.waiting {
			if l == 0 && r == 0 {
				continue
			}
			s.waiting = false
		}
		tail := (s.head + s.count) % len(s.ring)
		s.ring[tail] = [2]float32{l, r}
		if s.count == len(s.ring) {
			s.head = (s.head + 1) % len(s.ring)
		} else {
			s.count++
		}
	}
}

// Stream implements beep.Streamer. Silence fills gaps so a slow start does not
// read as the end of the track. Position moves only on real audio.
func (s *streamer) Stream(out [][2]float64) (int, bool) {
	s.once.Do(func() { go s.run() })

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, false
	}
	if s.buffering {
		if s.count < s.rate*prebufferMillis/1000 && !s.ended {
			clear(out)
			return len(out), true
		}
		s.buffering = false
	}
	n := min(len(out), s.count)
	for i := range n {
		f := s.ring[(s.head+i)%len(s.ring)]
		out[i] = [2]float64{float64(f[0]), float64(f[1])}
	}
	s.head = (s.head + n) % len(s.ring)
	s.count -= n
	s.position += n
	if n == len(out) {
		return n, true
	}
	if s.ended {
		return n, n > 0
	}
	s.buffering = true
	clear(out[n:])
	return len(out), true
}

// Err implements beep.Streamer.
func (s *streamer) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Len implements beep.StreamSeeker.
func (s *streamer) Len() int { return s.req.secs * s.rate }

// Position implements beep.StreamSeeker.
func (s *streamer) Position() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.position
}

// Seek implements beep.StreamSeeker. Music seeks on the run goroutine, and
// samples are dropped until it has moved.
func (s *streamer) Seek(p int) error {
	if p < 0 || p > s.Len() {
		return fmt.Errorf("apple music: seek to %d out of range", p)
	}
	s.mu.Lock()
	s.position = p
	s.head, s.count = 0, 0
	s.buffering = true
	if !s.started {
		s.offset = p
		s.mu.Unlock()
		return nil
	}
	s.dropping = true
	s.mu.Unlock()

	seconds := float64(p) / float64(s.rate)
	s.command(func(ctx context.Context) {
		if _, err := osascript(ctx, scriptSeek, fmt.Sprintf("%.3f", seconds)); err != nil {
			applog.Warn("apple music: seek: %v", err)
		}
		s.mu.Lock()
		s.dropping, s.waiting = false, true
		s.mu.Unlock()
	})
	return nil
}

// SetPaused implements player.PauseObserver.
func (s *streamer) SetPaused(paused bool) {
	s.mu.Lock()
	s.paused = paused
	started := s.started
	s.mu.Unlock()
	if !started {
		return
	}
	s.command(func(ctx context.Context) {
		verb := "pause"
		if !paused {
			verb = "play"
			s.mu.Lock()
			s.waiting = true
			s.mu.Unlock()
		}
		if err := music(ctx, verb); err != nil {
			applog.Warn("apple music: %s: %v", verb, err)
		}
	})
}

func (s *streamer) command(f func(context.Context)) {
	select {
	case s.cmds <- f:
	case <-s.ctx.Done():
	default:
		applog.Warn("apple music: command queue full")
	}
}

// Close implements beep.StreamCloser. It may run under locks, so Music is
// released on another goroutine.
func (s *streamer) Close() error {
	s.cancel()
	s.mu.Lock()
	ran := s.started
	s.mu.Unlock()
	if ran {
		go s.engine.release(s)
	}
	return nil
}

func (s *streamer) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *streamer) markEnded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
}

func (s *streamer) run() {
	if err := s.start(); err != nil {
		// A cancelled start can surface as a killed osascript instead of
		// context.Canceled.
		if s.ctx.Err() == nil {
			applog.Warn("%v", err)
			s.fail(err)
		}
		s.engine.release(s)
		return
	}
	if quality, err := s.engine.ctl(s.ctx, "quality", s.pid()); err == nil {
		applog.Info("apple music: %s: %s", s.req.title, quality)
	}
	s.monitor()
}

func (s *streamer) pid() string {
	pid, _ := musicPID(s.ctx)
	return pid
}

func (s *streamer) start() error {
	e := s.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	// Whatever Music is playing must not reach the speakers, so samples stay
	// dropped until it has stopped.
	s.mu.Lock()
	s.dropping = true
	s.mu.Unlock()
	if err := e.takeOver(s.ctx, s); err != nil {
		return err
	}
	if err := music(s.ctx, "pause"); err != nil {
		return fmt.Errorf("apple music: pause: %w", err)
	}
	s.mu.Lock()
	s.started = true
	offset := s.offset
	paused := s.paused
	s.mu.Unlock()

	if err := s.play(); err != nil {
		return err
	}
	if err := s.verify(); err != nil {
		_ = music(context.Background(), "pause")
		return err
	}
	if offset > 0 {
		s.mu.Lock()
		s.dropping = true
		s.mu.Unlock()
		secs := fmt.Sprintf("%.3f", float64(offset)/float64(s.rate))
		if _, err := osascript(s.ctx, scriptSeek, secs); err != nil {
			applog.Warn("apple music: seek: %v", err)
		}
		s.mu.Lock()
		s.dropping, s.waiting = false, true
		s.mu.Unlock()
	}
	if paused {
		return music(s.ctx, "pause")
	}
	return s.awaitAudio()
}

func (s *streamer) play() error {
	link := s.req.link
	if link == "" {
		found, err := s.resolve(s.ctx, s.req)
		if err != nil {
			return err
		}
		link = found
	}
	return s.engine.playCatalog(s.ctx, link, s.req.title, func() {
		s.mu.Lock()
		s.dropping, s.waiting = false, true
		s.head, s.count = 0, 0
		s.mu.Unlock()
	})
}

// verify confirms Music is playing the requested song, which guards against a
// queued song starting instead.
func (s *streamer) verify() error {
	var last string
	for range 30 {
		st, err := readState(s.ctx)
		if err == nil {
			last = st.name
			if st.status == "playing" && sameTitle(st.name, s.req.title) {
				return nil
			}
		}
		if err := s.ctx.Err(); err != nil {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("apple music: wanted %q, Music has %q", s.req.title, last)
}

// awaitAudio fails with an actionable message when the tap reads only silence,
// which is how macOS reports missing recording permission.
func (s *streamer) awaitAudio() error {
	deadline := time.Now().Add(firstAudioWait)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		waiting := s.waiting
		s.mu.Unlock()
		if !waiting {
			return nil
		}
		if err := s.ctx.Err(); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("apple music: no audio from the tap; allow CliampTap under System Settings > Privacy & Security > Screen & System Audio Recording")
}

// monitor applies queued commands and watches for the end of the song.
func (s *streamer) monitor() {
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case f := <-s.cmds:
			f(s.ctx)
		case <-timer.C:
			next, done := s.poll()
			if done {
				return
			}
			timer.Reset(next)
		}
	}
}

func (s *streamer) poll() (time.Duration, bool) {
	s.mu.Lock()
	paused, dropping := s.paused, s.dropping
	s.mu.Unlock()
	if paused || dropping {
		return pollInterval, false
	}
	st, err := readState(s.ctx)
	if err != nil {
		return pollInterval, false
	}
	if st.status == "stopped" || !sameTitle(st.name, s.req.title) {
		s.finish()
		return 0, true
	}
	if st.dur > 0 && st.position >= st.dur-endMargin {
		s.finish()
		return 0, true
	}

	s.mu.Lock()
	if math.Abs(st.position-float64(s.position)/float64(s.rate)) > resyncSeconds {
		s.position = int(st.position * float64(s.rate))
	}
	s.mu.Unlock()

	if st.dur > 0 && st.dur-st.position < 2 {
		return endingInterval, false
	}
	return pollInterval, false
}

// finish stops Music so its queue does not run on, then lets the ring drain.
func (s *streamer) finish() {
	if err := music(s.ctx, "pause"); err != nil {
		applog.Warn("apple music: pause: %v", err)
	}
	s.markEnded()
}

func sameTitle(a, b string) bool {
	x, y := foldTitle(a), foldTitle(b)
	return x == y || strings.HasPrefix(x, y) || strings.HasPrefix(y, x)
}

func foldTitle(s string) string {
	return strings.ToLower(norm.NFC.String(strings.TrimSpace(s)))
}
