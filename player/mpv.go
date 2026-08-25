package player

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/applog"
)

const (
	defaultMPVRequestTimeout = 3 * time.Second
	mpvConnectTimeout        = 5 * time.Second
)

var errMPVClosed = errors.New("MPV backend is closed")

// MPVOptions configures the persistent MPV process.
type MPVOptions struct {
	Executable       string
	AudioDevice      string
	AudioReservation string
	BitPerfect       bool
	RequestTimeout   time.Duration
}

type reservationHandle interface {
	Close() error
}

// MPVBackend implements Engine with one persistent MPV process controlled over
// JSON IPC. cliamp remains responsible for playlist sequencing.
type MPVBackend struct {
	stateMu sync.RWMutex

	playing        bool
	paused         bool
	drained        bool
	seekable       bool
	hasPreload     bool
	position       time.Duration
	duration       time.Duration
	volumeDB       float64
	volumeMinDB    float64
	speed          float64
	path           string
	mediaTitle     string
	device         string
	currentAO      string
	sourceParams   AudioParameters
	outputParams   AudioParameters
	streamErr      error
	closing        bool
	bitPerfect     bool
	requestTimeout time.Duration

	resolverMu      sync.RWMutex
	sourceResolvers map[string]SourceResolver

	ipcMu sync.RWMutex
	ipc   *mpvIPC

	cmd         *exec.Cmd
	processDone chan struct{}
	stderr      *lockedBuffer
	runtimeDir  string
	socketPath  string
	reservation reservationHandle
	closeOnce   sync.Once
}

// NewMPVBackend starts MPV, connects its JSON IPC socket, and registers
// property observations used by cliamp's UI and daemon.
func NewMPVBackend(options MPVOptions) (*MPVBackend, error) {
	executable := strings.TrimSpace(options.Executable)
	if executable == "" {
		executable = "mpv"
	}
	path, err := exec.LookPath(executable)
	if err != nil {
		return nil, fmt.Errorf("MPV backend requested, but %q was not found in PATH; install mpv or use --audio-backend native", executable)
	}

	runtimeDir, socketPath, err := newMPVRuntimeSocket()
	if err != nil {
		return nil, err
	}
	cleanupRuntime := func() { _ = os.RemoveAll(runtimeDir) }

	reservation, err := acquireAudioReservation(options.AudioReservation)
	if err != nil {
		cleanupRuntime()
		return nil, err
	}

	timeout := options.RequestTimeout
	if timeout <= 0 {
		timeout = defaultMPVRequestTimeout
	}
	stderr := &lockedBuffer{}
	args := mpvCommandArgs(options, socketPath)
	cmd := exec.Command(path, args...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	applog.Debug("MPV command: %q", append([]string{path}, args...))
	if err := cmd.Start(); err != nil {
		if reservation != nil {
			_ = reservation.Close()
		}
		cleanupRuntime()
		return nil, fmt.Errorf("start MPV: %w", err)
	}

	b := &MPVBackend{
		volumeMinDB:     -50,
		speed:           1,
		device:          options.AudioDevice,
		bitPerfect:      options.BitPerfect,
		requestTimeout:  timeout,
		sourceResolvers: make(map[string]SourceResolver),
		cmd:             cmd,
		processDone:     make(chan struct{}),
		stderr:          stderr,
		runtimeDir:      runtimeDir,
		socketPath:      socketPath,
		reservation:     reservation,
	}
	go b.waitProcess()

	conn, err := b.connectIPC(mpvConnectTimeout)
	if err != nil {
		b.Close()
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return nil, fmt.Errorf("connect MPV IPC socket %s: %w: %s", socketPath, err, detail)
		}
		return nil, fmt.Errorf("connect MPV IPC socket %s: %w", socketPath, err)
	}
	ipc := newMPVIPC(conn, timeout, b.handleIPCMessage)
	b.ipcMu.Lock()
	b.ipc = ipc
	b.ipcMu.Unlock()
	applog.Info("MPV IPC connected: %s", socketPath)

	if err := b.observeProperties(); err != nil {
		b.Close()
		return nil, fmt.Errorf("initialize MPV IPC: %w", err)
	}
	return b, nil
}

