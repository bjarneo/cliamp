//go:build termux

package player

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
)

// makeSocket creates a real Unix domain socket at <dir>/<sub>/<name> and
// returns the directory path. The socket is closed on test cleanup.
func makeSocket(t *testing.T, sub, name string) string {
	t.Helper()
	dir := t.TempDir()
	subDir := filepath.Join(dir, sub)
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sockPath := filepath.Join(subDir, name)
	ln, err := net.ListenUnix("unix", mustAddr(t, sockPath))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return dir
}

// withEnv sets env vars for the duration of a test, restoring on cleanup.
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		old, had := os.LookupEnv(k)
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("setenv %s: %v", k, err)
		}
		if had {
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		} else {
			t.Cleanup(func() { _ = os.Unsetenv(k) })
		}
	}
}

// clearEnv unsets env vars for the duration of a test.
func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		_ = os.Unsetenv(k)
		if had {
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
	}
}

func mustAddr(t *testing.T, path string) *net.UnixAddr {
	t.Helper()
	a, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return a
}

// --- PulseAudio discovery ---

func TestDiscoverPulseSocket_CanonicalPath(t *testing.T) {
	dir := makeSocket(t, "pulse", "native")
	withEnv(t, map[string]string{"XDG_RUNTIME_DIR": dir, "TMPDIR": ""})

	got := discoverPulseSocket()
	want := filepath.Join(dir, "pulse", "native")
	if got != want {
		t.Errorf("discoverPulseSocket() = %q, want %q", got, want)
	}
}

func TestDiscoverPulseSocket_RandomizedSuffix(t *testing.T) {
	// PulseAudio's default runtime dir has a random hash suffix.
	dir := makeSocket(t, "pulse-AbCd1234", "native")
	withEnv(t, map[string]string{"XDG_RUNTIME_DIR": dir, "TMPDIR": ""})

	want := filepath.Join(dir, "pulse-AbCd1234", "native")
	if got := discoverPulseSocket(); got != want {
		t.Errorf("discoverPulseSocket() = %q, want %q (must match the random suffix without hardcoding)", got, want)
	}
}

func TestDiscoverPulseSocket_TMPDIRFallback(t *testing.T) {
	// Termux sets $TMPDIR but not $XDG_RUNTIME_DIR; pulseaudio creates
	// its runtime dir there.
	dir := makeSocket(t, "pulse-XyZ987", "native")
	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX")
	withEnv(t, map[string]string{"TMPDIR": dir})

	want := filepath.Join(dir, "pulse-XyZ987", "native")
	if got := discoverPulseSocket(); got != want {
		t.Errorf("discoverPulseSocket() = %q, want %q (TMPDIR fallback)", got, want)
	}
}

func TestDiscoverPulseSocket_TermuxPREFIXPath(t *testing.T) {
	// $PREFIX/tmp is the canonical Termux runtime location when XDG and
	// TMPDIR are absent; the PREFIX contains "com.termux" so the
	// Termux-specific fallback engages. We construct the prefix path under
	// t.TempDir() with a "com.termux" segment so pulseSocketBases'
	// strings.Contains check fires, without depending on a hardcoded /tmp
	// path that could collide with parallel runs or system state.
	prefixPath := filepath.Join(t.TempDir(), "com.termux-test", "files", "usr")
	tmpDir := filepath.Join(prefixPath, "tmp")
	sockPath := filepath.Join(tmpDir, "pulse-AbC", "native")
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ln, err := net.ListenUnix("unix", mustAddr(t, sockPath))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX")
	withEnv(t, map[string]string{"PREFIX": prefixPath})

	if got := discoverPulseSocket(); got != sockPath {
		t.Errorf("discoverPulseSocket() = %q, want %q (PREFIX/tmp fallback)", got, sockPath)
	}
}

