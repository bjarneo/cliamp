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
	Path   string
	Title  string
	Artist string
	Stream bool // true for HTTP/HTTPS URLs
}

// IsURL reports whether path is an HTTP or HTTPS URL.
func IsURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

// IsM3U reports whether the URL points to an M3U playlist file.
func IsM3U(path string) bool {
	if !IsURL(path) {
		return false
	}
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	ext := strings.ToLower(filepath.Ext(u.Path))
	return ext == ".m3u" || ext == ".m3u8"
}

// IsYTDL reports whether the URL points to a site supported by yt-dlp
// (SoundCloud, YouTube, Bandcamp, etc.).
func IsYTDL(path string) bool {
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
	switch host {
	case "soundcloud.com",
		"youtube.com", "youtu.be", "music.youtube.com",
		"bandcamp.com":
		return true
	}
	// Bandcamp artist subdomains (e.g. artist.bandcamp.com)
	if strings.HasSuffix(host, ".bandcamp.com") {
		return true
	}
	return false
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
// Supports "Artist - Title" format, otherwise uses the filename as title.
func TrackFromPath(path string) Track {
	if IsURL(path) {
		return trackFromURL(path)
	}
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	parts := strings.SplitN(name, " - ", 2)
	if len(parts) == 2 {
		return Track{Path: path, Artist: strings.TrimSpace(parts[0]), Title: strings.TrimSpace(parts[1])}
	}
	return Track{Path: path, Title: name}
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

// Add appends tracks to the playlist.
func (p *Playlist) Add(tracks ...Track) {
	start := len(p.tracks)
	p.tracks = append(p.tracks, tracks...)
	for i := start; i < len(p.tracks); i++ {
		p.order = append(p.order, i)
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
	return p.tracks[p.order[p.pos]], true
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
