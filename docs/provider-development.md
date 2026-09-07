# Creating a Provider

Put providers in `external/<name>/`, for example `external/jellyfin/`. A
provider is a Go package. It implements the base `playlist.Provider` interface
and can implement capability interfaces from the `provider/` package. The UI
uses type assertions at run time to detect capabilities and enable features.

Use these providers as examples:

- `external/navidrome/`: Subsonic API, browsing, scrobbling
- `external/plex/`: Plex Media Server, search, album tracks
- `external/spotify/`: Spotify, search, playlist management, custom streaming
- `external/mixcloud/`: public catalog, browse-entry shortcuts, creator jumps,
  genre search and local genre favorites
- `external/radio/`: internet radio, favorites
- `external/local/`: local TOML playlist files
- `external/audiobookshelf/`: Audiobookshelf, sectioned playlists, resume

## Base Interface (required)

Every provider must implement `playlist.Provider`:

```go
type Provider interface {
    Name() string
    Playlists() ([]playlist.PlaylistInfo, error)
    Tracks(playlistID string) ([]Track, error)
}
```

This interface provides a name, a playlist list, and tracks for a playlist. It
is enough for basic playback.

## Capability Interfaces (optional)

Implement any of these interfaces to enable more UI features. All interfaces
are in `provider/interfaces.go`.

| Interface | What it enables | Methods |
|---|---|---|
| `Searcher` | Track search overlay | `SearchTracks(ctx, query, limit)` |
| `ArtistBrowser` | Hierarchical artist browsing | `Artists()`, `ArtistAlbums(id)` |
| `TrackArtistResolver` | Jump from a highlighted provider track to its artist/creator with `N` | `ArtistForTrack(track)` |
| `BrowseEntryProvider` | Add non-playable shortcuts into the provider playlist pane | `BrowseEntries()`; each entry can set `AfterID`, `AfterSection`, and `OpenInPlaylist` |
| `GenreBrowser` | Hierarchical category browsing with provider-defined sort views | `Genres()`, `GenreSortTypes()`, `GenreTracks(genreID, sortType)` |
| `GenreBrowseRouter` | Route multiple provider-pane entries to distinct category catalogues | `GenreBrowserFor(entryID)` |
| `GenreFavoriteToggler` | Favorite/unfavorite categories with `f` in the genre browser | `ToggleGenreFavorite(genreID)` |
| `GenreSearcher` | Search beyond the initially loaded category catalogue | `SearchGenres(ctx, query, limit)` |
| `AlbumBrowser` | Paginated album browsing with sort | `AlbumList(sort, offset, size)`, `AlbumSortTypes()` |
| `AlbumTrackLoader` | Album track listing | `AlbumTracks(albumID)` |
| `PlaybackReporter` | Playback reporting at track start and finish | `CanReportPlayback(track)`, `ReportNowPlaying(track, position, canSeek) error`, `ReportScrobble(track, elapsed, duration, canSeek) error` |
| `PlaylistWriter` | Add track to playlist | `AddTrackToPlaylist(ctx, playlistID, track)` |
| `PlaylistCreator` | Create new playlist | `CreatePlaylist(ctx, name)` |
| `PlaylistDeleter` | Remove playlists/tracks | `DeletePlaylist(name)`, `RemoveTrack(name, index)` |
| `CustomStreamer` | Custom URI decode pipeline | `URISchemes()`, `NewStreamer(uri)` |
| `FavoriteToggler` | Favorite toggling | `ToggleFavorite(id)` |
| `Closer` | Cleanup on shutdown | `Close()` |
| `Authenticator` | Interactive sign-in flow | `Authenticate() error` (in `playlist` package) |
| `ResumeTarget` | Server-side resume position | `ResumeTarget(playlistID, tracks)` |
| `TrackPosition` | Server-side position for one track, read on every play | `CanTrackPosition(track)`, `TrackPosition(track)` |
| `ProgressReporter` | Interim position updates while playing, in addition to `PlaybackReporter`'s start/finish reports | `ReportProgress(track, position) error` |
| `BrowseLabeler` | Relabel the browse overlay's two levels (e.g. Authors/Books instead of Artists/Albums) | `BrowseLabels()` |

## Steps

### 1. Create the package

Create `external/<name>/provider.go`:

```go
package jellyfin

import (
    "context"

    "github.com/bjarneo/cliamp/playlist"
    "github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
    _ provider.Searcher         = (*Provider)(nil)
    _ provider.AlbumTrackLoader = (*Provider)(nil)
)

type Provider struct {
    baseURL string
    token   string
}

func New(baseURL, token string) *Provider {
    return &Provider{baseURL: baseURL, token: token}
}

func (p *Provider) Name() string { return "Jellyfin" }

func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
    // Fetch playlists from your server's API.
    return nil, nil
}

func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
    // Fetch tracks for a playlist.
    return nil, nil
}

func (p *Provider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
    // Search the server's catalog.
    return nil, nil
}

func (p *Provider) AlbumTracks(albumID string) ([]playlist.Track, error) {
    // Fetch tracks for an album.
    return nil, nil
}
```

### 2. Return tracks

When you create `playlist.Track` values:

- **`Path`**: Set the playable URL or file path. For HTTP streams, use a full URL.
  For a custom URI scheme, for example `spotify:track:xxx`, implement `CustomStreamer`.
- **`Stream: true`**: Set this for HTTP URLs. The player then uses the streaming
  pipeline.
- **`ProviderMeta`**: Add provider-specific metadata in a string map with
  namespaced keys. cliamp uses this metadata for features such as scrobbling:

```go
playlist.Track{
    Path:         "https://my-server/stream/123",
    Title:        "Song Title",
    Artist:       "Artist Name",
    Stream:       true,
    ProviderMeta: map[string]string{"jellyfin.id": "123"},
}
```

### 3. Add configuration

Add a configuration struct to `config/config.go`:

```go
type JellyfinConfig struct {
    URL   string `toml:"url"`
    Token string `toml:"token"`
}
```

Add the field to the top-level `Config` struct. Then add a TOML section:

```toml
[jellyfin]
url = "https://jellyfin.example.com"
token = "your-api-key"
```

### 4. Register in main.go

Register the provider in the `run()` function in `main.go`:

```go
if cfg.Jellyfin.URL != "" && cfg.Jellyfin.Token != "" {
    jfProv := jellyfin.New(cfg.Jellyfin.URL, cfg.Jellyfin.Token)
    providers = append(providers, ui.ProviderEntry{
        Key: "jellyfin", Name: "Jellyfin", Provider: jfProv,
    })
}
```

If the provider needs a custom audio pipeline, such as Spotify `spotify:` URIs,
register a streamer factory:

```go
if cs, ok := myProv.(provider.CustomStreamer); ok {
    for _, scheme := range cs.URISchemes() {
        p.RegisterStreamerFactory(scheme, cs.NewStreamer)
    }
}
```

If the provider needs the buffered download pipeline for stream URLs, such as
Navidrome Subsonic endpoints, register a URL matcher:

```go
p.RegisterBufferedURLMatcher(jellyfin.IsStreamURL)
```

### 5. Add a `--provider` flag value

In the `main.go` help text, add the provider key to the `--provider` line. Users
can then set it as the default.

## What the UI Does Automatically

Do not change the UI code. The UI uses the interfaces your provider implements
to do the following:

- Show the browse overlay (`N`) when any registered provider implements
  `ArtistBrowser`, `AlbumBrowser`, or `GenreBrowser`
- Add `BrowseEntryProvider` routes to the provider pane without exposing them
  as playable playlists to IPC or other provider users. `AfterID` puts a route
  after one list item. `AfterSection` is its section fallback. cliamp omits a
  route that extends a missing section. `OpenInPlaylist` replaces the main
  playlist with its non-empty final result instead of opening the browser track
  screen
- Jump from a highlighted track to its artist or creator when the source
  provider implements both `TrackArtistResolver` and `ArtistBrowser`
- Add genre lists and sort views for `GenreBrowser`, the `f` action for
  `GenreFavoriteToggler`, and provider-side category search for `GenreSearcher`
- Route multiple `BrowseGenres` pane entries to separate catalogues when the
  provider implements `GenreBrowseRouter`
- Show the search overlay ("F") when a registered provider implements `Searcher`
- Enable add-to-playlist in search results when the searched provider implements `PlaylistWriter`
- Report playback at track start and finish when `PlaybackReporter` is implemented. Log failures that the provider returns.
- Run interactive authentication on first use when `Authenticator` is implemented
- Put the cursor on the active track and start at its stored position when `ResumeTarget` is implemented
- Send the listening position every 15 seconds while a track plays when `ProgressReporter` is implemented
- Set the browse overlay levels to your own nouns when `BrowseLabeler` is implemented
- Call `Close()` during shutdown when `Closer` is implemented

The "N" and "F" shortcuts work with any active provider. They use the first
registered provider with the required capability.