func TestDiscoverPulseSocket_NoSocket(t *testing.T) {
	dir := t.TempDir()
	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX")
	withEnv(t, map[string]string{"XDG_RUNTIME_DIR": dir})

	// Stub the spawn so we don't accidentally exec a real pulseaudio
	// binary if one happens to be on the test host's PATH.
	withStubSpawn(t, func() bool { return false })
	useFakeClock(t)
	if got := discoverPulseSocket(); got != "" {
		t.Errorf("discoverPulseSocket() = %q, want empty", got)
	}
}

func TestDiscoverPulseSocket_IgnoresRegularFile(t *testing.T) {
	// A regular file named "native" must not be mistaken for the socket.
	dir := t.TempDir()
	pulseDir := filepath.Join(dir, "pulse")
	if err := os.MkdirAll(pulseDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pulseDir, "native"), []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	withEnv(t, map[string]string{"XDG_RUNTIME_DIR": dir, "TMPDIR": ""})

	withStubSpawn(t, func() bool { return false })
	useFakeClock(t)
	if got := discoverPulseSocket(); got != "" {
		t.Errorf("discoverPulseSocket() = %q, want empty (regular file should be ignored)", got)
	}
}

// --- PULSE_SERVER env var ---

func TestPulseServerOption_HonorsPULSESERVER(t *testing.T) {
	// PULSE_SERVER wins even when a local socket is reachable.
	dir := makeSocket(t, "pulse-AbCd", "native")
	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX", "PULSE_SERVER")
	withEnv(t, map[string]string{
		"XDG_RUNTIME_DIR": dir,
		"PULSE_SERVER":    "unix:/some/explicit/path",
	})

	if got := pulseServerOption(); got != "" {
		t.Errorf("pulseServerOption() = %q, want empty (PULSE_SERVER must win)", got)
	}
}

func TestPulseServerOption_HonorsEmptyPULSESERVER(t *testing.T) {
	// An empty PULSE_SERVER is still an explicit setting.
	dir := makeSocket(t, "pulse-AbCd", "native")
	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX", "PULSE_SERVER")
	withEnv(t, map[string]string{
		"XDG_RUNTIME_DIR": dir,
		"PULSE_SERVER":    "",
	})

	if got := pulseServerOption(); got != "" {
		t.Errorf("pulseServerOption() = %q, want empty", got)
	}
}

// --- Retry budget ---

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time        { return c.t }
func (c *fakeClock) sleep(d time.Duration) { c.t = c.t.Add(d) }

func useFakeClock(t *testing.T) *fakeClock {
	t.Helper()
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0).UTC()}
	nowFunc = clk.now
	sleepFunc = clk.sleep
	t.Cleanup(func() {
		nowFunc = time.Now
		sleepFunc = time.Sleep
	})
	return clk
}

func TestDiscoverPulseSocketWithProbe_NoRetryWhenFoundImmediately(t *testing.T) {
	var calls int
	probe := func() string {
		calls++
		return "/tmp/pulse-AbCd/native"
	}

	if got := discoverPulseSocketWithProbe(probe); got != "/tmp/pulse-AbCd/native" {
		t.Errorf("got %q, want \"/tmp/pulse-AbCd/native\"", got)
	}
	if calls != 1 {
		t.Errorf("probe calls = %d, want 1 (no retry when first attempt succeeds)", calls)
	}
}

func TestDiscoverPulseSocketWithProbe_RetriesUntilFound(t *testing.T) {
	clk := useFakeClock(t)

	var calls int
	probe := func() string {
		calls++
		if calls < 4 {
			return ""
		}
		return "/tmp/pulse-ZzZ/native"
	}

	if got := discoverPulseSocketWithProbe(probe); got != "/tmp/pulse-ZzZ/native" {
		t.Errorf("got %q, want \"/tmp/pulse-ZzZ/native\"", got)
	}
	if calls != 4 {
		t.Errorf("probe calls = %d, want 4", calls)
	}
	// The backoff schedule (capped): 25ms, 50ms, 100ms.
	// Total elapsed: discoveryInitialBackoff + 2*discoveryInitialBackoff + discoveryMaxBackoff.
	wantElapsed := discoveryInitialBackoff + 2*discoveryInitialBackoff + discoveryMaxBackoff
	if elapsed := clk.t.Sub(time.Unix(1_700_000_000, 0).UTC()); elapsed != wantElapsed {
		t.Errorf("elapsed = %v, want %v", elapsed, wantElapsed)
	}
}

