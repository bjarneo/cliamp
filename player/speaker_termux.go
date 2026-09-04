//go:build termux

package player

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
)

// CLIAMP_DEBUG_PULSE=1 prints diagnostic information about the audio
// backend selection. Useful for diagnosing Termux installs where
// PulseAudio isn't running or the runtime socket is in an unexpected
// location. Disabled by default because the TUI cannot tolerate stray
// stderr writes.
const debugPulseEnv = "CLIAMP_DEBUG_PULSE"

func debugPulseLog(format string, args ...any) {
	if os.Getenv(debugPulseEnv) == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[cliamp:pulse] "+format+"\n", args...)
}

type termuxPlayback interface {
	StartContext(context.Context) error
	Done() <-chan struct{}
	Pause()
	Resume()
	Close()
}

type termuxSession struct {
	stream   termuxPlayback
	closeFn  func()
	closeOne sync.Once
}

func (s *termuxSession) Close() {
	if s == nil {
		return
	}
	s.closeOne.Do(func() {
		if s.closeFn != nil {
			s.closeFn()
			return
		}
		if s.stream != nil {
			s.stream.Close()
		}
	})
}

type termuxSessionFactory func(beep.SampleRate, int, func([]float32) (int, error)) (*termuxSession, error)

// termuxSpeaker drives a beep.Mixer through one supervised PulseAudio
// playback session. The mixer lock is intentionally separate from the
// lifecycle lock: PulseAudio requests can block, while the mixer callback
// must remain compatible with SpeakerLock/ SpeakerUnlock.
type termuxSpeaker struct {
	mu       sync.Mutex
	mixer    beep.Mixer
	frameBuf [][2]float64 // reused across callbacks; guarded by mu

	lifecycleMu    sync.Mutex
	session        *termuxSession
	sampleRate     beep.SampleRate
	bufferSize     int
	wantPlayback   bool
	suspended      bool
	closed         bool
	startupCancel  context.CancelFunc
	wake           chan struct{}
	stop           chan struct{}
	supervisorDone chan struct{}
	factory        termuxSessionFactory
}

func (t *termuxSpeaker) Init(sampleRate beep.SampleRate, bufferSize int) error {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	if t.supervisorDone != nil {
		return fmt.Errorf("speaker already initialized")
	}

	t.sampleRate = sampleRate
	t.bufferSize = bufferSize
	factory := t.factory
	if factory == nil {
		factory = newTermuxSession
	}
	session, err := factory(sampleRate, bufferSize, t.fillSamples)
	if err != nil {
		debugPulseLog("PulseAudio session initialization failed: %v", err)
		return err
	}
	if session == nil || session.stream == nil {
		if session != nil {
			session.Close()
		}
		return fmt.Errorf("PulseAudio session initialization returned no stream")
	}

	t.session = session
	t.wantPlayback = false
	t.suspended = false
	t.closed = false
	t.wake = make(chan struct{}, 1)
	t.stop = make(chan struct{})
	t.supervisorDone = make(chan struct{})
	go t.runLifecycle(t.supervisorDone)
	return nil
}

func (t *termuxSpeaker) Play(s ...beep.Streamer) {
	t.mu.Lock()
	t.mixer.Add(s...)
	t.mu.Unlock()

	t.lifecycleMu.Lock()
	if !t.closed {
		t.wantPlayback = true
		t.signalWakeLocked()
	}
	t.lifecycleMu.Unlock()
}

func (t *termuxSpeaker) Clear() {
	t.mu.Lock()
	t.mixer.Clear()
	t.mu.Unlock()

	t.lifecycleMu.Lock()
	t.wantPlayback = false
	t.signalWakeLocked()
	t.lifecycleMu.Unlock()
}

func (t *termuxSpeaker) Lock()   { t.mu.Lock() }
func (t *termuxSpeaker) Unlock() { t.mu.Unlock() }

func (t *termuxSpeaker) Suspend() error {
	t.lifecycleMu.Lock()
	t.suspended = true
	if t.startupCancel != nil {
		t.startupCancel()
	}
	t.signalWakeLocked()
	t.lifecycleMu.Unlock()
	return nil
}

func (t *termuxSpeaker) Resume() error {
	t.lifecycleMu.Lock()
	t.suspended = false
	t.signalWakeLocked()
	t.lifecycleMu.Unlock()
	return nil
}

