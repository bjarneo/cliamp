// state.go defines sub-structs that group related fields in the Model,
// making the overall model scannable and maintainable.

package model

import (
	"fmt"
	"strings"
	"time"

	"cliamp/applog"
	"cliamp/lyrics"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/provider"
)

// searchState holds state for the playlist search overlay.
type searchState struct {
	active  bool
	query   string
	results []int // indices into playlist tracks
	cursor  int
}

// netSearchState holds state for the internet search overlay.
type netSearchState struct {
	active     bool
	query      string
	soundcloud bool // true = SoundCloud (scsearch), false = YouTube (ytsearch)
}

// provSearchState holds state for filtering the provider playlist list.
type provSearchState struct {
	active  bool
	query   string
	results []int // indices into providerLists
	cursor  int
}

// seekState holds debounce state for yt-dlp seek-by-restart.
type seekState struct {
	active    bool          // true from first keypress until seek completes
	targetPos time.Duration // absolute target position
	timer     int           // tick countdown for debounce (0 = idle)
	grace     int           // ticks to suppress reconnect after seek completes
	timerFor  time.Duration
	graceFor  time.Duration
}

// themePickerState holds state for the theme picker overlay.
type themePickerState struct {
	visible  bool
	cursor   int
	savedIdx int // themeIdx before opening picker, for cancel/restore
}

// lyricsState holds state for the lyrics display overlay.
type lyricsState struct {
	visible bool
	lines   []lyrics.Line
	loading bool
	err     error
	query   string // "artist\ntitle" of the last fetch
	scroll  int
}

// keymapOverlay holds state for the keybindings overlay.
type keymapOverlay struct {
	visible  bool
	cursor   int
	scroll   int
	search   string
	filtered []int // indices into keymapEntries
}

// queueOverlay holds state for the queue manager overlay.
type queueOverlay struct {
	visible bool
	cursor  int
}

// plManagerState holds state for the playlist manager overlay.
type plManagerState struct {
	visible     bool
	screen      plMgrScreenType
	cursor      int
	playlists   []playlist.PlaylistInfo
	selPlaylist string           // playlist name open in screen 1
	tracks      []playlist.Track // tracks in the selected playlist
	newName     string
	confirmDel  bool
}

// fileBrowserState holds state for the file browser overlay.
type fileBrowserState struct {
	visible  bool
	dir      string
	entries  []fbEntry
	cursor   int
	scroll   int
	selected map[string]bool
	err      string
}

// navBrowserState holds state for the provider browser overlay.
type navBrowserState struct {
	prov         playlist.Provider
	visible      bool
	mode         navBrowseModeType
	screen       navBrowseScreenType
	cursor       int
	scroll       int
	artists      []provider.ArtistInfo
	albums       []provider.AlbumInfo
	tracks       []playlist.Track
	selArtist    provider.ArtistInfo
	selAlbum     provider.AlbumInfo
	sortType     string
	albumLoading bool
	albumDone    bool
	loading      bool
	searching    bool
	search       string
	searchIdx    []int
}

// spotSearchScreenType identifies which screen of the Spotify search overlay is active.
type spotSearchScreenType int

const (
	spotSearchInput    spotSearchScreenType = iota // typing search query
	spotSearchResults                              // browsing search results
	spotSearchPlaylist                             // picking a playlist to add to
	spotSearchNewName                              // typing new playlist name
)

// spotSearchState holds state for the provider search + add-to-playlist overlay.
type spotSearchState struct {
	prov      playlist.Provider // the provider being searched (may differ from active provider)
	visible   bool
	screen    spotSearchScreenType
	query     string
	results   []playlist.Track
	cursor    int
	loading   bool
	playlists []playlist.PlaylistInfo // user's Spotify playlists for picker
	selTrack  playlist.Track          // track selected to add
	newName   string                  // new playlist name input
	err       string
}

// catalogBatchState holds state for lazy-loading catalog entries from a provider.CatalogLoader.
type catalogBatchState struct {
	offset  int  // next offset to fetch
	loading bool // true while a fetch is in flight
	done    bool // true when all stations have been loaded
}

// ytdlBatchState holds state for incremental yt-dlp playlist loading.
type ytdlBatchState struct {
	url     string
	gen     uint64
	offset  int
	done    bool
	loading bool
}

// reconnectState holds state for stream auto-reconnect with exponential backoff.
type reconnectState struct {
	attempts int
	at       time.Time
}

// devicePickerState holds state for the audio device picker overlay.
type devicePickerState struct {
	visible bool
	devices []player.AudioDevice
	cursor  int
	loading bool
}

type saveState struct {
	pendingDownloads int
}

func (s saveState) activityText() string {
	switch s.pendingDownloads {
	case 0:
		return ""
	case 1:
		return "Downloading..."
	default:
		return fmt.Sprintf("Downloading... (%d)", s.pendingDownloads)
	}
}

func (s *saveState) startDownload() {
	s.pendingDownloads++
}

func (s *saveState) finishDownload() {
	if s.pendingDownloads > 0 {
		s.pendingDownloads--
	}
}

// statusTTL is how long a status line stays visible.
type statusTTL time.Duration

func (t statusTTL) expiresAt(now time.Time) time.Time {
	return now.Add(time.Duration(t))
}

// statusMsg holds a temporary status message shown at the bottom of the UI.
type statusMsg struct {
	text      string
	expiresAt time.Time // zero = no active message
}

func (s statusMsg) Expired(now time.Time) bool {
	return !s.expiresAt.IsZero() && !now.Before(s.expiresAt)
}

func (s *statusMsg) Show(text string, ttl statusTTL) {
	s.ShowAt(time.Now(), text, ttl)
}

func (s *statusMsg) Showf(ttl statusTTL, format string, args ...any) {
	s.Show(fmt.Sprintf(format, args...), ttl)
}

func (s *statusMsg) ShowAt(now time.Time, text string, ttl statusTTL) {
	s.text = text
	s.expiresAt = ttl.expiresAt(now)
}

func (s *statusMsg) Clear() {
	*s = statusMsg{}
}

// logLine is a timestamped log message shown in the footer.
type logLine struct {
	text      string
	expiresAt time.Time
}

const logLineTTL = 6 * time.Second

// tickLogLines drains the applog buffer and expires old entries.
func (m *Model) tickLogLines(now time.Time) {
	for _, e := range applog.Drain() {
		text := strings.TrimRight(e.Text, "\n")
		m.logLines = append(m.logLines, logLine{
			text:      text,
			expiresAt: e.At.Add(logLineTTL),
		})
	}
	// Expire old entries.
	n := 0
	for _, l := range m.logLines {
		if now.Before(l.expiresAt) {
			m.logLines[n] = l
			n++
		}
	}
	m.logLines = m.logLines[:n]
}

// networkStats tracks network throughput for the stream status bar.
type networkStats struct {
	speed     float64 // bytes per second (smoothed)
	lastBytes int64
	sampleFor time.Duration
}

type terminalTitleState struct {
	introActive bool
	introOffset int
	introTick   int
}