func TestDiscoverPulseSocketWithProbe_EmptyAfterDeadline(t *testing.T) {
	useFakeClock(t)

	probe := func() string { return "" }
	if got := discoverPulseSocketWithProbe(probe); got != "" {
		t.Errorf("got %q, want empty after deadline", got)
	}
}

// --- Autospawn ---

func withStubSpawn(t *testing.T, fn func() bool) {
	t.Helper()
	saved := spawnPulseaudioFn
	spawnPulseaudioFn = fn
	t.Cleanup(func() { spawnPulseaudioFn = saved })
}

func TestDiscoverPulseSocket_DoesNotSpawnWhenSocketExists(t *testing.T) {
	dir := makeSocket(t, "pulse-AbCd", "native")
	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX", "PULSE_SERVER")
	withEnv(t, map[string]string{"TMPDIR": dir})

	spawnCalls := 0
	withStubSpawn(t, func() bool {
		spawnCalls++
		return true
	})
	useFakeClock(t)

	if got := discoverPulseSocket(); got == "" {
		t.Errorf("discoverPulseSocket() = empty, want socket")
	}
	if spawnCalls != 0 {
		t.Errorf("spawn called %d times, want 0 (daemon already running)", spawnCalls)
	}
}

func TestDiscoverPulseSocket_SpawnsOnceWhenFirstPhaseFails(t *testing.T) {
	// No socket initially; spawn is invoked and succeeds (but doesn't
	// create a socket, to keep the test hermetic). discoverPulseSocket
	// returns "" and spawn is called exactly once.
	dir := t.TempDir()
	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX", "PULSE_SERVER")
	withEnv(t, map[string]string{"TMPDIR": dir})

	spawnCalls := 0
	withStubSpawn(t, func() bool {
		spawnCalls++
		return true
	})
	useFakeClock(t)

	if got := discoverPulseSocket(); got != "" {
		t.Errorf("got %q, want empty (no socket after spawn)", got)
	}
	if spawnCalls != 1 {
		t.Errorf("spawn called %d times, want exactly 1", spawnCalls)
	}
}

func TestDiscoverPulseSocket_SpawnThenSucceeds(t *testing.T) {
	// No socket initially. The stub for spawnPulseaudioFn creates the
	// socket (mirroring what the real pulseaudio --start does). The
	// second discovery phase finds it.
	dir := t.TempDir()
	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX", "PULSE_SERVER")
	withEnv(t, map[string]string{"TMPDIR": dir})

	withStubSpawn(t, func() bool {
		sub := filepath.Join(dir, "pulse-VVVV")
		if err := os.MkdirAll(sub, 0o700); err != nil {
			return false
		}
		sockPath := filepath.Join(sub, "native")
		ln, err := net.ListenUnix("unix", mustAddr(t, sockPath))
		if err != nil {
			return false
		}
		t.Cleanup(func() { _ = ln.Close() })
		return true
	})
	useFakeClock(t)

	want := filepath.Join(dir, "pulse-VVVV", "native")
	if got := discoverPulseSocket(); got != want {
		t.Errorf("got %q, want %q (spawn must enable discovery on second phase)", got, want)
	}
}

func TestDiscoverPulseSocket_FailedSpawnReturnsEmpty(t *testing.T) {
	// Spawn fails (binary missing, exit code != 0, etc). There is no
	// point in retrying discovery.
	dir := t.TempDir()
	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX", "PULSE_SERVER")
	withEnv(t, map[string]string{"TMPDIR": dir})

	spawnCalls := 0
	withStubSpawn(t, func() bool {
		spawnCalls++
		return false
	})
	useFakeClock(t)

	if got := discoverPulseSocket(); got != "" {
		t.Errorf("got %q, want empty (spawn failed)", got)
	}
	if spawnCalls != 1 {
		t.Errorf("spawn called %d times, want exactly 1", spawnCalls)
	}
}

