package provider

import (
	"context"
	"time"

	"github.com/gopxl/beep/v2"

	"cliamp/playlist"
)

// Searcher is implemented by providers that support searching for tracks.
type Searcher interface {
	SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error)
}

// ArtistBrowser is implemented by providers that support listing artists
// and their albums.
type ArtistBrowser interface {
	Artists() ([]ArtistInfo, error)
	ArtistAlbums(artistID string) ([]AlbumInfo, error)
}

// AlbumBrowser is implemented by providers that support paginated album
// listing with configurable sort order.
type AlbumBrowser interface {
	AlbumList(sortType string, offset, size int) ([]AlbumInfo, error)
	AlbumSortTypes() []SortType
}

// AlbumTrackLoader is implemented by providers that can return the tracks
// of a specific album (as opposed to a playlist).
type AlbumTrackLoader interface {
	AlbumTracks(albumID string) ([]playlist.Track, error)
}

// Scrobbler is implemented by providers that report playback to an
// external service (e.g. Navidrome/Subsonic, Last.fm).
type Scrobbler interface {
	Scrobble(track playlist.Track, submission bool)
}

// PlaylistWriter is implemented by providers that support adding tracks
// to existing playlists.
type PlaylistWriter interface {
	AddTrackToPlaylist(ctx context.Context, playlistID string, track playlist.Track) error
}

// PlaylistCreator is implemented by providers that support creating new
// playlists.
type PlaylistCreator interface {
	CreatePlaylist(ctx context.Context, name string) (string, error)
}

// PlaylistDeleter is implemented by providers that support removing
// playlists and individual tracks.
type PlaylistDeleter interface {
	DeletePlaylist(name string) error
	RemoveTrack(name string, index int) error
}

// CustomStreamer is implemented by providers that need a custom audio
// decode path for non-standard URI schemes (e.g. spotify:track:xxx).
type CustomStreamer interface {
	// URISchemes returns the URI prefixes this provider handles.
	URISchemes() []string
	// NewStreamer creates a decoder for the given URI.
	NewStreamer(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error)
}

// FavoriteToggler is implemented by providers that support marking items
// as favorites (e.g. radio station favorites).
type FavoriteToggler interface {
	ToggleFavorite(id string) (added bool, name string, err error)
}

// Closer is implemented by providers that hold resources (sessions,
// connections) that should be released on shutdown.
type Closer interface {
	Close()
}
