package provider

import (
	"context"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/playlist"
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
	DefaultAlbumSort() string
}

// AlbumSortSaver is implemented by providers that persist album sort changes.
type AlbumSortSaver interface {
	SaveAlbumSort(sortType string) error
}

// AlbumTrackLoader is implemented by providers that can return the tracks
// of a specific album (as opposed to a playlist).
type AlbumTrackLoader interface {
	AlbumTracks(albumID string) ([]playlist.Track, error)
}

// PlaybackReporter is implemented by providers that accept now-playing and
// playback-completion reports for tracks they originated.
type PlaybackReporter interface {
	CanReportPlayback(track playlist.Track) bool
	ReportNowPlaying(track playlist.Track, position time.Duration, canSeek bool)
	ReportScrobble(track playlist.Track, elapsed, duration time.Duration, canSeek bool)
}

// PlaylistWriter is implemented by providers that support adding tracks
// to existing playlists.
type PlaylistWriter interface {
	AddTrackToPlaylist(ctx context.Context, playlistID string, track playlist.Track) error
}

// PlaylistBatchWriter is implemented by providers that support adding multiple
// tracks to existing playlists in one operation.
type PlaylistBatchWriter interface {
	AddTracksToPlaylist(ctx context.Context, playlistID string, tracks []playlist.Track) (added, skipped int, err error)
}

// PlaylistSaver is implemented by providers that can overwrite a playlist's
// complete ordered track list.
type PlaylistSaver interface {
	SavePlaylist(name string, tracks []playlist.Track) error
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

// PlaylistRenamer is implemented by providers that support renaming playlists.
type PlaylistRenamer interface {
	RenamePlaylist(oldName, newName string) error
}

// BookmarkSetter is implemented by providers that support toggling
// track bookmarks and persisting them.
type BookmarkSetter interface {
	SetBookmark(playlistName string, idx int) error
	SetBookmarkByPath(playlistName string, path string) error
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

// CatalogLoader is implemented by providers that support lazy-loading
// catalog pages from an external source (e.g. Radio Browser API).
type CatalogLoader interface {
	// LoadCatalogPage fetches the next page of catalog entries starting at
	// offset. Returns the number of items added and any error.
	LoadCatalogPage(offset, limit int) (added int, err error)
}

// CatalogSearcher is implemented by providers that support server-side
// catalog search (e.g. radio station search via an API).
type CatalogSearcher interface {
	// SearchCatalog performs a server-side search. Results are reflected
	// in the next Playlists() call.
	SearchCatalog(query string) (int, error)
	ClearSearch()
	IsSearching() bool
}

// SectionedList is implemented by providers whose playlist list has
// logical sections (e.g. local stations, favorites, catalog).
type SectionedList interface {
	// IDPrefix returns the section prefix for a playlist ID (e.g. "f", "c", "s").
	IDPrefix(id string) string
	// IsFavoritableID reports whether the given ID can be favorited.
	IsFavoritableID(id string) bool
}

// Closer is implemented by providers that hold resources (sessions,
// connections) that should be released on shutdown.
type Closer interface {
	Close()
}

// TrackPager is implemented by providers that can serve playlist tracks in
// pages, enabling incremental loading of large playlists.
type TrackPager interface {
	// TracksPage returns one page of tracks for the given playlist ID and
	// the total number of tracks. offset and limit are hints; providers may
	// clamp limit to their own page size.
	TracksPage(id string, offset, limit int) (tracks []playlist.Track, total int, err error)
}

// MultiSearcher is implemented by providers that can search across multiple
// entity types (tracks, albums, artists, playlists) in one query.
type MultiSearcher interface {
	SearchAll(ctx context.Context, query string, limit int) (SearchResults, error)
}

// ArtistDetailLoader is implemented by providers that can return a rich
// artist profile (header info plus popular tracks and discography).
type ArtistDetailLoader interface {
	ArtistDetail(artistID string) (ArtistDetail, error)
}

// TrackLiker is implemented by providers that support saving and removing
// individual tracks in the user's library (e.g. Spotify liked songs).
type TrackLiker interface {
	// ToggleTrackLike flips the saved state of the track and returns the
	// new state. Implementations resolve the current state themselves.
	ToggleTrackLike(ctx context.Context, track playlist.Track) (liked bool, err error)
}

// Recommender is implemented by providers that can recommend additional
// tracks related to the current queue (e.g. Smart Shuffle).
type Recommender interface {
	RecommendTracks(ctx context.Context, seed []playlist.Track, limit int) ([]playlist.Track, error)
}

// PlaylistFollower is implemented by providers that support following and
// unfollowing playlists by ID. For playlists owned by the user, unfollowing
// typically deletes the playlist.
type PlaylistFollower interface {
	FollowPlaylistByID(ctx context.Context, playlistID string) error
	UnfollowPlaylistByID(ctx context.Context, playlistID string) error
}

// ArtistFollower is implemented by providers that support following and
// unfollowing artists by ID.
type ArtistFollower interface {
	FollowArtist(ctx context.Context, artistID string) error
	UnfollowArtist(ctx context.Context, artistID string) error
}

// PlaylistTrackRemover is implemented by providers that support removing a
// track from a playlist. position is the zero-based index in the caller's
// track list (used for caller-side bookkeeping); implementations should
// resolve the track's remote identity from track itself rather than trusting
// position, which may not match the provider's raw item positions when the
// caller's list filters out unplayable items.
type PlaylistTrackRemover interface {
	RemoveTrackFromPlaylist(ctx context.Context, playlistID string, position int, track playlist.Track) error
}

// RemotePlaylistRenamer is implemented by providers that support renaming
// playlists addressed by ID (as opposed to by name, like PlaylistRenamer).
type RemotePlaylistRenamer interface {
	RenamePlaylistByID(ctx context.Context, playlistID, newName string) error
}