func TestTrySpawnPulseaudioImpl_NotInPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if got := trySpawnPulseaudioImpl(); got {
		t.Errorf("trySpawnPulseaudioImpl() = true, want false (pulseaudio not in PATH)")
	}
}

// --- Integration with jfreymuth/pulse ---

func TestTermuxSpeakerInit_DoesNotReturnNoValidServer(t *testing.T) {
	// Regression guard: with a real Unix socket present and PULSE_SERVER
	// unset, termuxSpeaker.Init must reach the protocol stage. The fake
	// socket only accepts; it does not speak PulseAudio, so we expect
	// an error — just not the "no valid server" error that would mean
	// our server string never reached proto.Connect.
	dir := makeSocket(t, "pulse-AbCd", "native")
	clearEnv(t, "XDG_RUNTIME_DIR", "TMPDIR", "PREFIX", "PULSE_SERVER")
	withEnv(t, map[string]string{"TMPDIR": dir})

	err := (&termuxSpeaker{}).Init(44100, 4096)
	if err == nil {
		t.Fatal("expected error from fake socket (no real PulseAudio protocol)")
	}
	if msg := err.Error(); strings.Contains(msg, "no valid server") {
		t.Fatalf("Init returned %q: our server-string selection must have failed", msg)
	}
}

// --- Lifecycle invariants ---

// runClearWithTimeout invokes Clear in a goroutine and waits up to the
// timeout for it to return.
func runClearWithTimeout(t *testing.T, sp *termuxSpeaker) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		sp.Clear()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Clear hung beyond reasonable timeout")
	}
}

type fakeTermuxPlayback struct {
	startEntered  chan struct{}
	startRelease  chan struct{}
	startReturned chan struct{}
	done          chan struct{}

	startOnce        sync.Once
	startReleaseOnce sync.Once
	startReturnOnce  sync.Once
	doneOnce         sync.Once
	startCalls       atomic.Int32
	closeCalls       atomic.Int32
}

func newFakeTermuxPlayback() *fakeTermuxPlayback {
	return &fakeTermuxPlayback{
		startEntered:  make(chan struct{}),
		startRelease:  make(chan struct{}),
		startReturned: make(chan struct{}),
		done:          make(chan struct{}),
	}
}

func (f *fakeTermuxPlayback) StartContext(ctx context.Context) error {
	f.startCalls.Add(1)
	f.startOnce.Do(func() { close(f.startEntered) })
	var err error
	select {
	case <-f.startRelease:
	case <-f.done:
		err = errTermuxPulseConnectionClosed
	case <-ctx.Done():
		err = ctx.Err()
	}
	f.startReturnOnce.Do(func() { close(f.startReturned) })
	return err
}

func (f *fakeTermuxPlayback) Done() <-chan struct{} { return f.done }

func (f *fakeTermuxPlayback) Pause()  {}
func (f *fakeTermuxPlayback) Resume() {}

func (f *fakeTermuxPlayback) Close() {
	f.closeCalls.Add(1)
	f.disconnect()
}

func (f *fakeTermuxPlayback) releaseStart() {
	f.startReleaseOnce.Do(func() { close(f.startRelease) })
}

func (f *fakeTermuxPlayback) disconnect() {
	f.doneOnce.Do(func() { close(f.done) })
}

type fakeTermuxSessionFactory struct {
	mu       sync.Mutex
	sessions []*fakeTermuxPlayback
	created  chan *fakeTermuxPlayback
}

func newFakeTermuxSessionFactory() *fakeTermuxSessionFactory {
	return &fakeTermuxSessionFactory{created: make(chan *fakeTermuxPlayback, 8)}
}

