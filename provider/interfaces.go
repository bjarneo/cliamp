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

// TrackArtistResolver lets the UI jump from a provider-originated track to
// that track's artist in the hierarchical browser. It should return false for
// tracks the provider does not recognize.
type TrackArtistResolver interface {
	ArtistForTrack(track playlist.Track) (ArtistInfo, bool)
}

// BrowseMode identifies a useful entry point into the hierarchical provider
// browser. Providers can expose these routes alongside their playlist pane
// through BrowseEntryProvider without representing them as playable lists.
type BrowseMode uint8

const (
	BrowseAlbums BrowseMode = iota + 1
	BrowseArtists
	BrowseArtistAlbums
	BrowseGenres
)

// BrowseEntry is a provider-pane shortcut into the hierarchical browser.
type BrowseEntry struct {
	ID      string
	Name    string
	Section string
	Mode    BrowseMode
	// AfterID places this entry group immediately after a provider list item.
	// AfterSection is used as a fallback when the item is absent.
	AfterID string
	// AfterSection places this entry group after the last provider list in the
	// named section. If that section is absent and Section == AfterSection, the
	// entry is omitted rather than creating a section with no provider content.
	// Other entries with a missing anchor remain at the top.
	AfterSection string
	// OpenInPlaylist makes the route's final track result replace the main
	// playlist and close the browser instead of opening its track screen.
	OpenInPlaylist bool
}

// BrowseEntryProvider is implemented by providers that want their principal
// hierarchical browse routes advertised in the provider playlist pane.
type BrowseEntryProvider interface {
	BrowseEntries() []BrowseEntry
}

// GenreBrowser is implemented by providers that expose a category catalogue.
type GenreBrowser interface {
	Genres() ([]GenreInfo, error)
	GenreSortTypes() []SortType
	GenreTracks(genreID, sortType string) ([]playlist.Track, error)
}

// GenreFavoriteToggler is implemented by genre browsers that can persist
// favorite state in provider-specific configuration or on the remote service.
type GenreFavoriteToggler interface {
	ToggleGenreFavorite(genreID string) (favorite bool, err error)
}

// GenreSearcher extends GenreBrowser with provider-side catalogue search.
// It is useful when the browsable category list is only a curated subset of
// the provider's full tag or genre catalogue.
type GenreSearcher interface {
	GenreBrowser
	SearchGenres(ctx context.Context, query string, limit int) ([]GenreInfo, error)
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
	// ReportNowPlaying and ReportScrobble return the failure so the caller can
	// record it; reporting is best-effort and never blocks playback.
	ReportNowPlaying(track playlist.Track, position time.Duration, canSeek bool) error
	ReportScrobble(track playlist.Track, elapsed, duration time.Duration, canSeek bool) error
}

// ProgressReporter is implemented by providers that track listening position
// server-side and accept interim updates while a track plays, not only at its
// start and end.
type ProgressReporter interface {
	PlaybackReporter
	// ReportProgress sends an interim position update for a playing track.
	ReportProgress(track playlist.Track, position time.Duration) error
}

// TrackPosition is implemented by providers that can report where a single
// track should resume from.
type TrackPosition interface {
	// CanTrackPosition reports whether track belongs to this provider.
	CanTrackPosition(track playlist.Track) bool
	// TrackPosition returns the saved position for track, or 0 to start over.
	TrackPosition(track playlist.Track) time.Duration
}

// ResumeTarget is implemented by providers that track listening position
// server-side and can point the UI at where to continue.
type ResumeTarget interface {
	// ResumeTarget returns the index within tracks to continue from and the
	// offset into that track. It returns (0, 0) when there is no stored
	// position.
	ResumeTarget(playlistID string, tracks []playlist.Track) (index int, offset time.Duration)
}

// BrowseLabeler is implemented by providers whose catalog is not music, so the
// browse overlay can use the right vocabulary.
type BrowseLabeler interface {
	// BrowseLabels returns the singular nouns for the artist and album levels,
	// e.g. ("Author", "Book").
	BrowseLabels() (artist, album string)
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

// PlaylistDocumenter is implemented by providers that can hand back a
// playlist's raw TOML document and restore it verbatim. Callers use this to
// snapshot a playlist before a destructive operation so sections the plain
// track list cannot represent (e.g. [[dir]] directory sources) survive an
// undo.
type PlaylistDocumenter interface {
	PlaylistDocument(name string) ([]byte, error)
	RestorePlaylistDocument(name string, data []byte) error
}

// BookmarkSetter is implemented by providers that support toggling
// track bookmarks and persisting them.
type BookmarkSetter interface {
	SetBookmark(playlistName string, idx int) error
	SetBookmarkByPath(playlistName string, path string) error
}

// PlaylistDirSourceManager is implemented by providers whose playlists can
// reference directory sources that are re-scanned on each load. The local
// TOML provider implements this for its [[dir]] sections; other providers
// leave it unimplemented and the UI hides directory-source controls.
type PlaylistDirSourceManager interface {
	DirSources(name string) ([]playlist.DirSource, error)
	AddDirSource(name, dir string) (bool, error)
	RemoveDirSource(name, dir string) error
	SetDirRecursive(name, dir string, recursive bool) error
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

// RadioStatsLoader is implemented by radio providers that expose aggregate
// listener statistics for their built-in stations.
type RadioStatsLoader interface {
	RadioStats() (RadioStats, error)
}

// RadioStats summarizes listener activity across a radio provider's stations.
type RadioStats struct {
	TotalSessions    int                          `json:"total_sessions"`
	TotalListenHours float64                      `json:"total_listen_hours"`
	PeakListeners    int                          `json:"peak_listeners"`
	Stations         map[string]RadioStationStats `json:"stations"`
}

// RadioStationStats summarizes listener activity for one radio station.
type RadioStationStats struct {
	TotalSessions    int     `json:"total_sessions"`
	TotalListenHours float64 `json:"total_listen_hours"`
	PeakListeners    int     `json:"peak_listeners"`
	ActiveListeners  int     `json:"active_listeners"`
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

// FavoritesManager is implemented by providers that support a cross-playlist
// favorites virtual playlist. The UI uses this to toggle favorites from the
// track list without going through the per-playlist write path.
type FavoritesManager interface {
	// ToggleFavorite toggles the given track in the favorites store.
	// Returns true when the track is now favorited after the call.
	ToggleFavorite(track playlist.Track) (bool, error)
	// IsFavorited reports whether the given path is in the favorites store.
	IsFavorited(path string) bool
	// FavoritesCount returns the number of favorited tracks.
	FavoritesCount() int
}