func (t *termuxSpeaker) Close() {
	t.lifecycleMu.Lock()
	done := t.supervisorDone
	if done == nil {
		t.closed = true
		t.lifecycleMu.Unlock()
		t.clearMixer()
		return
	}
	if !t.closed {
		t.closed = true
		close(t.stop)
		if t.startupCancel != nil {
			t.startupCancel()
		}
	}
	t.lifecycleMu.Unlock()
	<-done

	t.lifecycleMu.Lock()
	t.session = nil
	t.supervisorDone = nil
	t.startupCancel = nil
	t.wake = nil
	t.stop = nil
	t.wantPlayback = false
	t.suspended = false
	t.lifecycleMu.Unlock()
	t.clearMixer()
}

func (t *termuxSpeaker) clearMixer() {
	t.mu.Lock()
	t.mixer.Clear()
	t.mu.Unlock()
}

func (t *termuxSpeaker) signalWakeLocked() {
	if t.wake == nil {
		return
	}
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *termuxSpeaker) lifecycleState() (want, suspended, closed bool) {
	t.lifecycleMu.Lock()
	want = t.wantPlayback
	suspended = t.suspended
	closed = t.closed
	t.lifecycleMu.Unlock()
	return
}

func (t *termuxSpeaker) waitLifecycle() bool {
	t.lifecycleMu.Lock()
	wake := t.wake
	stop := t.stop
	closed := t.closed
	t.lifecycleMu.Unlock()
	if closed {
		return false
	}
	select {
	case <-wake:
		return true
	case <-stop:
		return false
	}
}

func (t *termuxSpeaker) waitRecovery(delay time.Duration) bool {
	t.lifecycleMu.Lock()
	wake := t.wake
	stop := t.stop
	closed := t.closed
	t.lifecycleMu.Unlock()
	if closed {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-wake:
		return true
	case <-stop:
		return false
	}
}

func (t *termuxSpeaker) createSession() (*termuxSession, error) {
	t.lifecycleMu.Lock()
	factory := t.factory
	sampleRate := t.sampleRate
	bufferSize := t.bufferSize
	t.lifecycleMu.Unlock()
	if factory == nil {
		factory = newTermuxSession
	}
	return factory(sampleRate, bufferSize, t.fillSamples)
}

func (t *termuxSpeaker) installSession(session *termuxSession) bool {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	if t.closed {
		return false
	}
	t.session = session
	return true
}

func (t *termuxSpeaker) forgetSession(session *termuxSession) {
	t.lifecycleMu.Lock()
	if t.session == session {
		t.session = nil
	}
	t.lifecycleMu.Unlock()
}

func (t *termuxSpeaker) setStartupCancel(cancel context.CancelFunc) {
	t.lifecycleMu.Lock()
	t.startupCancel = cancel
	t.lifecycleMu.Unlock()
}

func (t *termuxSpeaker) clearStartupCancel() {
	t.lifecycleMu.Lock()
	t.startupCancel = nil
	t.lifecycleMu.Unlock()
}

func (t *termuxSpeaker) applySessionState(session *termuxSession, started bool) bool {
	_, suspended, closed := t.lifecycleState()
	if closed {
		return false
	}
	if started && suspended {
		session.stream.Pause()
	}
	if started && !suspended {
		session.stream.Resume()
	}
	return true
}

func (t *termuxSpeaker) runLifecycle(done chan struct{}) {
	defer close(done)
	t.lifecycleMu.Lock()
	session := t.session
	t.lifecycleMu.Unlock()
	started := false
	startedAt := time.Time{}
	backoff := 100 * time.Millisecond

	for {
		if session == nil {
			want, suspended, closed := t.lifecycleState()
			if closed {
				return
			}
			if !want || suspended {
				if !t.waitLifecycle() {
					return
				}
				continue
			}

			created, err := t.createSession()
			if err != nil {
				debugPulseLog("PulseAudio session recovery failed: %v", err)
				if !t.waitRecovery(backoff) {
					return
				}
				backoff = min(backoff*2, time.Second)
				continue
			}
			if !t.installSession(created) {
				created.Close()
				return
			}
			session = created
			started = false
			startedAt = time.Time{}
		}

		if !started {
			want, suspended, closed := t.lifecycleState()
			if closed {
				session.Close()
				return
			}
			if !want || suspended {
				if !t.waitLifecycle() {
					session.Close()
					return
				}
				continue
			}

			ctx, cancel := context.WithCancel(context.Background())
			t.setStartupCancel(cancel)
			err := session.stream.StartContext(ctx)
			cancel()
			t.clearStartupCancel()
			if err != nil {
				session.Close()
				t.forgetSession(session)
				session = nil
				started = false
				startedAt = time.Time{}
				if _, _, closed := t.lifecycleState(); closed {
					return
				}
				if !t.waitRecovery(backoff) {
					return
				}
				backoff = min(backoff*2, time.Second)
				continue
			}
			started = true
			startedAt = nowFunc()
			if !t.applySessionState(session, started) {
				session.Close()
				return
			}
		}

		select {
		case <-session.stream.Done():
			if !startedAt.IsZero() && nowFunc().Sub(startedAt) >= termuxSessionStableTime {
				backoff = 100 * time.Millisecond
			}
			session.Close()
			t.forgetSession(session)
			session = nil
			started = false
			startedAt = time.Time{}
			if _, _, closed := t.lifecycleState(); closed {
				return
			}
			if !t.waitRecovery(backoff) {
				return
			}
			backoff = min(backoff*2, time.Second)
		case <-t.stop:
			session.Close()
			t.forgetSession(session)
			return
		case <-t.wake:
			if !t.applySessionState(session, started) {
				session.Close()
				t.forgetSession(session)
				return
			}
		}
	}
}

