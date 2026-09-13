// Package provider defines optional capability interfaces for music providers.
// Providers implement the base playlist.Provider interface and may additionally
// implement any of the interfaces here to expose extended features (browsing,
// searching, playback reporting, etc.). The UI discovers capabilities at runtime
// via type assertions.
package provider

import "github.com/bjarneo/cliamp/playlist"

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

// SearchResults carries multi-type search results from a MultiSearcher.
type SearchResults struct {
	Tracks    []playlist.Track
	Albums    []AlbumInfo
	Artists   []ArtistInfo
	Playlists []playlist.PlaylistInfo
}

// ArtistDetail carries everything a provider can supply for an artist
// profile page. Popular tracks carry ProviderMeta[MetaSpotifyPopularity]
// ("0"-"100") and ProviderMeta[MetaSpotifyLiked] ("true" when saved)
// where the provider supports them.
type ArtistDetail struct {
	Info        ArtistInfo
	Genres      []string
	Followers   int
	Popular     []playlist.Track
	Discography []AlbumInfo
}

// ProviderMeta key constants used across providers and the UI.
const (
	MetaNavidromeID = "navidrome.id"
	MetaJellyfinID  = "jellyfin.id"
	MetaEmbyID      = "emby.id"
	MetaNetEaseID   = "netease.id"
	MetaQobuzID     = "qobuz.id"

	MetaSpotifyPopularity = "spotify.popularity"
	MetaSpotifyLiked      = "spotify.liked"
)
