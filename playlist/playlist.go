// Package playlist manages an ordered track list with shuffle and repeat support.
package playlist

import (
	"maps"
	"math/rand"
	"net/url"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// RepeatMode controls playlist repeat behavior.
type RepeatMode int

const (
	RepeatOff RepeatMode = iota
	RepeatAll
	RepeatOne
)

func (r RepeatMode) String() string {
	switch r {
	case RepeatAll:
		return "All"
	case RepeatOne:
		return "One"
	default:
		return "Off"
	}
}

// Track represents a single audio file or HTTP stream.
type Track struct {
	Path         string
	Title        string
	Artist       string
	Album        string
	Genre        string
	Year         int
	TrackNumber  int
	Stream       bool // true for HTTP/HTTPS URLs
	Realtime     bool // true for real-time/live streams (e.g. radio)
	Feed         bool // true for RSS/podcast feed URLs (resolved before playback)
	DurationSecs int  // known duration in seconds (0 = unknown)
	Bookmark     bool // user-bookmarked track

	Unplayable bool // true when the track is known not playable in the current playback context

	DirSourced bool // true when expanded from a [[dir]] playlist section; re-derived on load, never persisted

	EmbeddedLyrics string // embedded lyrics from local file tags, when present
	AlbumArtURL    string // file:// URL for cached embedded album art, when present

	// ProviderMeta holds provider-specific key-value pairs.
	// Keys are namespaced by provider, e.g. "navidrome.id", "jellyfin.id".
	ProviderMeta map[string]string
}

// Meta returns the value for a provider-specific metadata key, or "" if unset.
func (t Track) Meta(key string) string {
	if t.ProviderMeta == nil {
		return ""
	}
	return t.ProviderMeta[key]
}

// TotalDurationSecs sums DurationSecs across a slice of tracks, skipping
// entries with unknown duration (zero).
func TotalDurationSecs(tracks []Track) int {
	total := 0
	for _, t := range tracks {
		if t.DurationSecs > 0 {
			total += t.DurationSecs
		}
	}
	return total
}

// IsURL reports whether path is an HTTP or HTTPS URL, or a yt-dlp search protocol string.
func IsURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") ||
		IsYTSearch(path)
}

// IsYTSearch reports whether path is a yt-dlp search expression
// (ytsearch:, ytsearchN:, scsearch:, scsearchN:).
func IsYTSearch(path string) bool {
	return matchSearchPrefix(path, "ytsearch") || matchSearchPrefix(path, "scsearch")
}

func matchSearchPrefix(path, name string) bool {
	if !strings.HasPrefix(path, name) {
		return false
	}
	rest := path[len(name):]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return false
	}
	for _, c := range rest[:colon] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// IsM3U reports whether the path points to an M3U playlist file (URL or local).
