// Package provider defines optional capability interfaces for music providers.
// Providers implement the base playlist.Provider interface and may additionally
// implement any of the interfaces here to expose extended features (browsing,
// searching, playback reporting, etc.). The UI discovers capabilities at runtime
// via type assertions.
package provider

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

// ProviderMeta key constants used across providers and the UI.
const (
	MetaNavidromeID = "navidrome.id"
	MetaJellyfinID  = "jellyfin.id"
)
