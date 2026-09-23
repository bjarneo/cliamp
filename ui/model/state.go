// state.go defines sub-structs that group related fields in the Model,
// making the overall model scannable and maintainable.

package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// searchState holds state for the playlist search overlay.
type searchState struct {
	active  bool
	query   string
	results []int // indices into playlist tracks
	cursor  int
	scroll  int
}

type playlistUndo struct {
	active    bool
	snapshot  playlist.Snapshot
	loaded    string
	saved     []playlist.Track
	persisted bool
}

// netSearchScreenType identifies which screen of the net search overlay is active.
type netSearchScreenType int

const (
	netSearchInput   netSearchScreenType = iota // typing search query
	netSearchResults                            // browsing search results
)

// netSearchState holds state for the internet search overlay.
type netSearchState struct {
	active     bool
	screen     netSearchScreenType
	query      string
	soundcloud bool // true = SoundCloud (scsearch), false = YouTube (ytsearch)
	loading    bool
	results    []playlist.Track
	cursor     int
	scroll     int
	err        string
	request    string
}

// provSearchState holds state for filtering the provider playlist list.
type provSearchState struct {
	active  bool
	loading bool // catalog search in flight, before IsSearching reports results
	query   string
	results []int // indices into providerLists
	cursor  int
	scroll  int
}

// seekState holds debounce state for yt-dlp seek-by-restart.
type seekState struct {
	active    bool          // true from first keypress until seek completes
	inFlight  bool          // a decoder-restarting seek command is running
	gen       uint64        // bumped per track; completions from older tracks are ignored
	pending   bool          // targetPos still needs a commit once inFlight clears
	targetPos time.Duration // absolute target position
	timer     int           // tick countdown for debounce (0 = idle)
	grace     int           // ticks to suppress reconnect after seek completes
	timerFor  time.Duration
	graceFor  time.Duration
}

// themePickerState holds state for the theme picker overlay.
type themePickerState struct {
	visible     bool
	cursor      int // view index into filtered when filter != "", otherwise raw theme index
	scroll      int
	savedName   string // theme name before opening picker, for cancel/restore after reload
	filtering   bool
	filter      string
	filtered    []int // raw indices into [Default, themes...]
	savedCursor int
	savedScroll int
}

// visPickerState holds state for the visualizer picker overlay.
type visPickerState struct {
	visible     bool
	cursor      int // view index into filtered when filter != "", otherwise raw visualizer mode
	scroll      int
	savedMode   int      // vis.Mode before opening, for cancel/restore
	modes       []string // mode names captured at open (stable while open)
	filtering   bool
	filter      string
	filtered    []int // raw indices into modes
	savedCursor int
	savedScroll int
}

// lyricsState holds state for the lyrics display overlay.
type lyricsState struct {
	visible bool
	lines   []lyrics.Line
	loading bool
	err     error
	query   string // "artist\ntitle" of the last fetch
	scroll  int
	offset  time.Duration // synced-lyrics timestamp adjustment (persisted as lyrics_offset_ms)
}

// keymapOverlay holds state for the keybindings overlay.
type keymapOverlay struct {
	visible     bool
	cursor      int
	scroll      int
	savedCursor int
	savedScroll int
	searching   bool
	search      string
	filtered    []int         // indices into entries
	entries     []keymapEntry // core keys + plugin keys, rebuilt on openKeymap
}

// queueOverlay holds state for the queue manager overlay.
type queueOverlay struct {
	visible bool
	cursor  int
	scroll  int
}

// subsOverlay holds state for the subscribed-shows overlay. Subscriptions come
// from the provider's local store, so the list itself needs no network call;
// only the episode fetches triggered from it do.
type subsOverlay struct {
	visible bool
	cursor  int
	scroll  int
	shows   []provider.SubscriptionInfo
	// loader fetches episodes for shows in the list. It is the provider the
	// list came from, not the active one, which may be a different service.
	loader    provider.AlbumTrackLoader
	filtering bool
	filter    string
	filtered  []int // indices into shows; nil when filter is empty
	loading   bool
	status    string
	err       string
}