func IsM3U(path string) bool {
	if IsURL(path) {
		u, err := url.Parse(path)
		if err != nil {
			return false
		}
		ext := strings.ToLower(pathpkg.Ext(u.Path))
		return ext == ".m3u" || ext == ".m3u8"
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".m3u" || ext == ".m3u8"
}

// IsLocalM3U reports whether the path is a local (non-URL) M3U file.
func IsLocalM3U(path string) bool {
	return !IsURL(path) && IsM3U(path)
}

// IsPLS reports whether the path points to a PLS playlist file (URL or local).
func IsPLS(path string) bool {
	if IsURL(path) {
		u, err := url.Parse(path)
		if err != nil {
			return false
		}
		return strings.ToLower(pathpkg.Ext(u.Path)) == ".pls"
	}
	return strings.ToLower(filepath.Ext(path)) == ".pls"
}

// IsLocalPLS reports whether the path is a local (non-URL) PLS file.
func IsLocalPLS(path string) bool {
	return !IsURL(path) && IsPLS(path)
}

// IsYouTubeURL reports whether the URL points to YouTube (youtube.com or youtu.be).
// YouTube Music (music.youtube.com) is excluded — use IsYouTubeMusicURL for that.
func IsYouTubeURL(path string) bool {
	if !IsURL(path) {
		return false
	}
	// ytsearch: protocols are handled by yt-dlp, not the native YouTube client.
	if IsYTSearch(path) {
		return false
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	switch host {
	case "youtube.com", "youtu.be":
		return true
	}
	return false
}

// IsYouTubeMusicURL reports whether the URL points to YouTube Music (music.youtube.com).
// These URLs require yt-dlp rather than the native YouTube API client.
func IsYouTubeMusicURL(path string) bool {
	if !IsURL(path) {
		return false
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	return host == "music.youtube.com"
}

// IsYTDL reports whether the URL points to a site supported by yt-dlp
// (YouTube, SoundCloud, Bandcamp, ytsearch: protocol, etc.).
func IsYTDL(path string) bool {
	if !IsURL(path) {
		return false
	}
	// YouTube and YouTube Music URLs are handled by yt-dlp for playback.
	if IsYouTubeURL(path) || IsYouTubeMusicURL(path) {
		return true
	}
	if IsYTSearch(path) {
		return true
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	switch host {
	case "soundcloud.com",
		"bandcamp.com",
		"music.163.com",
		"bilibili.com",
		"b23.tv":
		return true
	}
	// Bilibili subdomains (e.g. space.bilibili.com)
	if strings.HasSuffix(host, ".bilibili.com") {
		return true
	}
	// Bandcamp artist subdomains (e.g. artist.bandcamp.com)
	if strings.HasSuffix(host, ".bandcamp.com") {
		return true
	}
	return false
}

// IsXiaoyuzhouEpisode reports whether the URL points to a Xiaoyuzhou episode page.
func IsXiaoyuzhouEpisode(path string) bool {
	if !IsURL(path) {
		return false
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	if host != "xiaoyuzhoufm.com" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(u.Path), "/episode/")
}

// IsFeed reports whether the URL points to a podcast RSS/XML feed.
func IsFeed(path string) bool {
	if !IsURL(path) {
		return false
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	ext := strings.ToLower(pathpkg.Ext(u.Path))
	return ext == ".xml" || ext == ".rss" || ext == ".atom"
}

// TrackFromPath creates a Track by parsing the filename or URL.
// For local files, embedded tags (ID3v2, Vorbis, MP4) are tried first,
// falling back to "Artist - Title" filename parsing.
func TrackFromPath(path string) Track {
	if IsURL(path) {
		return trackFromURL(path)
	}
	return readTags(path)
}

// trackFromURL creates a Track from an HTTP/HTTPS URL, extracting a clean
// display title from the URL path (ignoring query parameters).
func trackFromURL(rawURL string) Track {
	t := Track{Path: rawURL, Stream: true}

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Title = rawURL
		return t
	}

	// Extract filename from URL path using slash semantics, not OS-specific
	// filepath rules. URL paths always use '/'.
	base := pathpkg.Base(u.Path)
	if base != "" && base != "." && base != "/" {
		name := strings.TrimSuffix(base, pathpkg.Ext(base))
		if name != "" && name != "stream" && name != "rest" {
			t.Title = name
			return t
		}
	}

	// Fallback: use hostname
	t.Title = u.Hostname()
	return t
}

// IsLive reports whether the track is a live stream (e.g. Icecast radio)
func (t Track) IsLive() bool {
	return t.Realtime
}

// DisplayName returns a formatted display string for the track.
func (t Track) DisplayName() string {
	if t.Artist != "" {
		return t.Artist + " - " + t.Title
	}
	return t.Title
}

// Playlist manages an ordered list of tracks with shuffle and repeat support.
// All exported methods are safe for concurrent use: the Bubbletea UI loop
// mutates the playlist while Lua plugin goroutines read state through it.
type Playlist struct {
	mu             sync.Mutex
	tracks         []Track
	order          []int // indices into tracks, shuffled or sequential
	pos            int   // current position in order
	shuffle        bool
	repeat         RepeatMode
	queue          []int       // track indices queued to play next
	queuePositions map[int]int // first 1-based queue position by track index
	queuedIdx      int         // track index currently playing from queue, -1 if none
	bookmarkCount  int         // number of tracks with Bookmark set
}

// Snapshot preserves the complete mutable playback state for later restoration.
// Its fields are intentionally private so callers can only return it to Restore.
type Snapshot struct {
	tracks    []Track
	order     []int
	pos       int
	shuffle   bool
	repeat    RepeatMode
	queue     []int
	queuedIdx int
}

// New creates an empty Playlist.
func New() *Playlist {
	return &Playlist{queuedIdx: -1}
}

func (p *Playlist) rebuildQueuePositions() {
	if len(p.queue) == 0 {
		p.queuePositions = nil
		return
	}
	p.queuePositions = make(map[int]int, len(p.queue))
	for i, idx := range p.queue {
		if _, exists := p.queuePositions[idx]; !exists {
			p.queuePositions[idx] = i + 1
		}
	}
}

func (p *Playlist) rebuildBookmarkCount() {
	p.bookmarkCount = 0
	for _, track := range p.tracks {
		if track.Bookmark {
			p.bookmarkCount++
		}
	}
}

func cloneTrack(track Track) Track {
	track.ProviderMeta = maps.Clone(track.ProviderMeta)
	return track
}

func cloneTracks(tracks []Track) []Track {
	cloned := slices.Clone(tracks)
	for i := range cloned {
		cloned[i] = cloneTrack(cloned[i])
	}
	return cloned
}

func windowBounds(length, start, limit int) (int, int) {
	start = max(0, start)
	if limit <= 0 || start >= length {
		return 0, 0
	}
	end := length
	if limit < end-start {
		end = start + limit
	}
	return start, end
}

func (p *Playlist) cloneQueuedTracks(queue []int) []Track {
	tracks := make([]Track, len(queue))
	for i, idx := range queue {
		tracks[i] = cloneTrack(p.tracks[idx])
	}
	return tracks
}

// Replace clears the playlist and loads the given tracks, resetting
// position, queue, and shuffle order.
func (p *Playlist) Replace(tracks []Track) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tracks = cloneTracks(tracks)
	p.order = make([]int, len(tracks))
	for i := range tracks {
		p.order[i] = i
	}
	p.pos = 0
	p.queue = nil
	p.queuePositions = nil
	p.queuedIdx = -1
	p.rebuildBookmarkCount()
	if p.shuffle && len(tracks) > 0 {
		p.doShuffle()
	}
}

// Add appends tracks to the playlist.
func (p *Playlist) Add(tracks ...Track) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tracks = cloneTracks(tracks)
	start := len(p.tracks)
	p.tracks = append(p.tracks, tracks...)
	for _, track := range tracks {
		if track.Bookmark {
			p.bookmarkCount++
		}
	}
	for i := start; i < len(p.tracks); i++ {
		p.order = append(p.order, i)
	}
	if !p.shuffle || len(tracks) == 0 {
		return
	}
	// Shuffle mode: mix newly added tracks into the upcoming playback order
	// without disturbing already-played items or the current position.
	if start == 0 {
		p.pos = 0
		p.doShuffle()
		return
	}
	if p.pos < 0 {
		p.pos = 0
	}
	if p.pos >= len(p.order) {
		// Inconsistent internal state; recover by re-shuffling so newly added
		// tracks don't end up in sequential order.
		p.pos = 0
		p.doShuffle()
		return
	}
	// tail is an alias into p.order's backing array; shuffling it
	// directly reorders the upcoming entries in p.order in-place.
	tail := p.order[p.pos+1:]
	for i := len(tail) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		tail[i], tail[j] = tail[j], tail[i]
	}
}

// Len returns the number of tracks.
func (p *Playlist) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.tracks)
}