func (f *fakeTermuxSessionFactory) newSession(beep.SampleRate, int, func([]float32) (int, error)) (*termuxSession, error) {
	stream := newFakeTermuxPlayback()
	f.mu.Lock()
	f.sessions = append(f.sessions, stream)
	f.mu.Unlock()
	f.created <- stream
	return &termuxSession{stream: stream}, nil
}

func (f *fakeTermuxSessionFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sessions)
}

func waitForFakeSession(t *testing.T, created <-chan *fakeTermuxPlayback) *fakeTermuxPlayback {
	t.Helper()
	select {
	case stream := <-created:
		return stream
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fake PulseAudio session")
		return nil
	}
}

func newFakeTermuxSpeaker(t *testing.T) (*termuxSpeaker, *fakeTermuxSessionFactory, *fakeTermuxPlayback) {
	t.Helper()
	factory := newFakeTermuxSessionFactory()
	sp := &termuxSpeaker{factory: factory.newSession}
	if err := sp.Init(44100, 4096); err != nil {
		t.Fatalf("Init: %v", err)
	}
	first := waitForFakeSession(t, factory.created)
	t.Cleanup(sp.Close)
	return sp, factory, first
}

// TestClear_DoesNotTouchStream verifies that Clear only empties the mixer and
// leaves the current session owned by the lifecycle supervisor.
func TestClear_DoesNotTouchStream(t *testing.T) {
	sp, _, first := newFakeTermuxSpeaker(t)
	runClearWithTimeout(t, sp)

	sp.lifecycleMu.Lock()
	current := sp.session
	sp.lifecycleMu.Unlock()
	if current == nil || current.stream != first {
		t.Errorf("Clear replaced the current PulseAudio session")
	}
}

// TestPlay_ClearThenPlay_UsesOneSession guards the Play -> Clear -> Play race
// while startup is pending. Clear and Play must not create a second session
// while the supervisor owns the first StartContext call.
func TestPlay_ClearThenPlay_UsesOneSession(t *testing.T) {
	sp, factory, first := newFakeTermuxSpeaker(t)

	sp.Play()
	select {
	case <-first.startEntered:
	case <-time.After(time.Second):
		t.Fatal("startup did not begin")
	}
	sp.Clear()
	sp.Play()

	if got := factory.count(); got != 1 {
		t.Fatalf("session count after Play -> Clear -> Play = %d, want 1", got)
	}
	if got := first.startCalls.Load(); got != 1 {
		t.Fatalf("start calls after Play → Clear → Play = %d, want 1", got)
	}
	first.releaseStart()
}

// TestPlay_NoGoroutineWhenStreamNil verifies that Play is safe to call
// before Init (or after an Init failure): the snapshot is nil, so no
// goroutine is spawned and no panic occurs.
func TestPlay_NoGoroutineWhenStreamNil(t *testing.T) {
	sp := &termuxSpeaker{} // no Init; client and stream are nil

	sp.Play()

	sp.lifecycleMu.Lock()
	session := sp.session
	sp.lifecycleMu.Unlock()
	if session != nil {
		t.Errorf("session must remain nil when Play is called before Init")
	}
}

func TestTermuxSpeaker_ReconnectsBeforeStarted(t *testing.T) {
	sp, factory, first := newFakeTermuxSpeaker(t)

	sp.Play()
	select {
	case <-first.startEntered:
	case <-time.After(time.Second):
		t.Fatal("startup did not begin")
	}
	first.disconnect()

	second := waitForFakeSession(t, factory.created)
	select {
	case <-second.startEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement startup did not begin after pre-start disconnect")
	}
	if got := first.closeCalls.Load(); got == 0 {
		t.Fatal("lost pre-start session was not closed")
	}
	second.releaseStart()
}