// plManagerState holds state for the playlist manager overlay.
type plManagerState struct {
	visible       bool
	screen        plMgrScreenType
	cursor        int // view-index: offset into filtered when filter != "", else direct index
	scroll        int
	playlists     []playlist.PlaylistInfo
	selPlaylist   string               // playlist name open in screen 1
	tracks        []playlist.Track     // tracks in the selected playlist
	missingLocal  []bool               // cached missing-file state, indexed with tracks
	dirs          []playlist.DirSource // [[dir]] sources for the selected playlist (screen 2)
	newName       string
	confirmDel    bool
	renameOldName string
	renameName    string
	inputErr      string
	marked        map[int]bool // real track indices marked on the tracks screen
	sortMode      int
	undo          plManagerUndo

	// Filter (`/`) state. Reset on screen change. `filtered` indexes into
	// `playlists` (list screen) or `tracks` (tracks screen).
	filtering   bool
	filter      string
	filtered    []int
	savedCursor int // cursor before `/` was pressed, restored on Esc
	savedScroll int
}

type plManagerUndoKind int

const (
	plUndoNone plManagerUndoKind = iota
	plUndoTracks
	plUndoPlaylist
)

type plManagerUndo struct {
	kind         plManagerUndoKind
	name         string
	tracks       []playlist.Track
	missingLocal []bool
	doc          []byte // raw TOML snapshot; when set, undo restores it verbatim
}

type playlistPickerScreen int

const (
	plPickerChoose playlistPickerScreen = iota
	plPickerNewName
)

// playlistPickerState holds the reusable local "write to playlist" picker.
type playlistPickerState struct {
	visible   bool
	screen    playlistPickerScreen
	cursor    int
	scroll    int
	playlists []playlist.PlaylistInfo
	tracks    []playlist.Track
	title     string
	newName   string
	inputErr  string

	// Remote section (appended when every selected track belongs to a
	// provider that implements provider.PlaylistWriter).
	remoteProv    playlist.Provider
	remoteName    string                  // provider display name; "" = no remote section
	remote        []playlist.PlaylistInfo // remote playlists to write to
	remoteLoading bool                    // remote list fetch in flight
	newNameRemote bool                    // new-name screen targets the remote provider
}

// fileBrowserState holds state for the file browser overlay.
type fileBrowserState struct {
	visible        bool
	dir            string
	entries        []fbEntry
	cursor         int
	scroll         int
	savedCursor    int
	savedScroll    int
	selected       map[string]bool
	err            string
	searching      bool
	search         string
	filtered       []int // indices into entries
	targetPlaylist string
	confirmReplace bool
}

// navBrowserState holds state for the provider browser overlay.
type navBrowserState struct {
	prov            playlist.Provider
	genreBrowser    provider.GenreBrowser
	visible         bool
	mode            navBrowseModeType
	screen          navBrowseScreenType
	cursor          int
	scroll          int
	artists         []provider.ArtistInfo
	albums          []provider.AlbumInfo
	tracks          []playlist.Track
	genres          []provider.GenreInfo
	genreSorts      []provider.SortType
	selArtist       provider.ArtistInfo
	selAlbum        provider.AlbumInfo
	selGenre        provider.GenreInfo
	selGenreSort    provider.SortType
	genreQuery      string
	sortType        string
	albumLoading    bool
	albumDone       bool
	loading         bool
	searching       bool
	search          string
	searchIdx       []int
	confirmReplace  bool
	directTrackJump bool
	fromProvList    bool
	openInPlaylist  bool
}