// fillSamples is invoked by PulseAudio's internal goroutine when the daemon
// needs more bytes. It pulls stereo frames out of beep.Mixer under our mutex
// and writes them as interleaved float32, matching the Float32Reader format
// registered in Init.
func (t *termuxSpeaker) fillSamples(buf []float32) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	frames := len(buf) / 2
	if cap(t.frameBuf) < frames {
		t.frameBuf = make([][2]float64, frames)
	}
	frameSlice := t.frameBuf[:frames]
	n, _ := t.mixer.Stream(frameSlice)
	for i := 0; i < n; i++ {
		l := frameSlice[i][0]
		r := frameSlice[i][1]
		if l > 1 {
			l = 1
		} else if l < -1 {
			l = -1
		}
		if r > 1 {
			r = 1
		} else if r < -1 {
			r = -1
		}
		buf[i*2+0] = float32(l)
		buf[i*2+1] = float32(r)
	}
	return n * 2, nil
}

// pulseServerOption returns the explicit server string to pass to
// pulse.ClientServerString, or "" when jfreymuth/pulse should use the
// user's PULSE_SERVER or its built-in defaultServerStrings fallback.
//
// Priority order:
//  1. PULSE_SERVER env var (any value) → "" (respect user setting).
//  2. Termux-friendly base dirs → "unix:<absolute path>".
//  3. Nothing → "" (let jfreymuth/pulse fail with a clear error).
func pulseServerOption() string {
	if v, ok := os.LookupEnv("PULSE_SERVER"); ok {
		debugPulseLog("PULSE_SERVER=%q set; deferring to user configuration", v)
		return ""
	}
	if addr := discoverPulseSocket(); addr != "" {
		debugPulseLog("discovered pulse socket: %q", addr)
		return "unix:" + addr
	}
	debugPulseLog("no pulse socket found")
	return ""
}

// Discovery budget. PulseAudio creates its runtime socket asynchronously,
// so cliamp may boot a few milliseconds before the daemon does. The retry
// wrapper polls with exponential backoff up to discoveryTotalDeadline. If
// the socket exists on the first attempt we return immediately — no fixed
// sleep is added.
const (
	discoveryTotalDeadline  = 500 * time.Millisecond
	discoveryInitialBackoff = 25 * time.Millisecond
	discoveryMaxBackoff     = 100 * time.Millisecond
	termuxSessionStableTime = time.Second
)

// nowFunc and sleepFunc are swapped in tests so retry behavior can be
// exercised without real waits.
var (
	nowFunc   = time.Now
	sleepFunc = time.Sleep
)

// discoverPulseSocket locates the PulseAudio daemon's Unix socket on Termux
// without invoking pactl, hardcoding the random runtime-dir suffix, or
// falling back to TCP. Returns the absolute socket path, or "" if not
// found within the discovery deadline.
//
// The retry is needed because PulseAudio creates its runtime dir and
// socket asynchronously after spawning. On a slow Termux boot we may
// read the directory before the socket appears; the wrapper waits up
// to discoveryTotalDeadline with exponential backoff.
//
// If the socket is still not visible, we try the libpulse "autospawn"
// fallback: invoke `pulseaudio --start` (exactly what libpulse's
// autospawn does) so the daemon creates the runtime dir + socket,
// then retry the discovery once more. We do not extend the per-phase
// deadline or chain additional retries; one spawn attempt is enough.
func discoverPulseSocket() string {
	debugPulseLog("PULSE_SERVER unset; bases=%v", pulseSocketBases())

	if addr := discoverPulseSocketWithProbe(discoverPulseSocketOnce); addr != "" {
		return addr
	}
	if !trySpawnPulseaudio() {
		return ""
	}
	return discoverPulseSocketWithProbe(discoverPulseSocketOnce)
}

