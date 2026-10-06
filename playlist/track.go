package playlist

import (
	"net/url"
	pathpkg "path"
	"strings"
)

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
	// Restricted marks a track that its provider may refuse to play, such as
	// an exclusive Mixcloud show. The UI marks it, and playback still tries.
	Restricted bool

	Unplayable bool // true when the track is known not playable in the current playback context

	DirSourced bool // true when expanded from a [[dir]] playlist section; re-derived on load, never persisted

	EmbeddedLyrics string // embedded lyrics from local file tags, when present
	AlbumArtURL    string // file:// URL for cached embedded album art, when present

	// ProviderMeta holds provider-specific key-value pairs.
	// Keys are namespaced by provider, e.g. "navidrome.id", "jellyfin.id".
	ProviderMeta map[string]string

	// Runtime-only provenance shared by tracks selected from the same source.
	playbackContext      []Track
	playbackContextIndex int
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

// ProviderMeta keys shared across providers. Unlike the provider-namespaced
// keys (e.g. "navidrome.id"), these describe what a Track stands for, so the
// UI can handle it without knowing which provider produced it.
const (
	// MetaKind marks a Track that is not a plain playable track.
	MetaKind = "kind"
	// MetaKindAlbum is the MetaKind value for an album placeholder: a search
	// result standing for a whole album, expanded to its tracks when chosen.
	MetaKindAlbum = "album"
	// MetaAlbumID carries the provider-side album id of an album placeholder.
	MetaAlbumID = "albumID"
)

// IsAlbum reports whether the track is an album placeholder rather than
// something playable on its own. Callers must expand it with the provider's
// AlbumTracks before handing it to the player.
func (t Track) IsAlbum() bool {
	return t.ProviderMeta[MetaKind] == MetaKindAlbum
}

// AlbumID returns the provider-side album id of an album placeholder, or ""
// when the track is not one.
func (t Track) AlbumID() string {
	if !t.IsAlbum() {
		return ""
	}
	return t.ProviderMeta[MetaAlbumID]
}
