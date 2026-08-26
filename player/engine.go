package player

import "time"

// Engine is the interface used by the TUI model to control audio playback.
// It is satisfied by *Player and can be replaced with a mock for testing.
type Engine interface {
	// Playback control
	Play(path string, knownDuration time.Duration) error
	PlayAt(path string, knownDuration, offset time.Duration) error
	PlayYTDL(pageURL string, knownDuration time.Duration) error
	SetPlaybackGeneration(generation uint64)
	PlayAtForGeneration(path string, knownDuration, offset time.Duration, generation uint64) error
	PlayYTDLForGeneration(pageURL string, knownDuration time.Duration, generation uint64) error
	Preload(path string, knownDuration time.Duration) error
	PreloadYTDL(pageURL string, knownDuration time.Duration) error
	BeginPreload() uint64
	PreloadForGeneration(path string, knownDuration time.Duration, generation uint64) error
	PreloadYTDLForGeneration(pageURL string, knownDuration time.Duration, generation uint64) error
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
	LastPlayedDuration() time.Duration

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