func (p *Playlist) currentTrackIndex() int {
	if len(p.order) == 0 {
		return -1
	}
	if p.queuedIdx >= 0 {
		return p.queuedIdx
	}
	return p.currentOrderTrackIndex()
}

func (p *Playlist) currentOrderTrackIndex() int {
	if len(p.order) == 0 {
		return -1
	}
	return p.order[p.pos]
}

func (p *Playlist) isPlayable(idx int) bool {
	return idx >= 0 && idx < len(p.tracks) && !p.tracks[idx].Unplayable
}

func (p *Playlist) firstPlayableOrderSlot(from, to int) (orderPos int, trackIdx int, ok bool) {
	for i := from; i < to && i < len(p.order); i++ {
		idx := p.order[i]
		if p.isPlayable(idx) {
			return i, idx, true
		}
	}
	return -1, -1, false
}

func (p *Playlist) lastPlayableOrderSlot(from int) (orderPos int, trackIdx int, ok bool) {
	if from >= len(p.order) {
		from = len(p.order) - 1
	}
	for i := from; i >= 0; i-- {
		idx := p.order[i]
		if p.isPlayable(idx) {
			return i, idx, true
		}
	}
	return -1, -1, false
}

func (p *Playlist) nextPlayableQueued() (trackIdx int, remaining []int, ok bool) {
	for i, idx := range p.queue {
		if p.isPlayable(idx) {
			return idx, p.queue[i+1:], true
		}
	}
	return -1, nil, false
}

