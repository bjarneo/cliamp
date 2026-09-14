package player

import (
	"context"
	"time"
)

// Engine is the interface used by the TUI model and the daemon to control
// audio playback. It is satisfied by *Player and can be replaced with a mock
// for testing.
//
// A start is a three-step protocol. BeginStart reserves a ticket and revokes
// the previous pending start. Prepare opens the source on any goroutine and
// stores it in the engine without touching audio. CommitStart, on the
// controller's goroutine, swaps the prepared source into the speaker. Preloads
// follow the same shape through BeginPreload and CommitPreload. A method given
// a ticket that is no longer current fails with ErrRevoked or reports false.
type Engine interface {
	// Source setup is background work; only the caller commits prepared audio.
	BeginStart() (ticket uint64, ctx context.Context)
	BeginPreload() (ticket uint64, ctx context.Context)
	Prepare(ticket uint64, req StartRequest) error
	CommitStart(ticket uint64) (finished PlaybackStats, ok bool)
	CommitPreload(ticket uint64) bool
	ClearPreload()
	Stop() PlaybackStats
	Close()
	TogglePause()

	// Seeking
	Seek(ticket uint64, d time.Duration) error
	SeekYTDL(ticket uint64, d time.Duration) error
	CancelSeekYTDL()

	// State queries
	Snapshot() PlaybackStats
	IsPlaying() bool
	IsPaused() bool
	Drained() bool
	HasPreload() bool
	Seekable() bool
	IsYTDLSeek() bool
	TakeAdvance() (Advance, bool)

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
