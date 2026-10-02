// Package ipc provides Unix socket IPC for remote playback control of cliamp.
// The protocol is newline-delimited JSON over a Unix domain socket.
package ipc

// Request is the parameter object decoded from a V2 operation's params field.
// It is an in-process adapter while runtime owners migrate to narrower typed
// operation structs; it is never sent as a top-level protocol envelope.
type Request struct {
	Cmd      string      `json:"-"`
	Value    float64     `json:"value,omitempty"`
	Playlist string      `json:"playlist,omitempty"`
	Path     string      `json:"path,omitempty"`
	Name     string      `json:"name,omitempty"`
	Band     int         `json:"band,omitempty"`
	Sub      string      `json:"sub,omitempty"`
	Args     []string    `json:"args,omitempty"`
	Provider string      `json:"provider,omitempty"`
	Query    string      `json:"query,omitempty"`
	Artist   string      `json:"artist,omitempty"`
	Album    string      `json:"album,omitempty"`
	Sort     string      `json:"sort,omitempty"`
	Offset   int         `json:"offset,omitempty"`
	Index    int         `json:"index,omitempty"`
	To       int         `json:"to,omitempty"`
	Limit    int         `json:"limit,omitempty"`
	Revision uint64      `json:"if_revision,omitempty"`
	NewName  string      `json:"new_name,omitempty"`
	Track    *TrackInfo  `json:"track,omitempty"`
	Tracks   []TrackInfo `json:"tracks,omitempty"`
	Topics   []string    `json:"topics,omitempty"`
	Play     bool        `json:"play,omitempty"`
}

// Response is the operation-specific data embedded in a successful V2 job.
// V2Response and Job carry protocol success and failure state.
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

// TrackInfo is the track metadata in a status response.
type TrackInfo struct {
	Title         string            `json:"title,omitempty"`
	Artist        string            `json:"artist,omitempty"`
	Album         string            `json:"album,omitempty"`
	Genre         string            `json:"genre,omitempty"`
	Path          string            `json:"path"`
	AlbumArtURL   string            `json:"album_art_url,omitempty"`
	Year          int               `json:"year,omitempty"`
	TrackNumber   int               `json:"track_number,omitempty"`
	DurationSecs  int               `json:"duration_secs,omitempty"`
	Index         int               `json:"index,omitempty"`
	QueuePosition int               `json:"queue_position,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	StreamTitle   string            `json:"stream_title,omitempty"`
	Station       string            `json:"station,omitempty"`
	Realtime      bool              `json:"realtime,omitempty"`
	Restricted    bool              `json:"restricted,omitempty"`
	Feed          bool              `json:"feed,omitempty"`
	Bookmark      bool              `json:"bookmark,omitempty"`
	Unplayable    bool              `json:"unplayable,omitempty"`
	DirSourced    bool              `json:"dir_sourced,omitempty"`
	ProviderMeta  map[string]string `json:"provider_meta,omitempty"`
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