func (p *Playlist) nextShuffleWrap() (orderPos int, trackIdx int, ok bool) {
	origOrder := slices.Clone(p.order)
	origPos := p.pos
	p.doShuffle()
	if orderPos, trackIdx, ok = p.firstPlayableOrderSlot(1, len(p.order)); ok {
		return orderPos, trackIdx, true
	}
	if orderPos, trackIdx, ok = p.firstPlayableOrderSlot(0, 1); ok {
		return orderPos, trackIdx, true
	}
	p.order = origOrder
	p.pos = origPos
	return -1, -1, false
}

func (p *Playlist) advanceFromOrder() (orderPos int, trackIdx int, ok bool) {
	if orderPos, trackIdx, ok = p.firstPlayableOrderSlot(p.pos+1, len(p.order)); ok {
		return orderPos, trackIdx, true
	}
	if p.repeat != RepeatAll {
		return -1, -1, false
	}
	if p.shuffle && p.atShuffleWrap() {
		return p.nextShuffleWrap()
	}
	return p.firstPlayableOrderSlot(0, len(p.order))
}

func (p *Playlist) resolveSelectedPlayablePos() (orderPos int, trackIdx int, ok bool) {
	if len(p.order) == 0 {
		return -1, -1, false
	}
	if idx := p.order[p.pos]; p.isPlayable(idx) {
		return p.pos, idx, true
	}
	if orderPos, trackIdx, ok = p.firstPlayableOrderSlot(p.pos+1, len(p.order)); ok {
		return orderPos, trackIdx, true
	}
	if p.repeat == RepeatAll {
		return p.firstPlayableOrderSlot(0, p.pos)
	}
	return -1, -1, false
}

// Current returns the currently selected track and its index.
func (p *Playlist) Current() (Track, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tracks) == 0 {
		return Track{}, -1
	}
	idx := p.currentTrackIndex()
	return cloneTrack(p.tracks[idx]), idx
}

// Index returns the track index of the current position.
func (p *Playlist) Index() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.currentTrackIndex()
}

func (p *Playlist) CurrentIsQueued() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.queuedIdx >= 0
}

func (p *Playlist) atShuffleWrap() bool {
	return p.repeat == RepeatAll && p.shuffle && len(p.queue) == 0 && p.queuedIdx == -1 && p.pos+1 >= len(p.order)
}

// SelectionActivation describes the playable track activated from the selected row.
type SelectionActivation struct {
	Track   Track
	Index   int
	Skipped bool
}

// ActivateSelected promotes the selected row to the active playable track.
// Queue state is ignored for candidate selection and left unchanged. If no
// playable track can be activated, playlist state is unchanged.
func (p *Playlist) ActivateSelected() (SelectionActivation, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	selectedPos := p.pos
	orderPos, idx, ok := p.resolveSelectedPlayablePos()
	if !ok {
		return SelectionActivation{}, false
	}
	p.pos = orderPos
	p.queuedIdx = -1
	return SelectionActivation{
		Track:   cloneTrack(p.tracks[idx]),
		Index:   idx,
		Skipped: orderPos != selectedPos,
	}, true
}

