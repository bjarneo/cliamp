// Package provider defines optional capability interfaces for music providers.
// Providers implement the base playlist.Provider interface and may additionally
// implement any of the interfaces here to expose extended features (browsing,
// searching, playback reporting, etc.). The UI discovers capabilities at runtime
// via type assertions.
package provider

import "strconv"

// ArtistInfo describes an artist in a provider's catalog.
type ArtistInfo struct {
	ID         string
	Name       string
	AlbumCount int
}

// AlbumInfo describes an album in a provider's catalog.
type AlbumInfo struct {
	ID         string
	Name       string
	Artist     string
	ArtistID   string
	Year       int
	TrackCount int
	Genre      string
	// Restricted is presentation metadata for provider items that may require
	// account access. It must not be folded into Name or persisted metadata.
	Restricted bool
}

// GenreInfo describes a provider category and whether it is pinned as a
// favorite. Group is optional (for example "Music" or "Talk").
type GenreInfo struct {
	ID       string
	Name     string
	Group    string
	Favorite bool
}

// SortType describes one sort option for album listing.
type SortType struct {
	ID    string // e.g. "alphabeticalByName"
	Label string // e.g. "By Name"
}

// YearFromDate extracts the year from a "YYYY-MM-DD" (or bare "YYYY") date
// string, as returned by streaming-service APIs. Returns 0 when the string
// does not start with a 4-digit year.
func YearFromDate(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return y
}

// ProviderMeta key constants used across providers and the UI.
const (
	MetaNavidromeID = "navidrome.id"
	MetaJellyfinID  = "jellyfin.id"
	MetaEmbyID      = "emby.id"
	MetaNetEaseID   = "netease.id"
	MetaQobuzID     = "qobuz.id"
	MetaTidalID     = "tidal.id"
	MetaMixcloudKey = "mixcloud.key"
	// MetaMixcloudCreator is the profile username that owns a Mixcloud show.
	MetaMixcloudCreator = "mixcloud.creator"
	// MetaMixcloudExclusive marks a show that Mixcloud may restrict to
	// signed-in users or subscribers. The viewer's entitlement is resolved
	// only during playback, so it is informational rather than Unplayable.
	MetaMixcloudExclusive = "mixcloud.exclusive"

	MetaAudiobookshelfID      = "audiobookshelf.id"
	MetaAudiobookshelfEpisode = "audiobookshelf.episode"
	MetaAudiobookshelfOffset  = "audiobookshelf.offset"
	MetaAudiobookshelfTotal   = "audiobookshelf.total"
)
