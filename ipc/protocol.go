// Package ipc provides Unix socket IPC for remote playback control of cliamp.
// The protocol is newline-delimited JSON over a Unix domain socket.
package ipc

// Compile-time interface check.
var _ Dispatcher = DispatcherFunc(nil)

// Request is the JSON command sent by the client.
type Request struct {
	Cmd      string  `json:"cmd"`
	Value    float64 `json:"value,omitempty"`
	Playlist string  `json:"playlist,omitempty"`
	Path     string  `json:"path,omitempty"`
}

// Response is the JSON response sent by the server.
type Response struct {
	OK       bool       `json:"ok"`
	Error    string     `json:"error,omitempty"`
	State    string     `json:"state,omitempty"`
	Track    *TrackInfo `json:"track,omitempty"`
	Position float64    `json:"position,omitempty"`
	Duration float64    `json:"duration,omitempty"`
	Volume   float64    `json:"volume,omitempty"`
	Playlist string     `json:"playlist,omitempty"`
	Index    int        `json:"index,omitempty"`
	Total    int        `json:"total,omitempty"`
}

// TrackInfo is the track metadata in a status response.
type TrackInfo struct {
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Path   string `json:"path"`
}

// DispatcherFunc adapts a plain function to the Dispatcher interface.
type DispatcherFunc func(msg interface{})

// Send implements Dispatcher.
func (f DispatcherFunc) Send(msg interface{}) { f(msg) }

// Messages sent to the TUI via prog.Send().

// PlayMsg requests playback to start (unpause).
type PlayMsg struct{}

// PauseMsg requests playback to pause.
type PauseMsg struct{}

// ToggleMsg requests play/pause toggle.
type ToggleMsg struct{}

// StopMsg requests playback to stop.
type StopMsg struct{}

// VolumeMsg requests a relative volume change in dB.
type VolumeMsg struct{ DB float64 }

// NextMsg requests advancing to the next track.
type NextMsg struct{}

// PrevMsg requests going to the previous track.
type PrevMsg struct{}

// SeekMsg requests a relative seek in seconds.
type SeekMsg struct{ Secs float64 }

// LoadMsg requests loading a playlist by name.
// Reply receives the result so the client can report errors.
type LoadMsg struct {
	Playlist string
	Reply    chan Response
}

// QueueMsg requests queuing a file path for playback.
type QueueMsg struct{ Path string }

// StatusRequestMsg asks the TUI for current state.
// The TUI writes the response to Reply and closes the channel.
type StatusRequestMsg struct {
	Reply chan Response
}