// Next advances to the next track according to queue, repeat, and shuffle.
// Unplayable queued entries are pruned as playback advances. RepeatOne still
// limits playback to the current track.
func (p *Playlist) Next() (Track, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tracks) == 0 {
		return Track{}, false
	}
	origPos := p.pos
	origQueuedIdx := p.queuedIdx

	if idx, remaining, ok := p.nextPlayableQueued(); ok {
		p.queue = remaining
		p.rebuildQueuePositions()
		p.queuedIdx = idx
		return cloneTrack(p.tracks[idx]), true
	}
	if len(p.queue) > 0 {
		p.queue = nil
		p.queuePositions = nil
	}
	if p.repeat == RepeatOne {
		idx := p.currentOrderTrackIndex()
		if p.isPlayable(idx) {
			p.queuedIdx = -1
			return cloneTrack(p.tracks[idx]), true
		}
		p.pos = origPos
		p.queuedIdx = origQueuedIdx
		return Track{}, false
	}

	orderPos, idx, ok := p.advanceFromOrder()
	if !ok {
		p.pos = origPos
		p.queuedIdx = origQueuedIdx
		return Track{}, false
	}
	p.queuedIdx = -1
	p.pos = orderPos
	return cloneTrack(p.tracks[idx]), true
}

// PeekNext returns the next track without advancing the playlist position.
// Returns false when the next track can't be predicted (e.g., shuffle wrap).
func (p *Playlist) PeekNext() (Track, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tracks) == 0 {
		return Track{}, false
	}
	if idx, _, ok := p.nextPlayableQueued(); ok {
		return cloneTrack(p.tracks[idx]), true
	}
	if p.repeat == RepeatOne {
		idx := p.currentOrderTrackIndex()
		if p.isPlayable(idx) {
			return cloneTrack(p.tracks[idx]), true
		}
		return Track{}, false
	}
	if p.atShuffleWrap() {
		return Track{}, false
	}
	_, idx, ok := p.advanceFromOrder()
	if !ok {
		return Track{}, false
	}
	return cloneTrack(p.tracks[idx]), true
}

// Prev moves to the previous track, skipping unavailable tracks.
// Wraps around with RepeatAll.
func (p *Playlist) Prev() (Track, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.tracks) == 0 {
		return Track{}, false
	}
	origPos := p.pos
	origQueuedIdx := p.queuedIdx
	p.queuedIdx = -1

	if orderPos, idx, ok := p.lastPlayableOrderSlot(p.pos - 1); ok {
		p.pos = orderPos
		return cloneTrack(p.tracks[idx]), true
	}
	if p.repeat == RepeatAll {
		if orderPos, idx, ok := p.lastPlayableOrderSlot(len(p.order) - 1); ok {
			p.pos = orderPos
			return cloneTrack(p.tracks[idx]), true
		}
	}
	p.pos = origPos
	p.queuedIdx = origQueuedIdx
	if origQueuedIdx >= 0 {
		return cloneTrack(p.tracks[origQueuedIdx]), false
	}
	return cloneTrack(p.tracks[p.order[p.pos]]), false
}

// SetIndex sets the current position to the given track index.
func (p *Playlist) SetIndex(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queuedIdx = -1
	for pos, idx := range p.order {
		if idx == i {
			p.pos = pos
			return
		}
	}
}

// Queue adds a track to the play-next queue by its index.
func (p *Playlist) Queue(trackIdx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if trackIdx < 0 || trackIdx >= len(p.tracks) {
		return
	}
	p.queue = append(p.queue, trackIdx)
	if _, exists := p.queuePositions[trackIdx]; !exists {
		if p.queuePositions == nil {
			p.queuePositions = make(map[int]int)
		}
		p.queuePositions[trackIdx] = len(p.queue)
	}
}

// Dequeue removes a track from the queue. Returns true if it was found.
func (p *Playlist) Dequeue(trackIdx int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, idx := range p.queue {
		if idx == trackIdx {
			p.queue = slices.Delete(p.queue, i, i+1)
			p.rebuildQueuePositions()
			return true
		}
	}
	return false
}