func mpvCommandArgs(options MPVOptions, socketPath string) []string {
	volumeMax := "200"
	if options.BitPerfect {
		volumeMax = "100"
	}
	args := []string{
		"--idle=yes",
		"--no-video",
		"--audio-display=no",
		"--no-terminal",
		"--no-config",
		"--audio-fallback-to-null=no",
		"--gapless-audio=no",
		"--input-ipc-server=" + socketPath,
		"--volume-max=" + volumeMax,
	}
	if options.AudioDevice != "" {
		args = append(args, "--audio-device="+options.AudioDevice)
	}
	if options.BitPerfect {
		args = append(args,
			"--volume=100",
			"--speed=1.0",
			"--af=",
			"--replaygain=no",
			"--audio-pitch-correction=no",
			"--audio-normalize-downmix=no",
		)
	}
	return args
}

func newMPVRuntimeSocket() (runtimeDir, socketPath string, err error) {
	base := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR"))
	if base != "" {
		base = filepath.Join(base, "cliamp")
		if err := os.MkdirAll(base, 0o700); err != nil {
			return "", "", fmt.Errorf("create MPV runtime directory: %w", err)
		}
		runtimeDir, err = os.MkdirTemp(base, "mpv-")
	} else {
		runtimeDir, err = os.MkdirTemp("", "cliamp-mpv-")
	}
	if err != nil {
		return "", "", fmt.Errorf("create MPV runtime directory: %w", err)
	}
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		_ = os.RemoveAll(runtimeDir)
		return "", "", fmt.Errorf("secure MPV runtime directory: %w", err)
	}
	return runtimeDir, filepath.Join(runtimeDir, "ipc.sock"), nil
}

func (b *MPVBackend) connectIPC(timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", b.socketPath, 100*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case <-b.processDone:
			if detail := strings.TrimSpace(b.stderr.String()); detail != "" {
				return nil, fmt.Errorf("MPV exited before creating IPC socket: %s", detail)
			}
			return nil, errors.New("MPV exited before creating IPC socket")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("timeout")
	}
	return nil, lastErr
}

func (b *MPVBackend) waitProcess() {
	err := b.cmd.Wait()
	b.stateMu.Lock()
	closing := b.closing
	b.stateMu.Unlock()
	if !closing {
		b.recordUnexpectedExit(err, strings.TrimSpace(b.stderr.String()))
	}
	close(b.processDone)

	b.ipcMu.RLock()
	ipc := b.ipc
	b.ipcMu.RUnlock()
	if ipc != nil {
		if err == nil {
			err = errors.New("MPV process exited")
		}
		ipc.fail(err)
	}
	if !closing {
		applog.Error("MPV process exited unexpectedly: %v", err)
	}
}

func (b *MPVBackend) recordUnexpectedExit(err error, detail string) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	switch {
	case err != nil && detail != "":
		b.streamErr = fmt.Errorf("MPV exited unexpectedly: %w: %s", err, detail)
	case err != nil:
		b.streamErr = fmt.Errorf("MPV exited unexpectedly: %w", err)
	default:
		b.streamErr = errors.New("MPV exited unexpectedly")
	}
	b.playing = false
	b.paused = false
}

func (b *MPVBackend) observeProperties() error {
	properties := []string{
		"time-pos", "duration", "pause", "volume", "speed", "audio-params",
		"audio-out-params", "path", "media-title", "seekable", "audio-device", "current-ao",
	}
	for id, name := range properties {
		if _, err := b.command([]any{"observe_property", id + 1, name}); err != nil {
			return fmt.Errorf("observe %s: %w", name, err)
		}
	}
	return nil
}