// discoverPulseSocketWithProbe retries probe up to discoveryTotalDeadline
// with exponential backoff (capped). Returns the first non-empty result.
// Used directly by tests with a controllable probe.
func discoverPulseSocketWithProbe(probe func() string) string {
	deadline := nowFunc().Add(discoveryTotalDeadline)
	backoff := discoveryInitialBackoff
	for {
		if addr := probe(); addr != "" {
			return addr
		}
		if !nowFunc().Before(deadline) {
			return ""
		}
		// Cap each sleep to the remaining deadline so we never overshoot.
		remaining := deadline.Sub(nowFunc())
		if remaining <= 0 {
			return ""
		}
		sleepFor := backoff
		if sleepFor > remaining {
			sleepFor = remaining
		}
		sleepFunc(sleepFor)
		backoff *= 2
		if backoff > discoveryMaxBackoff {
			backoff = discoveryMaxBackoff
		}
	}
}

// discoverPulseSocketOnce makes a single attempt to find the socket,
// without any retry. Returns "" if no socket is currently visible.
//
// Strategy:
//  1. Try the canonical path pulse/native in each candidate base directory.
//  2. Fall back to scanning each base directory for any subdirectory whose
//     name starts with "pulse" and which contains a Unix socket named
//     "native". We use os.ReadDir + os.Stat directly because filepath.Glob
//     is unreliable on some Android/Termux filesystems.
func discoverPulseSocketOnce() string {
	for _, base := range pulseSocketBases() {
		if path := filepath.Join(base, "pulse", "native"); isUnixSocket(path) {
			return path
		}
	}
	for _, base := range pulseSocketBases() {
		if path := findPulseSocketInDir(base); path != "" {
			return path
		}
	}
	return ""
}

// findPulseSocketInDir scans base for a Unix socket at <subdir>/native
// where subdir's name starts with "pulse".
func findPulseSocketInDir(base string) string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "pulse") {
			continue
		}
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(base, name, "native")
		info, statErr := os.Stat(candidate)
		if statErr != nil {
			continue
		}
		if info.Mode()&os.ModeSocket != 0 {
			return candidate
		}
	}
	return ""
}

// pulseSocketBases returns the directories where the PulseAudio runtime
// socket might live, in priority order. De-duplicated.
func pulseSocketBases() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}

	// Standard freedesktop spec.
	add(os.Getenv("XDG_RUNTIME_DIR"))

	// POSIX fallback. Termux sets $TMPDIR but not $XDG_RUNTIME_DIR, so
	// pulseaudio uses $TMPDIR for its runtime dir.
	add(os.Getenv("TMPDIR"))

	// Termux-only fallback: $PREFIX/tmp. The PREFIX env var contains
	// "com.termux" on real Termux installs; we restrict the fallback to
	// that case so unrelated Linux installs aren't pointlessly probed.
	if prefix := os.Getenv("PREFIX"); strings.Contains(prefix, "com.termux") {
		add(filepath.Join(prefix, "tmp"))
	}

	return out
}

func isUnixSocket(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSocket != 0
}

// spawnPulseaudioFn is the autospawn trigger. Production calls
// trySpawnPulseaudioImpl; tests can swap it for a deterministic stub.
var spawnPulseaudioFn = trySpawnPulseaudioImpl

// pulseaudioSpawnTimeout bounds how long trySpawnPulseaudioImpl will wait
// for `pulseaudio --start` to return. Without this, a stalled daemon
// could block Player.New indefinitely. 5s is generous (a healthy
// daemon completes the spawn in well under a second).
const pulseaudioSpawnTimeout = 5 * time.Second

// trySpawnPulseaudio invokes `pulseaudio --start`, the same way libpulse's
// autospawn does (see pulseaudio/src/pulse/context.c context_autospawn).
// The parent returns 0 once the daemon has forked and bound its socket.
// We do NOT pass --exit-idle-time=-1 (libpulse doesn't either); the
// daemon persists according to PulseAudio's standard lifecycle rules.
func trySpawnPulseaudio() bool {
	return spawnPulseaudioFn()
}

func trySpawnPulseaudioImpl() bool {
	bin, err := exec.LookPath("pulseaudio")
	if err != nil {
		debugPulseLog("pulseaudio not in PATH: %v", err)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), pulseaudioSpawnTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--start")
	if err := cmd.Run(); err != nil {
		debugPulseLog("pulseaudio --start failed: %v", err)
		return false
	}
	debugPulseLog("pulseaudio --start succeeded")
	return true
}

func init() { backend = &termuxSpeaker{} }