// QueuePosition returns the 1-based position of a track in the queue,
// or 0 if the track is not queued.
func (p *Playlist) QueuePosition(trackIdx int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.queuePositions[trackIdx]
}

// QueueLen returns the number of tracks in the queue.
func (p *Playlist) QueueLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queue)
}

// QueueTracks returns copies of the tracks in queue order.
func (p *Playlist) QueueTracks() []Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cloneQueuedTracks(p.queue)
}

// QueueWindow returns copies of at most limit queued tracks starting at start.
func (p *Playlist) QueueWindow(start, limit int) []Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	start, end := windowBounds(len(p.queue), start, limit)
	if start == end {
		return nil
	}
	return p.cloneQueuedTracks(p.queue[start:end])
}

// ClearQueue removes all entries from the play-next queue.
func (p *Playlist) ClearQueue() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queue = nil
	p.queuePositions = nil
}

// Snapshot returns an independent copy of the playlist's mutable state.
func (p *Playlist) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Snapshot{
		tracks:    cloneTracks(p.tracks),
		order:     slices.Clone(p.order),
		pos:       p.pos,
		shuffle:   p.shuffle,
		repeat:    p.repeat,
		queue:     slices.Clone(p.queue),
		queuedIdx: p.queuedIdx,
	}
}

// Restore replaces the playlist's mutable state with a prior Snapshot.
func (p *Playlist) Restore(snapshot Snapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tracks = cloneTracks(snapshot.tracks)
	p.order = slices.Clone(snapshot.order)
	p.pos = snapshot.pos
	p.shuffle = snapshot.shuffle
	p.repeat = snapshot.repeat
	p.queue = slices.Clone(snapshot.queue)
	p.queuedIdx = snapshot.queuedIdx
	p.rebuildQueuePositions()
	p.rebuildBookmarkCount()
}

// RemoveQueueAt removes the entry at the given 0-based queue position.
func (p *Playlist) RemoveQueueAt(pos int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pos >= 0 && pos < len(p.queue) {
		p.queue = slices.Delete(p.queue, pos, pos+1)
		p.rebuildQueuePositions()
	}
}

// MoveQueue swaps the two entries at the given positions in the play-next queue.
func (p *Playlist) MoveQueue(from, to int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if from < 0 || from >= len(p.queue) || to < 0 || to >= len(p.queue) || from == to {
		return false
	}
	p.queue[from], p.queue[to] = p.queue[to], p.queue[from]
	p.rebuildQueuePositions()
	return true
}

// Move swaps the track at position from with the track at position to,
// updating order, queue, and position references so playback is unaffected.
// When shuffle is off, the visual order becomes the new playback order.
func (p *Playlist) Move(from, to int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if from < 0 || from >= len(p.tracks) || to < 0 || to >= len(p.tracks) || from == to {
		return false
	}

	// Swap in the tracks array (visual order).
	p.tracks[from], p.tracks[to] = p.tracks[to], p.tracks[from]

	// Update order: swap all references so they point at the moved tracks.
	for i, idx := range p.order {
		if idx == from {
			p.order[i] = to
		} else if idx == to {
			p.order[i] = from
		}
	}

	// Queue also references track indices.
	for i, idx := range p.queue {
		if idx == from {
			p.queue[i] = to
		} else if idx == to {
			p.queue[i] = from
		}
	}
	p.rebuildQueuePositions()
	if p.queuedIdx == from {
		p.queuedIdx = to
	} else if p.queuedIdx == to {
		p.queuedIdx = from
	}

	// When shuffle is off, reset order to [0,1,...,n] so playback follows
	// the new visual order rather than preserving the old sequence.
	if !p.shuffle {
		cur := p.order[p.pos]
		for i := range p.order {
			p.order[i] = i
		}
		p.pos = cur
	}

	return true
}