func (b *MPVBackend) command(command []any) (json.RawMessage, error) {
	b.ipcMu.RLock()
	ipc := b.ipc
	b.ipcMu.RUnlock()
	if ipc == nil {
		return nil, errMPVClosed
	}
	return ipc.command(command)
}

func (b *MPVBackend) handleIPCMessage(message mpvMessage) {
	switch message.Event {
	case "property-change":
		b.handleProperty(message.Name, message.Data)
	case "file-loaded":
		b.stateMu.Lock()
		b.playing = true
		b.drained = false
		b.streamErr = nil
		b.stateMu.Unlock()
		applog.Info("MPV event: file-loaded")
	case "end-file":
		b.stateMu.Lock()
		if message.Reason == "eof" {
			b.drained = true
			if b.duration > 0 {
				b.position = b.duration
			}
		} else if message.Reason == "error" {
			if message.Error != "" {
				b.streamErr = fmt.Errorf("MPV playback error: %s", message.Error)
			} else {
				b.streamErr = errors.New("MPV playback error")
			}
		}
		b.stateMu.Unlock()
		applog.Info("MPV event: end-file reason=%s error=%s", message.Reason, message.Error)
	case "playback-restart":
		applog.Debug("MPV event: playback-restart")
	case "shutdown":
		applog.Info("MPV event: shutdown")
	}
}

func (b *MPVBackend) handleProperty(name string, raw json.RawMessage) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()

	switch name {
	case "time-pos":
		if value, ok := jsonFloat(raw); ok {
			b.position = secondsDuration(value)
		}
	case "duration":
		if value, ok := jsonFloat(raw); ok {
			b.duration = secondsDuration(value)
		}
	case "pause":
		_ = json.Unmarshal(raw, &b.paused)
	case "volume":
		if value, ok := jsonFloat(raw); ok {
			b.volumeDB = mpvPercentToDB(value, b.volumeMinDB)
		}
	case "speed":
		if value, ok := jsonFloat(raw); ok {
			b.speed = value
		}
	case "path":
		b.path = jsonString(raw)
	case "media-title":
		b.mediaTitle = jsonString(raw)
	case "seekable":
		_ = json.Unmarshal(raw, &b.seekable)
	case "audio-device":
		b.device = jsonString(raw)
	case "current-ao":
		b.currentAO = jsonString(raw)
	case "audio-params":
		b.sourceParams = decodeAudioParameters(raw)
		applog.Debug("MPV audio-params: format=%s rate=%d channels=%s", b.sourceParams.Format, b.sourceParams.SampleRate, b.sourceParams.Channels)
	case "audio-out-params":
		b.outputParams = decodeAudioParameters(raw)
		applog.Debug("MPV audio-out-params: format=%s rate=%d channels=%s", b.outputParams.Format, b.outputParams.SampleRate, b.outputParams.Channels)
	}
}

func jsonString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func jsonFloat(raw json.RawMessage) (float64, bool) {
	var value float64
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	return value, true
}

func secondsDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func decodeAudioParameters(raw json.RawMessage) AudioParameters {
	var value struct {
		Format     string `json:"format"`
		SampleRate int    `json:"samplerate"`
		Channels   string `json:"hr-channels"`
		Fallback   string `json:"channels"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return AudioParameters{}
	}
	if value.Channels == "" {
		value.Channels = value.Fallback
	}
	return AudioParameters{Format: value.Format, SampleRate: value.SampleRate, Channels: value.Channels}
}

func (b *MPVBackend) resolvePath(path string) (string, error) {
	b.resolverMu.RLock()
	var resolver SourceResolver
	for prefix, candidate := range b.sourceResolvers {
		if strings.HasPrefix(path, prefix) {
			resolver = candidate
			break
		}
	}
	b.resolverMu.RUnlock()
	if resolver == nil {
		return path, nil
	}
	source, err := resolver(path)
	if err != nil {
		return "", fmt.Errorf("resolve source: %w", err)
	}
	if len(source.Segments) > 0 {
		return "", errors.New("segmented provider streams are unsupported with MPV backend; use --audio-backend native")
	}
	if source.URL == "" {
		return "", fmt.Errorf("resolve source: empty result for %s", path)
	}
	return source.URL, nil
}

// Play loads path into the persistent MPV process.
func (b *MPVBackend) Play(path string, knownDuration time.Duration) error {
	if strings.HasPrefix(path, "spotify:") {
		return errors.New("Spotify playback is unsupported with MPV backend; use --audio-backend native")
	}
	resolved, err := b.resolvePath(path)
	if err != nil {
		return err
	}
	b.stateMu.Lock()
	b.path = path
	b.mediaTitle = ""
	b.position = 0
	b.duration = knownDuration
	b.playing = true
	b.paused = false
	b.drained = false
	b.seekable = false
	b.hasPreload = false
	b.streamErr = nil
	b.sourceParams = AudioParameters{}
	b.outputParams = AudioParameters{}
	b.stateMu.Unlock()
	if _, err := b.command([]any{"loadfile", resolved, "replace"}); err != nil {
		b.recordPlaybackError(err)
		return fmt.Errorf("MPV loadfile: %w", err)
	}
	if _, err := b.command([]any{"set_property", "pause", false}); err != nil {
		b.recordPlaybackError(err)
		return fmt.Errorf("MPV resume after load: %w", err)
	}
	return nil
}

func (b *MPVBackend) recordPlaybackError(err error) {
	b.stateMu.Lock()
	b.streamErr = err
	b.playing = false
	b.paused = false
	b.stateMu.Unlock()
}

// PlayYTDL lets MPV's built-in yt-dlp hook resolve the page URL.
func (b *MPVBackend) PlayYTDL(pageURL string, knownDuration time.Duration) error {
	return b.Play(pageURL, knownDuration)
}

// ponytail: preload is marker-only until MPV can prebuffer without taking
// playlist ownership; EOF still returns to cliamp for sequencing.
func (b *MPVBackend) Preload(_ string, _ time.Duration) error {
	b.stateMu.Lock()
	b.hasPreload = true
	b.stateMu.Unlock()
	return nil
}

func (b *MPVBackend) PreloadYTDL(path string, knownDuration time.Duration) error {
	return b.Preload(path, knownDuration)
}

func (b *MPVBackend) ClearPreload() {
	b.stateMu.Lock()
	b.hasPreload = false
	b.stateMu.Unlock()
}

func (b *MPVBackend) Stop() {
	if _, err := b.command([]any{"stop"}); err != nil && !errors.Is(err, errMPVClosed) {
		applog.UserError("MPV stop failed: %v", err)
	}
	b.stateMu.Lock()
	b.playing = false
	b.paused = false
	b.drained = false
	b.seekable = false
	b.hasPreload = false
	b.position = 0
	b.duration = 0
	b.path = ""
	b.mediaTitle = ""
	b.sourceParams = AudioParameters{}
	b.outputParams = AudioParameters{}
	b.stateMu.Unlock()
}

func (b *MPVBackend) TogglePause() {
	b.stateMu.RLock()
	paused := b.paused
	b.stateMu.RUnlock()
	if _, err := b.command([]any{"set_property", "pause", !paused}); err != nil {
		applog.UserError("MPV pause failed: %v", err)
		return
	}
	b.stateMu.Lock()
	b.paused = !paused
	b.stateMu.Unlock()
}

func (b *MPVBackend) Seek(offset time.Duration) error {
	_, err := b.command([]any{"seek", offset.Seconds(), "relative"})
	if err != nil {
		return fmt.Errorf("MPV seek: %w", err)
	}
	b.stateMu.Lock()
	b.position = max(0, b.position+offset)
	b.drained = false
	b.stateMu.Unlock()
	return nil
}

func (b *MPVBackend) SeekYTDL(offset time.Duration) error { return b.Seek(offset) }
func (b *MPVBackend) CancelSeekYTDL()                     {}
func (b *MPVBackend) IsStreamSeek() bool                  { return false }
func (b *MPVBackend) IsYTDLSeek() bool                    { return false }
func (b *MPVBackend) GaplessAdvanced() bool               { return false }

func (b *MPVBackend) Position() time.Duration {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.position
}

func (b *MPVBackend) Duration() time.Duration {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.duration
}

func (b *MPVBackend) PositionAndDuration() (time.Duration, time.Duration) {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.position, b.duration
}

func (b *MPVBackend) SetVolumeMin(db float64) {
	b.stateMu.Lock()
	b.volumeMinDB = max(min(db, 0), -90)
	if b.volumeDB < b.volumeMinDB {
		b.volumeDB = b.volumeMinDB
	}
	b.stateMu.Unlock()
}

func (b *MPVBackend) VolumeMin() float64 {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.volumeMinDB
}

func (b *MPVBackend) SetVolume(db float64) {
	b.stateMu.RLock()
	bitPerfect := b.bitPerfect
	minDB := b.volumeMinDB
	b.stateMu.RUnlock()
	if bitPerfect {
		if db != 0 {
			applog.UserWarn("MPV bit-perfect mode: volume is locked at 0 dB")
		}
		return
	}
	db = max(min(db, 6), minDB)
	if _, err := b.command([]any{"set_property", "volume", dbToMPVPercent(db)}); err != nil {
		applog.UserError("MPV volume failed: %v", err)
		return
	}
	b.stateMu.Lock()
	b.volumeDB = db
	b.stateMu.Unlock()
}

func dbToMPVPercent(db float64) float64 { return 100 * math.Pow(10, db/20) }

func mpvPercentToDB(percent, floor float64) float64 {
	if percent <= 0 {
		return floor
	}
	return max(20*math.Log10(percent/100), floor)
}

func (b *MPVBackend) Volume() float64 {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.volumeDB
}

func (b *MPVBackend) SetSpeed(ratio float64) {
	b.stateMu.RLock()
	bitPerfect := b.bitPerfect
	b.stateMu.RUnlock()
	if bitPerfect {
		if ratio != 1 {
			applog.UserWarn("MPV bit-perfect mode: speed is locked at 1.0x")
		}
		return
	}
	ratio = max(min(ratio, 2), 0.25)
	if _, err := b.command([]any{"set_property", "speed", ratio}); err != nil {
		applog.UserError("MPV speed failed: %v", err)
		return
	}
	b.stateMu.Lock()
	b.speed = ratio
	b.stateMu.Unlock()
}

func (b *MPVBackend) Speed() float64 {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.speed
}

func (b *MPVBackend) ToggleMono() {
	applog.UserWarn("Mono is unsupported with MPV backend")
}

func (b *MPVBackend) Mono() bool { return false }

func (b *MPVBackend) SetEQBand(_ int, db float64) {
	if db != 0 {
		applog.UserWarn("EQ is unsupported with MPV backend")
	}
}

func (b *MPVBackend) EQBands() [10]float64 { return [10]float64{} }

func (b *MPVBackend) IsPlaying() bool {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.playing
}

func (b *MPVBackend) IsPaused() bool {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.paused
}

func (b *MPVBackend) Drained() bool {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.drained
}

func (b *MPVBackend) HasPreload() bool {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.hasPreload
}

func (b *MPVBackend) Seekable() bool {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.seekable
}

func (b *MPVBackend) IsLiveStream() bool {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.playing && b.duration <= 0 && (strings.HasPrefix(b.path, "http://") || strings.HasPrefix(b.path, "https://"))
}

func (b *MPVBackend) StreamErr() error {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.streamErr
}

func (b *MPVBackend) StreamTitle() string {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.mediaTitle
}

func (b *MPVBackend) StreamBytes() (downloaded, total int64) { return 0, 0 }
func (b *MPVBackend) SamplesInto(_ []float64) int            { return 0 }
func (b *MPVBackend) WaveformSamplesInto(_ []float64) int    { return 0 }
func (b *MPVBackend) StereoSamplesInto(_ [][2]float64) int   { return 0 }

func (b *MPVBackend) SampleRate() int {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	if b.outputParams.SampleRate > 0 {
		return b.outputParams.SampleRate
	}
	if b.sourceParams.SampleRate > 0 {
		return b.sourceParams.SampleRate
	}
	return 44100
}

// RegisterSourceResolver resolves provider URIs immediately before playback.
func (b *MPVBackend) RegisterSourceResolver(scheme string, resolver SourceResolver) {
	b.resolverMu.Lock()
	b.sourceResolvers[scheme] = resolver
	b.resolverMu.Unlock()
}

func (b *MPVBackend) FeatureError(feature Feature) error {
	switch feature {
	case FeatureEQ, FeatureMono, FeatureVisualizer:
		return fmt.Errorf("%s is unsupported with MPV backend", feature)
	case FeatureVolume, FeatureSpeed:
		b.stateMu.RLock()
		bitPerfect := b.bitPerfect
		b.stateMu.RUnlock()
		if bitPerfect {
			return fmt.Errorf("%s is locked in MPV bit-perfect mode", feature)
		}
	}
	return nil
}

func (b *MPVBackend) BackendStatus() BackendStatus {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return BackendStatus{
		Name:           "mpv",
		Device:         b.device,
		BitPerfectMode: b.bitPerfect,
		DSPDisabled:    b.bitPerfect,
		DirectALSA:     strings.HasPrefix(b.device, "alsa/hw:") && b.currentAO == "alsa",
		Source:         b.sourceParams,
		Output:         b.outputParams,
	}
}

func (b *MPVBackend) ListAudioDevices() ([]AudioDevice, error) {
	data, err := b.command([]any{"get_property", "audio-device-list"})
	if err != nil {
		return nil, fmt.Errorf("MPV audio-device-list: %w", err)
	}
	var entries []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode MPV audio-device-list: %w", err)
	}
	b.stateMu.RLock()
	active := b.device
	b.stateMu.RUnlock()
	devices := make([]AudioDevice, len(entries))
	for i, entry := range entries {
		devices[i] = AudioDevice{Index: i, Name: entry.Name, Description: entry.Description, Active: entry.Name == active}
	}
	return devices, nil
}

func (b *MPVBackend) SwitchAudioDevice(device string) error {
	if device == "" {
		return errors.New("MPV audio device cannot be empty")
	}
	b.stateMu.RLock()
	bitPerfect := b.bitPerfect
	b.stateMu.RUnlock()
	if bitPerfect && !strings.HasPrefix(device, "alsa/hw:") {
		return errors.New("MPV bit-perfect mode requires a direct ALSA device such as alsa/hw:CARD=Generic,DEV=0")
	}
	if _, err := b.command([]any{"set_property", "audio-device", device}); err != nil {
		return fmt.Errorf("MPV audio device: %w", err)
	}
	b.stateMu.Lock()
	b.device = device
	b.stateMu.Unlock()
	return nil
}

// Close terminates MPV, releases device reservation, and removes runtime files.
func (b *MPVBackend) Close() {
	b.closeOnce.Do(func() {
		b.stateMu.Lock()
		b.closing = true
		b.playing = false
		b.paused = false
		b.stateMu.Unlock()

		b.ipcMu.RLock()
		ipc := b.ipc
		b.ipcMu.RUnlock()
		if ipc != nil {
			_ = ipc.writeNoReply([]any{"quit"})
		}
		if b.cmd != nil && b.cmd.Process != nil {
			select {
			case <-b.processDone:
			case <-time.After(2 * time.Second):
				_ = b.cmd.Process.Kill()
				<-b.processDone
			}
		}
		if ipc != nil {
			ipc.fail(errMPVClosed)
		}
		if b.reservation != nil {
			if err := b.reservation.Close(); err != nil {
				applog.Warn("audio reservation release: %v", err)
			}
		}
		if b.runtimeDir != "" {
			if err := os.RemoveAll(b.runtimeDir); err != nil {
				applog.Warn("remove MPV runtime directory: %v", err)
			}
		}
		applog.Info("MPV backend shutdown complete")
	})
}

var _ Engine = (*MPVBackend)(nil)
var _ DeviceController = (*MPVBackend)(nil)
var _ FeatureReporter = (*MPVBackend)(nil)
var _ BackendReporter = (*MPVBackend)(nil)

type mpvMessage struct {
	Command   []any           `json:"command,omitempty"`
	RequestID *int64          `json:"request_id,omitempty"`
	Error     string          `json:"error,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Event     string          `json:"event,omitempty"`
	Name      string          `json:"name,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}

type mpvResult struct {
	message mpvMessage
	err     error
}

type mpvIPC struct {
	conn    net.Conn
	timeout time.Duration
	onEvent func(mpvMessage)

	writeMu  sync.Mutex
	nextID   int64
	mu       sync.Mutex
	pending  map[int64]chan mpvResult
	done     chan struct{}
	failOnce sync.Once
}

func newMPVIPC(conn net.Conn, timeout time.Duration, onEvent func(mpvMessage)) *mpvIPC {
	ipc := &mpvIPC{
		conn:    conn,
		timeout: timeout,
		onEvent: onEvent,
		pending: make(map[int64]chan mpvResult),
		done:    make(chan struct{}),
	}
	go ipc.readLoop()
	return ipc
}

func (c *mpvIPC) command(command []any) (json.RawMessage, error) {
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, errMPVClosed
	default:
	}
	c.nextID++
	id := c.nextID
	result := make(chan mpvResult, 1)
	c.pending[id] = result
	c.mu.Unlock()

	requestID := id
	if err := c.write(mpvMessage{Command: command, RequestID: &requestID}); err != nil {
		c.removePending(id)
		c.fail(err)
		return nil, err
	}
	name := "unknown"
	if len(command) > 0 {
		name = fmt.Sprint(command[0])
	}
	applog.Debug("MPV IPC request: id=%d command=%s", id, name)

	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.err != nil {
			return nil, response.err
		}
		if response.message.Error != "" && response.message.Error != "success" {
			applog.Warn("MPV IPC error: id=%d command=%s error=%s", id, name, response.message.Error)
			return nil, errors.New(response.message.Error)
		}
		return response.message.Data, nil
	case <-timer.C:
		c.removePending(id)
		return nil, fmt.Errorf("MPV request %d (%s) timed out after %s", id, name, c.timeout)
	case <-c.done:
		return nil, errMPVClosed
	}
}

func (c *mpvIPC) writeNoReply(command []any) error {
	return c.write(mpvMessage{Command: command})
}

func (c *mpvIPC) write(message mpvMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.timeout))
	_, err = c.conn.Write(data)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

func (c *mpvIPC) readLoop() {
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var message mpvMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			applog.Warn("decode MPV IPC message: %v", err)
			continue
		}
		if message.RequestID != nil && *message.RequestID != 0 {
			c.mu.Lock()
			result := c.pending[*message.RequestID]
			delete(c.pending, *message.RequestID)
			c.mu.Unlock()
			if result != nil {
				result <- mpvResult{message: message}
			}
			continue
		}
		if c.onEvent != nil {
			c.onEvent(message)
		}
	}
	err := scanner.Err()
	if err == nil {
		err = errors.New("MPV IPC connection closed")
	}
	c.fail(err)
}

func (c *mpvIPC) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *mpvIPC) fail(err error) {
	c.failOnce.Do(func() {
		_ = c.conn.Close()
		c.mu.Lock()
		pending := c.pending
		c.pending = make(map[int64]chan mpvResult)
		close(c.done)
		c.mu.Unlock()
		for _, result := range pending {
			select {
			case result <- mpvResult{err: err}:
			default:
			}
		}
	})
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
