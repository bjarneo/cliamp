// Package control defines shared message types for external playback control.
// Used by both MPRIS (D-Bus) and IPC (Unix socket) to avoid type duplication.
package control

// ToggleMsg requests a play/pause toggle.
type ToggleMsg struct{}

// NextMsg requests advancing to the next track.
type NextMsg struct{}

// PrevMsg requests going to the previous track.
type PrevMsg struct{}

// StopMsg requests playback to stop.
type StopMsg struct{}