// Remove deletes the track at index idx, updating order, queue, and position
// references so playback state is preserved when possible. Returns true if a
// track was removed. If the removed track was the active one, the position
// stays at the same order slot so playback advances naturally on next.
func (p *Playlist) Remove(idx int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.tracks) {
		return false
	}
	if p.tracks[idx].Bookmark {
		p.bookmarkCount--
	}

	p.tracks = slices.Delete(p.tracks, idx, idx+1)

	removedOrderPos := -1
	newOrder := p.order[:0]
	for i, ord := range p.order {
		if ord == idx {
			removedOrderPos = i
			continue
		}
		if ord > idx {
			ord--
		}
		newOrder = append(newOrder, ord)
	}
	p.order = newOrder

	if removedOrderPos >= 0 && removedOrderPos < p.pos {
		p.pos--
	}
	if p.pos >= len(p.order) {
		p.pos = len(p.order) - 1
	}
	if p.pos < 0 {
		p.pos = 0
	}

	newQueue := p.queue[:0]
	for _, q := range p.queue {
		if q == idx {
			continue
		}
		if q > idx {
			q--
		}
		newQueue = append(newQueue, q)
	}
	p.queue = newQueue
	p.rebuildQueuePositions()

	switch {
	case p.queuedIdx == idx:
		p.queuedIdx = -1
	case p.queuedIdx > idx:
		p.queuedIdx--
	}

	return true
}

// SetTrack replaces the track at index i.
func (p *Playlist) SetTrack(i int, t Track) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= 0 && i < len(p.tracks) {
		if p.tracks[i].Bookmark != t.Bookmark {
			if t.Bookmark {
				p.bookmarkCount++
			} else {
				p.bookmarkCount--
			}
		}
		p.tracks[i] = cloneTrack(t)
	}
}

// Tracks returns an independent snapshot of all tracks in the playlist.
// Render paths should use TrackWindow to avoid copying the full playlist.
func (p *Playlist) Tracks() []Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneTracks(p.tracks)
}

// Track returns an independent copy of the track at index.
func (p *Playlist) Track(index int) (Track, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index < 0 || index >= len(p.tracks) {
		return Track{}, false
	}
	return cloneTrack(p.tracks[index]), true
}

// TrackWindow returns independent copies of at most limit tracks starting at
// start. It is intended for bounded render windows.
func (p *Playlist) TrackWindow(start, limit int) []Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	start, end := windowBounds(len(p.tracks), start, limit)
	if start == end {
		return nil
	}
	return cloneTracks(p.tracks[start:end])
}

// ToggleBookmark flips the Bookmark flag on the track at the given index.
func (p *Playlist) ToggleBookmark(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx >= 0 && idx < len(p.tracks) {
		p.tracks[idx].Bookmark = !p.tracks[idx].Bookmark
		if p.tracks[idx].Bookmark {
			p.bookmarkCount++
		} else {
			p.bookmarkCount--
		}
	}
}

// BookmarkCount returns the number of bookmarked tracks.
func (p *Playlist) BookmarkCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bookmarkCount
}

// ToggleShuffle enables or disables shuffle mode.
// Uses Fisher-Yates shuffle, preserving the current track at position 0.
func (p *Playlist) ToggleShuffle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shuffle = !p.shuffle
	if len(p.tracks) == 0 {
		return
	}
	if p.shuffle {
		p.doShuffle()
		return
	}
	cur := p.order[p.pos]
	p.order = make([]int, len(p.tracks))
	for i := range p.order {
		p.order[i] = i
	}
	p.pos = cur
}

func (p *Playlist) doShuffle() {
	cur := p.order[p.pos]
	others := make([]int, 0, len(p.tracks)-1)
	for i := range len(p.tracks) {
		if i != cur {
			others = append(others, i)
		}
	}
	for i := len(others) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		others[i], others[j] = others[j], others[i]
	}
	p.order = make([]int, 0, len(p.tracks))
	p.order = append(p.order, cur)
	p.order = append(p.order, others...)
	p.pos = 0
}

// CycleRepeat cycles through Off -> All -> One.
func (p *Playlist) CycleRepeat() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.repeat = (p.repeat + 1) % 3
}

// SetRepeat sets the repeat mode directly.
func (p *Playlist) SetRepeat(mode RepeatMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.repeat = mode
}

// Shuffled returns whether shuffle is enabled.
func (p *Playlist) Shuffled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shuffle
}

// Repeat returns the current repeat mode.
func (p *Playlist) Repeat() RepeatMode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.repeat
}