func TestTermuxSpeaker_ReconnectsAfterStarted(t *testing.T) {
	sp, factory, first := newFakeTermuxSpeaker(t)

	sp.Play()
	select {
	case <-first.startEntered:
	case <-time.After(time.Second):
		t.Fatal("startup did not begin")
	}
	first.releaseStart()
	select {
	case <-first.startReturned:
	case <-time.After(time.Second):
		t.Fatal("startup did not return")
	}
	first.disconnect()

	second := waitForFakeSession(t, factory.created)
	select {
	case <-second.startEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement startup did not begin after post-start disconnect")
	}
	if got := first.closeCalls.Load(); got == 0 {
		t.Fatal("lost post-start session was not closed")
	}
	second.releaseStart()
}

func TestTermuxSpeaker_CloseCancelsPendingStart(t *testing.T) {
	sp, _, first := newFakeTermuxSpeaker(t)
	sp.Play()
	select {
	case <-first.startEntered:
	case <-time.After(time.Second):
		t.Fatal("startup did not begin")
	}

	done := make(chan struct{})
	go func() {
		sp.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel pending startup")
	}
}

func TestTermuxSpeaker_CloseAllowsReinitialization(t *testing.T) {
	sp, factory, first := newFakeTermuxSpeaker(t)

	sp.Play()
	select {
	case <-first.startEntered:
	case <-time.After(time.Second):
		t.Fatal("initial startup did not begin")
	}
	first.releaseStart()
	select {
	case <-first.startReturned:
	case <-time.After(time.Second):
		t.Fatal("initial startup did not return")
	}
	sp.Close()

	if err := sp.Init(44100, 4096); err != nil {
		t.Fatalf("reinitialize after Close: %v", err)
	}
	second := waitForFakeSession(t, factory.created)
	sp.Play()
	select {
	case <-second.startEntered:
	case <-time.After(time.Second):
		t.Fatal("replacement startup did not begin")
	}
	if got := factory.count(); got != 2 {
		t.Fatalf("session count after Close -> Init = %d, want 2", got)
	}
	second.releaseStart()
}

func TestTermuxPulseClient_HasActivePlayback(t *testing.T) {
	stream := &termuxPulsePlayback{state: termuxPulseIdle}
	client := &termuxPulseClient{
		playback: map[uint32]*termuxPulsePlayback{1: stream},
	}

	if client.hasActivePlayback() {
		t.Fatal("idle playback must not trigger health checks")
	}
	stream.stateMu.Lock()
	stream.state = termuxPulseRunning
	stream.stateMu.Unlock()
	if !client.hasActivePlayback() {
		t.Fatal("running playback must trigger health checks")
	}
	stream.stateMu.Lock()
	stream.state = termuxPulsePaused
	stream.stateMu.Unlock()
	if client.hasActivePlayback() {
		t.Fatal("paused playback must not trigger health checks")
	}
}

func TestTermuxPulsePlayback_StartedAfterUnderflow(t *testing.T) {
	stream := &termuxPulsePlayback{
		started:   make(chan struct{}, 1),
		done:      make(chan struct{}),
		state:     termuxPulseRunning,
		underflow: true,
	}

	stream.notifyStarted()
	select {
	case <-stream.started:
	default:
		t.Fatal("Started notification must not be suppressed by an earlier underflow")
	}
}

// --- Retry deadline ---

// TestDiscoverPulseSocketWithProbe_RespectsDeadline verifies the retry
// budget: even with a probe that always fails, total elapsed time stays
// within discoveryTotalDeadline plus a small slack. Each sleep is
// capped to the remaining deadline so we never overshoot.
func TestDiscoverPulseSocketWithProbe_RespectsDeadline(t *testing.T) {
	useFakeClock(t)

	const slack = time.Millisecond
	probe := func() string { return "" }
	start := nowFunc()
	_ = discoverPulseSocketWithProbe(probe)
	elapsed := nowFunc().Sub(start)

	if elapsed > discoveryTotalDeadline+slack {
		t.Errorf("elapsed = %v, want <= %v (deadline + %v slack)", elapsed, discoveryTotalDeadline, slack)
	}
}
