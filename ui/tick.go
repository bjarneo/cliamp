package ui

import "time"

// Tick intervals: fast for visualizer animation, slow for time/seek display.
const (
	TickAnim    = 16 * time.Millisecond  // ~60 FPS — smooth bar/wave/scope animation cadence
	TickWave    = TickAnim               // ~60 FPS — waveform modes (no FFT)
	TickFast    = 50 * time.Millisecond  // 20 FPS — per-frame-animated spectrum modes
	TickAnalyze = 33 * time.Millisecond  // ~30 Hz — FFT analysis cadence (independent of animation)
	TickSlow    = 200 * time.Millisecond // 5 FPS — visualizer off or overlay
	// TickLowPowerPlaying keeps playback bookkeeping responsive while avoiding
	// the 20 FPS time/seek refresh cost when low-power mode is enabled.
	TickLowPowerPlaying = 500 * time.Millisecond // 2 FPS — low-power playback UI cadence
	// TickIdle is used when the player is stopped or paused with nothing
	// animating (no overlay, no buffering, no pending status / reconnect).
	// Bubbletea wakes immediately on key / IPC / MPRIS / plugin messages, so
	// the tick at this cadence only services time-based self-changes
	// (status-message expiry, log-line aging) — those tolerate the latency.
	TickIdle = 1500 * time.Millisecond // ~0.7 Hz — fully idle, minimal CPU
)