// requestState tracks the latest request in each independently asynchronous UI
// domain. Completion messages must match their generation before they can
// change the current screen.
type requestState struct {
	provider     uint64
	tracks       uint64
	nav          uint64
	lyrics       uint64
	netSearch    uint64
	spotSearch   uint64
	spotAlbum    uint64
	spotLists    uint64
	spotMutation uint64
	auth         uint64
	catalog      uint64
	stream       uint64
	preload      uint64
	provMutation uint64 // provider playlist writes (delete/unfollow/rename/remove)
	like         uint64 // track like/unlike toggles
	follow       uint64 // artist/playlist follow toggles
	smart        uint64 // Smart Shuffle recommendation fetches
	artist       uint64 // artist screen detail and discography drill fetches
	// Home sidebar fetches use one generation per section so a lazy album
	// page or creation refetch cannot make in-flight list/artist fetches
	// stale (stale drops would leave their loading flags stuck on).
	homeLists   uint64 // Home playlist-list fetches and creation refetches
	homeAlbums  uint64 // Home album pages and sort refetches
	homeArtists uint64 // Home artist-list fetches
	homeCreate  uint64 // Home new-playlist creation
	homeContent uint64 // Home content pane loads and incremental paging
}

func nextRequest(gen *uint64) uint64 {
	*gen = *gen + 1
	return *gen
}

// spotSearchScreenType identifies which screen of the Spotify search overlay is active.
type spotSearchScreenType int

const (
	spotSearchInput    spotSearchScreenType = iota // typing search query
	spotSearchResults                              // browsing search results
	spotSearchPlaylist                             // picking a playlist to add to
	spotSearchNewName                              // typing new playlist name
)

// spotSearchTab identifies a result tab when the searched provider implements
// provider.MultiSearcher.
type spotSearchTab int

const (
	spotTabTracks spotSearchTab = iota
	spotTabAlbums
	spotTabArtists
	spotTabPlaylists
	spotTabCount
)

var spotTabLabels = [spotTabCount]string{"Tracks", "Albums", "Artists", "Playlists"}

// spotDrillLevel is one drilled-in list inside the multi-type search results.
// A level lists either albums or tracks; tracks levels carry the same row
// actions as the search track tab.
type spotDrillLevel struct {
	crumb   string // one-line label of what was entered, e.g. "Album — Rumours"
	albums  []provider.AlbumInfo
	tracks  []playlist.Track
	loading bool
	cursor  int
	scroll  int
}

// spotSearchState holds state for the provider search + add-to-playlist overlay.
type spotSearchState struct {
	prov    playlist.Provider // the provider being searched (may differ from active provider)
	visible bool
	screen  spotSearchScreenType
	query   string
	results []playlist.Track
	cursor  int
	scroll  int
	loading bool
	// albumLoading is separate from loading so the results screen can say an
	// album is being expanded without claiming so during the playlist fetch.
	albumLoading bool
	playlists    []playlist.PlaylistInfo // user's Spotify playlists for picker
	selTrack     playlist.Track          // track selected to add
	newName      string                  // new playlist name input
	err          string
	cancel       func()
	multi        bool                   // provider implements MultiSearcher
	resultsAll   provider.SearchResults // multi-type results backing the tab bar
	tab          spotSearchTab          // active result tab (multi only)
	drill        []spotDrillLevel       // drill-down stack (multi only)
}

// artistSortMode identifies the ordering of the artist screen's Popular
// section; `s` cycles through them.
type artistSortMode int

const (
	artistSortPopularity artistSortMode = iota // popularity score, descending
	artistSortRecency                          // Track.Year, descending
	artistSortLikedFirst                       // liked rows first, stable by popularity
	artistSortCount
)

var artistSortLabels = [artistSortCount]string{"popularity", "recency", "liked"}

// artistScreenState holds the artist profile overlay. It opens on top of
// whichever surface drilled into it (search results, nav browser, later Home)
// and owns its own album drill stack for Discography rows, reusing the
// search drill machinery (spotDrillLevel crumbs; Esc pops one level).
type artistScreenState struct {
	prov    playlist.Provider // provider the artist belongs to
	visible bool
	info    provider.ArtistInfo // identity of the opened artist
	loading bool                // ArtistDetail fetch in flight
	detail  provider.ArtistDetail
	sort    artistSortMode   // ordering of the Popular section
	cursor  int              // index over selectable section rows (headers skipped)
	scroll  int              // first visible selectable row
	drill   []spotDrillLevel // Discography album drill stack
	cancel  func()           // context cancel of the in-flight request
}

