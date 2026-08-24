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
	MetaLyrionID    = "lyrion.id"

	MetaAudiobookshelfID      = "audiobookshelf.id"
	MetaAudiobookshelfEpisode = "audiobookshelf.episode"
	MetaAudiobookshelfOffset  = "audiobookshelf.offset"
	MetaAudiobookshelfTotal   = "audiobookshelf.total"
)
