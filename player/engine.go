package player

import "time"

// Feature identifies optional controls that are not supported by every
// playback engine.
type Feature string

const (
	FeatureEQ         Feature = "EQ"
	FeatureMono       Feature = "mono"
	FeatureVisualizer Feature = "visualizer"
	FeatureVolume     Feature = "volume"
	FeatureSpeed      Feature = "speed"
)

// FeatureReporter is optional. Engines that omit it support all features.
type FeatureReporter interface {
	FeatureError(Feature) error
}

// FeatureError reports whether engine supports feature.
func FeatureError(engine Engine, feature Feature) error {
	if reporter, ok := engine.(FeatureReporter); ok {
		return reporter.FeatureError(feature)
	}
	return nil
}

// DeviceController lets an engine own device discovery and switching.
type DeviceController interface {
	ListAudioDevices() ([]AudioDevice, error)
	SwitchAudioDevice(string) error
}

// EngineAudioDevices uses backend-specific discovery when available.
func EngineAudioDevices(engine Engine) ([]AudioDevice, error) {
	if controller, ok := engine.(DeviceController); ok {
		return controller.ListAudioDevices()
	}
	return ListAudioDevices()
}

// SwitchEngineAudioDevice uses backend-specific switching when available.
func SwitchEngineAudioDevice(engine Engine, device string) error {
	if controller, ok := engine.(DeviceController); ok {
		return controller.SwitchAudioDevice(device)
	}
	return SwitchAudioDevice(device)
}

// AudioParameters describes MPV's decoder or audio-output format.
type AudioParameters struct {
	Format     string
	SampleRate int
	Channels   string
}

// BackendStatus is optional diagnostic data exposed by a playback engine.
type BackendStatus struct {
	Name           string
	Device         string
	BitPerfectMode bool
	DSPDisabled    bool
	DirectALSA     bool
	Source         AudioParameters
	Output         AudioParameters
}

// BackendReporter exposes diagnostic backend state.
type BackendReporter interface {
	BackendStatus() BackendStatus
}

// EngineBackendStatus returns backend diagnostics when available.
func EngineBackendStatus(engine Engine) BackendStatus {
	if reporter, ok := engine.(BackendReporter); ok {
		return reporter.BackendStatus()
	}
	return BackendStatus{}
}

// Engine is the interface used by the TUI model to control audio playback.
// It is satisfied by *Player and can be replaced with a mock for testing.
type Engine interface {
	// Playback control
	Play(path string, knownDuration time.Duration) error
	PlayYTDL(pageURL string, knownDuration time.Duration) error
	Preload(path string, knownDuration time.Duration) error
	PreloadYTDL(pageURL string, knownDuration time.Duration) error
	ClearPreload()
	Stop()
	Close()
	TogglePause()

	// Seeking
	Seek(d time.Duration) error
	SeekYTDL(d time.Duration) error
	CancelSeekYTDL()

	// State queries
	IsPlaying() bool
	IsPaused() bool
	Drained() bool
	HasPreload() bool
	Seekable() bool
	IsStreamSeek() bool
	IsYTDLSeek() bool
	GaplessAdvanced() bool

	// Position and duration
	Position() time.Duration
	Duration() time.Duration
	PositionAndDuration() (time.Duration, time.Duration)

	// Volume
	SetVolumeMin(db float64)
	VolumeMin() float64
	SetVolume(db float64)
	Volume() float64

	// Speed
	SetSpeed(ratio float64)
	Speed() float64

	// Mono
	ToggleMono()
	Mono() bool

	// EQ
	SetEQBand(band int, dB float64)
	EQBands() [10]float64

	// Stream info
	StreamErr() error
	StreamTitle() string
	StreamBytes() (downloaded, total int64)

	// Audio samples for visualizer
	SamplesInto(dst []float64) int
	WaveformSamplesInto(dst []float64) int
	StereoSamplesInto(dst [][2]float64) int
	SampleRate() int
}

// Compile-time check that *Player satisfies Engine.
var _ Engine = (*Player)(nil)

// BackendStatus reports the native engine and its configured output rate.
func (p *Player) BackendStatus() BackendStatus {
	return BackendStatus{
		Name:   "native",
		Output: AudioParameters{SampleRate: int(p.sr)},
	}
}