// catalogBatchState holds state for lazy-loading catalog entries from a provider.CatalogLoader.
type catalogBatchState struct {
	offset  int  // next offset to fetch
	loading bool // true while a fetch is in flight
	done    bool // true when all stations have been loaded
}

// provConfirmState holds the inline delete/unfollow confirmation on the
// provider playlist pane.
type provConfirmState struct {
	active     bool
	playlistID string
	name       string
	owned      bool
}

// provListFixupState names a cursor adjustment to apply when the next
// playlistsLoadedMsg arrives from a write-triggered refresh.
type provListFixupState struct {
	clampCursor bool   // unfollow: keep provCursor in range after the list shrinks
	selectID    string // rename: move provCursor onto this playlist row
}

// provRenameState holds the inline rename input on the provider playlist pane.
type provRenameState struct {
	active     bool
	playlistID string
	oldName    string
	name       string
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
	scroll  int
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

// feedbackKind determines feedback styling and lifecycle.
type feedbackKind int

const (
	feedbackActivity feedbackKind = iota
	feedbackSuccess
	feedbackWarning
	feedbackError
)

// statusTTL is how long a status line stays visible.
type statusTTL time.Duration

func (t statusTTL) expiresAt(now time.Time) time.Time {
	return now.Add(time.Duration(t))
}

// statusMsg holds structured feedback shown at the bottom of the UI. A zero
// expiry is durable and remains until a later message replaces it or Clear is
// called. Inline form errors use their own state so they remain beside retry.
type statusMsg struct {
	kind      feedbackKind
	text      string
	expiresAt time.Time // zero = no active message
}

func (s statusMsg) Expired(now time.Time) bool {
	return !s.expiresAt.IsZero() && !now.Before(s.expiresAt)
}

func (s *statusMsg) Show(text string, ttl statusTTL) {
	s.Success(text, ttl)
}

func (s *statusMsg) Showf(ttl statusTTL, format string, args ...any) {
	s.Show(fmt.Sprintf(format, args...), ttl)
}

func (s *statusMsg) Activityf(ttl statusTTL, format string, args ...any) {
	s.Activity(fmt.Sprintf(format, args...), ttl)
}

func (s *statusMsg) Successf(ttl statusTTL, format string, args ...any) {
	s.Success(fmt.Sprintf(format, args...), ttl)
}

func (s *statusMsg) Warningf(ttl statusTTL, format string, args ...any) {
	s.Warning(fmt.Sprintf(format, args...), ttl)
}

func (s *statusMsg) Errorf(ttl statusTTL, format string, args ...any) {
	s.Error(fmt.Sprintf(format, args...), ttl)
}

func (s *statusMsg) Activity(text string, ttl statusTTL) {
	s.show(feedbackActivity, text, ttl)
}

func (s *statusMsg) Success(text string, ttl statusTTL) {
	s.show(feedbackSuccess, text, ttl)
}

func (s *statusMsg) Warning(text string, ttl statusTTL) {
	s.show(feedbackWarning, text, ttl)
}

func (s *statusMsg) Error(text string, ttl statusTTL) {
	s.show(feedbackError, text, ttl)
}

func (s *statusMsg) show(kind feedbackKind, text string, ttl statusTTL) {
	s.ShowAtKind(time.Now(), kind, text, ttl)
}

func (s *statusMsg) ShowAt(now time.Time, text string, ttl statusTTL) {
	s.ShowAtKind(now, feedbackSuccess, text, ttl)
}

func (s *statusMsg) ShowAtKind(now time.Time, kind feedbackKind, text string, ttl statusTTL) {
	s.kind = kind
	s.text = text
	if ttl > 0 {
		s.expiresAt = ttl.expiresAt(now)
	} else {
		s.expiresAt = time.Time{}
	}
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
