// Package playlist manages an ordered track list with shuffle and repeat support.
package playlist

import (
	"math/rand"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
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
	Stream       bool   // true for HTTP/HTTPS URLs
	Realtime     bool   // true for real-time/live streams (e.g. radio)
	DurationSecs int    // known duration in seconds (0 = unknown)

	// ProviderMeta holds provider-specific key-value pairs.
	// Keys are namespaced by provider, e.g. "navidrome.id", "spotify.uri".
	ProviderMeta map[string]string
}

// Meta returns the value for a provider-specific metadata key, or "" if unset.
func (t Track) Meta(key string) string {
	if t.ProviderMeta == nil {
		return ""
	}
	return t.ProviderMeta[key]
}

// IsURL reports whether path is an HTTP or HTTPS URL, or a yt-dlp search protocol string.
func IsURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") ||
		strings.HasPrefix(path, "ytsearch:") || strings.HasPrefix(path, "ytsearch1:") ||
		strings.HasPrefix(path, "scsearch:") || strings.HasPrefix(path, "scsearch1:")
}

// IsM3U reports whether the path points to an M3U playlist file (URL or local).
func IsM3U(path string) bool {
	if IsURL(path) {
		u, err := url.Parse(path)
		if err != nil {
			return false
		}
		ext := strings.ToLower(filepath.Ext(u.Path))
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
		return strings.ToLower(filepath.Ext(u.Path)) == ".pls"
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
	if strings.HasPrefix(path, "ytsearch:") || strings.HasPrefix(path, "ytsearch1:") {
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
	if strings.HasPrefix(path, "ytsearch:") || strings.HasPrefix(path, "ytsearch1:") ||
		strings.HasPrefix(path, "scsearch:") || strings.HasPrefix(path, "scsearch1:") {
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
	ext := strings.ToLower(filepath.Ext(u.Path))
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

	// Extract filename from URL path
	base := filepath.Base(u.Path)
	if base != "" && base != "." && base != "/" {
		name := strings.TrimSuffix(base, filepath.Ext(base))
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
type Playlist struct {
	tracks    []Track
	order     []int // indices into tracks, shuffled or sequential
	pos       int   // current position in order
	shuffle   bool
	repeat    RepeatMode
	queue     []int // track indices queued to play next
	queuedIdx int   // track index currently playing from queue, -1 if none
}

// New creates an empty Playlist.
func New() *Playlist {
	return &Playlist{queuedIdx: -1}
}

// Replace clears the playlist and loads the given tracks, resetting
// position, queue, and shuffle order.
func (p *Playlist) Replace(tracks []Track) {
	p.tracks = tracks
	p.order = make([]int, len(tracks))
	for i := range tracks {
		p.order[i] = i
	}
	p.pos = 0
	p.queue = nil
	p.queuedIdx = -1
	if p.shuffle && len(tracks) > 0 {
		p.doShuffle()
	}
}

// Add appends tracks to the playlist.
func (p *Playlist) Add(tracks ...Track) {
	start := len(p.tracks)
	p.tracks = append(p.tracks, tracks...)
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
func (p *Playlist) Len() int { return len(p.tracks) }

// Current returns the currently selected track and its index.
func (p *Playlist) Current() (Track, int) {
	if len(p.tracks) == 0 {
		return Track{}, -1
	}
	if p.queuedIdx >= 0 {
		return p.tracks[p.queuedIdx], p.queuedIdx
	}
	idx := p.order[p.pos]
	return p.tracks[idx], idx
}

// Index returns the track index of the current position.
func (p *Playlist) Index() int {
	if len(p.order) == 0 {
		return -1
	}
	if p.queuedIdx >= 0 {
		return p.queuedIdx
	}
	return p.order[p.pos]
}

// Next advances to the next track. Returns false if at end with repeat off.
// Queued tracks are played first before resuming normal order.
func (p *Playlist) Next() (Track, bool) {
	if len(p.tracks) == 0 {
		return Track{}, false
	}
	// Play from queue first
	if len(p.queue) > 0 {
		idx := p.queue[0]
		p.queue = p.queue[1:]
		p.queuedIdx = idx
		return p.tracks[idx], true
	}
	p.queuedIdx = -1
	if p.repeat == RepeatOne {
		return p.tracks[p.order[p.pos]], true
	}
	if p.pos+1 < len(p.order) {
		p.pos++
		return p.tracks[p.order[p.pos]], true
	}
	if p.repeat == RepeatAll {
		p.pos = 0
		if p.shuffle {
			p.doShuffle()
		}
		return p.tracks[p.order[p.pos]], true
	}
	return Track{}, false
}

// PeekNext returns the next track without advancing the playlist position.
// Returns false when the next track can't be predicted (e.g., shuffle wrap).
func (p *Playlist) PeekNext() (Track, bool) {
	if len(p.tracks) == 0 {
		return Track{}, false
	}
	// Queued tracks take priority
	if len(p.queue) > 0 {
		return p.tracks[p.queue[0]], true
	}
	if p.repeat == RepeatOne {
		idx := p.order[p.pos]
		if p.queuedIdx >= 0 {
			idx = p.queuedIdx
		}
		return p.tracks[idx], true
	}
	if p.pos+1 < len(p.order) {
		return p.tracks[p.order[p.pos+1]], true
	}
	if p.repeat == RepeatAll && !p.shuffle {
		return p.tracks[p.order[0]], true
	}
	// RepeatAll+shuffle: can't predict after re-shuffle; RepeatOff at end
	return Track{}, false
}

// Prev moves to the previous track. Wraps around with RepeatAll.
func (p *Playlist) Prev() (Track, bool) {
	p.queuedIdx = -1
	if len(p.tracks) == 0 {
		return Track{}, false
	}
	if p.pos > 0 {
		p.pos--
		return p.tracks[p.order[p.pos]], true
	}
	if p.repeat == RepeatAll {
		p.pos = len(p.order) - 1
		return p.tracks[p.order[p.pos]], true
	}
	// At the beginning with RepeatOff — return false for consistency with Next().
	return p.tracks[p.order[p.pos]], false
}

// SetIndex sets the current position to the given track index.
func (p *Playlist) SetIndex(i int) {
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
	if trackIdx >= 0 && trackIdx < len(p.tracks) {
		p.queue = append(p.queue, trackIdx)
	}
}

// Dequeue removes a track from the queue. Returns true if it was found.
func (p *Playlist) Dequeue(trackIdx int) bool {
	for i, idx := range p.queue {
		if idx == trackIdx {
			p.queue = slices.Delete(p.queue, i, i+1)
			return true
		}
	}
	return false
}

// QueuePosition returns the 1-based position of a track in the queue,
// or 0 if the track is not queued.
func (p *Playlist) QueuePosition(trackIdx int) int {
	for i, idx := range p.queue {
		if idx == trackIdx {
			return i + 1
		}
	}
	return 0
}

// QueueLen returns the number of tracks in the queue.
func (p *Playlist) QueueLen() int { return len(p.queue) }

// QueueTracks returns copies of the tracks in queue order.
func (p *Playlist) QueueTracks() []Track {
	out := make([]Track, len(p.queue))
	for i, idx := range p.queue {
		out[i] = p.tracks[idx]
	}
	return out
}

// ClearQueue removes all entries from the play-next queue.
func (p *Playlist) ClearQueue() { p.queue = nil }

// RemoveQueueAt removes the entry at the given 0-based queue position.
func (p *Playlist) RemoveQueueAt(pos int) {
	if pos >= 0 && pos < len(p.queue) {
		p.queue = slices.Delete(p.queue, pos, pos+1)
	}
}

// MoveQueue swaps two adjacent entries in the play-next queue by position.
func (p *Playlist) MoveQueue(from, to int) bool {
	if from < 0 || from >= len(p.queue) || to < 0 || to >= len(p.queue) || from == to {
		return false
	}
	p.queue[from], p.queue[to] = p.queue[to], p.queue[from]
	return true
}

// Move swaps the track at position from with the track at position to,
// updating order, queue, and position references so playback is unaffected.
// When shuffle is off, the visual order becomes the new playback order.
func (p *Playlist) Move(from, to int) bool {
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

// SetTrack replaces the track at index i.
func (p *Playlist) SetTrack(i int, t Track) {
	if i >= 0 && i < len(p.tracks) {
		p.tracks[i] = t
	}
}

// Tracks returns all tracks in the playlist.
func (p *Playlist) Tracks() []Track { return p.tracks }

// ToggleShuffle enables or disables shuffle mode.
// Uses Fisher-Yates shuffle, preserving the current track at position 0.
func (p *Playlist) ToggleShuffle() {
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
	p.repeat = (p.repeat + 1) % 3
}

// Shuffled returns whether shuffle is enabled.
func (p *Playlist) Shuffled() bool { return p.shuffle }

// Repeat returns the current repeat mode.
func (p *Playlist) Repeat() RepeatMode { return p.repeat }
