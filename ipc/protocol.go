// Package ipc provides Unix socket IPC for remote playback control of cliamp.
// The protocol is newline-delimited JSON over a Unix domain socket.
package ipc

import "time"

// Compile-time interface check.
var _ Dispatcher = DispatcherFunc(nil)

// Request is the JSON command sent by the client.
type Request struct {
	Cmd      string     `json:"cmd"`
	Value    float64    `json:"value,omitempty"`
	Playlist string     `json:"playlist,omitempty"`
	Path     string     `json:"path,omitempty"`
	Name     string     `json:"name,omitempty"`
	Band     int        `json:"band,omitempty"`
	Sub      string     `json:"sub,omitempty"`
	Args     []string   `json:"args,omitempty"`
	Provider string     `json:"provider,omitempty"`
	Query    string     `json:"query,omitempty"`
	Artist   string     `json:"artist,omitempty"`
	Album    string     `json:"album,omitempty"`
	Sort     string     `json:"sort,omitempty"`
	Offset   int        `json:"offset,omitempty"`
	Index    int        `json:"index,omitempty"`
	To       int        `json:"to,omitempty"`
	Limit    int        `json:"limit,omitempty"`
	NewName  string     `json:"new_name,omitempty"`
	Track    *TrackInfo `json:"track,omitempty"`
	Topics   []string   `json:"topics,omitempty"`
}

// Response is the JSON response sent by the server.
type Response struct {
	OK         bool           `json:"ok"`
	Error      string         `json:"error,omitempty"`
	State      string         `json:"state,omitempty"`
	Track      *TrackInfo     `json:"track,omitempty"`
	Position   float64        `json:"position,omitempty"`
	Duration   float64        `json:"duration,omitempty"`
	Volume     float64        `json:"volume,omitempty"`
	Playlist   string         `json:"playlist,omitempty"`
	Index      int            `json:"index,omitempty"`
	Total      int            `json:"total,omitempty"`
	Visualizer string         `json:"visualizer,omitempty"`
	Shuffle    *bool          `json:"shuffle,omitempty"`
	Repeat     string         `json:"repeat,omitempty"`
	Mono       *bool          `json:"mono,omitempty"`
	Speed      float64        `json:"speed,omitempty"`
	EQPreset   string         `json:"eq_preset,omitempty"`
	Device     string         `json:"device,omitempty"`
	Output     string         `json:"output,omitempty"`
	Items      []string       `json:"items,omitempty"`
	Theme      *ThemeInfo     `json:"theme,omitempty"`
	Bands      []float64      `json:"bands,omitempty"`
	EQBands    []float64      `json:"eq_bands,omitempty"`
	Tracks     []TrackInfo    `json:"tracks,omitempty"`
	Playlists  []PlaylistInfo `json:"playlists,omitempty"`
	Providers  []ProviderInfo `json:"providers,omitempty"`
	Artists    []ArtistInfo   `json:"artists,omitempty"`
	Albums     []AlbumInfo    `json:"albums,omitempty"`
	Sorts      []SortInfo     `json:"sorts,omitempty"`
	Lyrics     []LyricLine    `json:"lyrics,omitempty"`
	History    []HistoryInfo  `json:"history,omitempty"`
	Devices    []DeviceInfo   `json:"devices,omitempty"`
}

// ThemeInfo carries the active theme name and its resolved hex colors.
// Empty hex fields mean the default (ANSI fallback) theme is active.
type ThemeInfo struct {
	Name     string `json:"name"`
	BG       string `json:"bg,omitempty"`
	Accent   string `json:"accent,omitempty"`
	Fg       string `json:"fg,omitempty"`
	BrightFg string `json:"bright_fg,omitempty"`
	Green    string `json:"green,omitempty"`
	Yellow   string `json:"yellow,omitempty"`
	Red      string `json:"red,omitempty"`
}

// PluginDispatcher is the hook the IPC server calls to forward plugin.call and
// plugin.commands requests to the Lua plugin manager. Optional — if nil, those
// subcommands return an error.
type PluginDispatcher interface {
	EmitCommand(plugin, command string, args []string) (string, error)
	CommandList() []string
}

// TrackInfo is the track metadata in a status response.
type TrackInfo struct {
	Title         string `json:"title,omitempty"`
	Artist        string `json:"artist,omitempty"`
	Album         string `json:"album,omitempty"`
	Genre         string `json:"genre,omitempty"`
	Path          string `json:"path"`
	AlbumArtURL   string `json:"album_art_url,omitempty"`
	Year          int    `json:"year,omitempty"`
	TrackNumber   int    `json:"track_number,omitempty"`
	DurationSecs  int    `json:"duration_secs,omitempty"`
	Index         int    `json:"index,omitempty"`
	QueuePosition int    `json:"queue_position,omitempty"`
	Stream        bool   `json:"stream,omitempty"`
	StreamTitle   string `json:"stream_title,omitempty"`
	Station       string `json:"station,omitempty"`
	Realtime      bool   `json:"realtime,omitempty"`
	Bookmark      bool   `json:"bookmark,omitempty"`
	Unplayable    bool   `json:"unplayable,omitempty"`
}

type PlaylistInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Section      string `json:"section,omitempty"`
	TrackCount   int    `json:"track_count,omitempty"`
	DurationSecs int    `json:"duration_secs,omitempty"`
	Favoritable  bool   `json:"favoritable,omitempty"`
	Favorite     bool   `json:"favorite,omitempty"`
}

type ProviderInfo struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	Searchable    bool   `json:"searchable"`
	BrowseArtists bool   `json:"browse_artists,omitempty"`
	BrowseAlbums  bool   `json:"browse_albums,omitempty"`
	Catalog       bool   `json:"catalog,omitempty"`
}

type ArtistInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AlbumCount int    `json:"album_count,omitempty"`
}

type AlbumInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Artist     string `json:"artist,omitempty"`
	ArtistID   string `json:"artist_id,omitempty"`
	Year       int    `json:"year,omitempty"`
	TrackCount int    `json:"track_count,omitempty"`
	Genre      string `json:"genre,omitempty"`
}

type SortInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type LyricLine struct {
	Start float64 `json:"start"`
	Text  string  `json:"text"`
}

type HistoryInfo struct {
	Track    TrackInfo `json:"track"`
	PlayedAt string    `json:"played_at"`
}

type DeviceInfo struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
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

// BandsRequestMsg asks the TUI for the current visualizer band values
// (smoothed) and the active visualizer mode name. Lightweight compared to
// StatusRequestMsg — intended for high-rate polling from external widgets.
type BandsRequestMsg struct {
	Reply chan Response
}

type QueueRequestMsg struct {
	Op    string
	Index int
	To    int
	Track *TrackInfo
	Reply chan Response
}

type LibraryRequestMsg struct {
	Op       string
	Provider string
	Playlist string
	Query    string
	Artist   string
	Album    string
	Sort     string
	Offset   int
	Limit    int
	Index    int
	NewName  string
	Track    *TrackInfo
	Reply    chan Response
}

type LyricsRequestMsg struct {
	Reply chan Response
}

type HistoryRequestMsg struct {
	Op    string
	Limit int
	Reply chan Response
}

type URLRequestMsg struct {
	URL   string
	Reply chan Response
}

type SaveRequestMsg struct {
	Reply chan Response
}
