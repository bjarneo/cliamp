// Package ipc provides Unix socket IPC for remote playback control of cliamp.
// The protocol is newline-delimited JSON over a Unix domain socket.
package ipc

import "time"

// Compile-time interface check.
var _ Dispatcher = DispatcherFunc(nil)

// Request is the JSON command sent by the client.
type Request struct {
	Cmd      string  `json:"cmd"`
	Value    float64 `json:"value,omitempty"`
	Playlist string  `json:"playlist,omitempty"`
	Path     string  `json:"path,omitempty"`
	Name     string  `json:"name,omitempty"`
	Band     int     `json:"band,omitempty"`
}

// Response is the JSON response sent by the server.
type Response struct {
	OK         bool       `json:"ok"`
	Error      string     `json:"error,omitempty"`
	State      string     `json:"state,omitempty"`
	Track      *TrackInfo `json:"track,omitempty"`
	Position   float64    `json:"position,omitempty"`
	Duration   float64    `json:"duration,omitempty"`
	Volume     float64    `json:"volume,omitempty"`
	Playlist   string     `json:"playlist,omitempty"`
	Index      int        `json:"index,omitempty"`
	Total      int        `json:"total,omitempty"`
	Visualizer string     `json:"visualizer,omitempty"`
	Shuffle    *bool      `json:"shuffle,omitempty"`
	Repeat     string     `json:"repeat,omitempty"`
	Mono       *bool      `json:"mono,omitempty"`
	Speed      float64    `json:"speed,omitempty"`
	EQPreset   string     `json:"eq_preset,omitempty"`
	Device     string     `json:"device,omitempty"`
}

// TrackInfo is the track metadata in a status response.
type TrackInfo struct {
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Path   string `json:"path"`
}

// DispatcherFunc adapts a plain function to the Dispatcher interface.
type DispatcherFunc func(msg any)

// Send implements Dispatcher.
func (f DispatcherFunc) Send(msg any) { f(msg) }

// IPC-specific messages sent to the TUI via prog.Send().
// For shared types (NextMsg, PrevMsg, StopMsg, PlayPauseMsg), see internal/playback.

// PlayMsg requests playback to start (unpause only, not toggle).
type PlayMsg struct{}

// PauseMsg requests playback to pause (pause only, not toggle).
type PauseMsg struct{}

// VolumeMsg requests a relative volume change in dB.
type VolumeMsg struct{ DB float64 }

// SeekMsg requests a relative seek.
type SeekMsg struct{ Offset time.Duration }

// LoadMsg requests loading a playlist by name.
// Reply receives the result so the client can report errors.
type LoadMsg struct {
	Playlist string
	Reply    chan Response
}

// QueueMsg requests queuing a file path for playback.
type QueueMsg struct{ Path string }

// ThemeMsg requests changing the TUI theme by name.
// Reply receives confirmation or error if theme not found.
type ThemeMsg struct {
	Name  string
	Reply chan Response
}

// VisMsg requests changing the active visualizer by name.
// If Name is "next", the visualizer cycles to the next mode.
// Reply receives confirmation or error if mode not found.
type VisMsg struct {
	Name  string
	Reply chan Response
}

// ShuffleMsg requests toggling or setting shuffle mode.
// If Name is "on"/"off", it sets the mode explicitly; "toggle" toggles.
type ShuffleMsg struct {
	Name  string
	Reply chan Response
}

// RepeatMsg requests setting or cycling the repeat mode.
// Name is "off", "all", "one", or "cycle".
type RepeatMsg struct {
	Name  string
	Reply chan Response
}

// MonoMsg requests toggling or setting mono mode.
// If Name is "on"/"off", it sets the mode explicitly; "toggle" toggles.
type MonoMsg struct {
	Name  string
	Reply chan Response
}

// SpeedMsg requests setting the playback speed.
type SpeedMsg struct {
	Speed float64
	Reply chan Response
}

// EQMsg requests setting EQ preset by name or a single band's gain.
// If Band >= 0, sets that band to Value dB. Otherwise applies preset Name.
type EQMsg struct {
	Name  string
	Band  int
	Value float64
	Reply chan Response
}

// DeviceMsg requests switching the audio output device or listing devices.
// If Name is "list", returns available devices. Otherwise switches to named device.
type DeviceMsg struct {
	Name  string
	Reply chan Response
}

// StatusRequestMsg asks the TUI for current state.
// The TUI writes the response to Reply and closes the channel.
type StatusRequestMsg struct {
	Reply chan Response
}
