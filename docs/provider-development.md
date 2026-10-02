# Creating a Provider

Put providers in `external/<name>/`, for example `external/plex/`. A provider
is a Go package. It implements the base `playlist.Provider` interface and can
implement capability interfaces from the `provider/` package. The UI uses type
assertions at run time to detect capabilities and enable features.

Use these providers as examples:

- `external/navidrome/`: Subsonic API, browsing, scrobbling, starred songs
- `external/plex/`: Plex Media Server, search, album tracks
- `external/jellyfin/` and `external/emby/`: two small adapters over the shared
  client in `internal/embyapi/`
- `external/spotify/`: Spotify, browser sign-in, playlist management, custom
  streaming, synced lyrics
- `external/qobuz/` and `external/tidal/`: provider URIs that resolve to a
  signed URL when playback starts
- `external/mixcloud/`: public catalog, browse-entry shortcuts, creator jumps,
  genre search and local genre favorites
- `external/radio/`: internet radio, catalog paging, station favorites
- `external/local/`: local TOML playlist files
- `external/audiobookshelf/`: Audiobookshelf, sectioned playlists, resume

## Checklist

A new provider touches the places in this list. The items that name a feature
apply only when the provider has that feature. The [Steps](#steps) section
shows the code for the main items.

### Package

- [ ] `external/<name>/`: implement `playlist.Provider` and the capability
  interfaces. Add a compile-time check for each capability, such as
  `var _ provider.Searcher = (*Provider)(nil)`. Give the package a
  `NewFromConfig` constructor that returns nil when the provider is not
  configured.
- [ ] Send API, auth and download requests through a client from
  `httpclient.NewAPI(timeout)`. Send audio through `httpclient.Streaming`.
  Both clients follow `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` and `NO_PROXY`.
- [ ] For a server on the local network, wrap a connection error with
  `netdiag.Explain`. macOS users then get the Local Network hint.
- [ ] For a sign-in that saves a token, keep the token in a
  `credstore.File` in the config directory.
- [ ] For a browser sign-in, implement `playlist.Authenticator`. Pass the
  sign-in URL to an `authurl.Observer` behind a package function
  `SetAuthURLObserver`, as Spotify, Qobuz, Tidal and YouTube do.
- [ ] For a provider that plays through yt-dlp and takes `cookies_from`, call
  `resolve.SetYTDLCookiesForHost(host, cfg.CookiesFrom)` in `NewFromConfig`,
  as `external/soundcloud/provider.go` does. yt-dlp then uses the browser
  session of the user for that host.
- [ ] Add table-driven tests that run against `httptest` servers.
- [ ] `provider/types.go`: add a `ProviderMeta` key constant for the ID of a
  track, such as `MetaQobuzID = "qobuz.id"`. A key can hold only letters,
  digits, `.`, `_` and `-`. `playlist.WriteTrackTOML` does not save other keys.

### Configuration

- [ ] `config/config.go`: add a `<Name>Config` struct with an `IsSet` method
  and a `set(key, val)` method, a field on `Config`, and a case in the section
  switch of `Load`. The doc comment of the struct names the enable rule. When
  the section header alone turns the provider on, as `[spotify]` does, also
  add a case to the header switch of `Load`.
- [ ] `config/<name>_test.go`: table-driven tests for the keys of the section.
- [ ] `config.toml.example`: add the section as comments.
  `TestLoadGolden` in `config/config_golden_test.go` uncomments every setting
  of the example, so add the expected values to `allWant`.
- [ ] For a setting that the provider saves while it runs, give the provider
  a save function from `buildProviders`. Navidrome gets `SaveSort`, Mixcloud
  gets `SaveStyles` and Radio gets `SaveCountry`. The save function calls
  `config.SaveSection`, which keeps the other lines and comments of the file.

### Registration in providers.go

- [ ] `providerKeys`: add the key, the display name, an optional alias, and
  `optional: true` when the provider registers only when it is configured.
  The `--provider` flag, its help text and the log of skipped providers read
  this table.
- [ ] `buildProviders`: register the provider with `add(key, p)` when
  `NewFromConfig` returns a provider.
- [ ] `providers_test.go`: add a case to `TestBuildProviders`. The test fails
  for an entry that `providerKeys` does not list.
- [ ] `registerPlayerHooks`: add a case when the provider resolves its own URI
  scheme when playback starts, such as `qobuz://`. Set `Buffered` in the
  `player.ResolvedSource` when the resolved URL is a finite file.
- [ ] `isBufferedProviderURL`: add a matcher when the stream URLs of the
  provider are finite files that need the buffered pipeline.
- [ ] `observeAuthURLs`: add a case when the provider has a browser sign-in.

A provider that implements `CustomStreamer` or `Closer` needs no code in
`providers.go`. `registerPlayerHooks` and `Close` find these interfaces in
the provider list.

### UI

- [ ] For a `Shift+letter` shortcut, add the key to `providerKeyForShortcut`
  in `ui/model/providers.go` and a row to `commandRegistry` in
  `ui/model/command_registry.go`. The tests in `ui/model/key_drift_test.go`
  compare the keys that the handlers take with the registry. Add the key to
  the table test of `providerKeyForShortcut` in `ui/model/view_helpers_test.go`.
- [ ] `providerEmptyStateHint` in `ui/model/view.go`: add the hint that shows
  under "No playlists in X". The map key is the lowercase `Name()`.
- [ ] For synced lyrics, add a
  `TrackLyrics(ctx context.Context, track playlist.Track) ([]lyrics.Line, error)`
  method. `trackLyricsSource` in `ui/model/lyrics.go` finds it. Return
  `lyrics.ErrNotFound` at once for a track that is not from the provider.

### CLI

- [ ] `cmd/setup.go`: add a `providerSpec` to `providers()` for the setup
  wizard. `body` returns the keys that setup writes with `config.SaveSection`.
  `owned` lists every key that setup manages. `TestSetupBodyRoundTrip` in
  `cmd/setup_roundtrip_test.go` fails until the section has a case.
- [ ] `commands.go`: when the provider keeps a credentials file, add a
  `<name>Command()` that returns `providerCredsCommand(...)` for
  `cliamp <name> reset`. Add it to the `Commands` list of `buildApp`.
- [ ] `resolve/` and `playlist/url.go`: add URL detection when users can
  paste links of the service.

### Docs

- [ ] `docs/<name>.md`: setup, features and limits.
- [ ] `docs/configuration.md`: the section, the row in
  [Provider enable rules](configuration.md#provider-enable-rules) and the
  `provider` values.
- [ ] `docs/cli.md`: the `--provider` values, the `setup` provider list and
  any reset command.
- [ ] `docs/keybindings.md`: the shortcut, and the `Ctrl+F` provider list when
  the provider implements `Searcher`.
- [ ] `README.md`: the provider list of the intro, and the setup list when the
  wizard supports the provider.
- [ ] `site/index.html`: the meta description, the lede and the `SOURCES`
  list.
- [ ] `CLAUDE.md`: the provider list in "What cliamp is" and the
  `external/<name>/` row.

## Base Interface (required)

Every provider must implement `playlist.Provider` in `playlist/provider.go`:

```go
type Provider interface {
    Name() string
    Playlists() ([]PlaylistInfo, error)
    Tracks(playlistID string) ([]Track, error)
}
```

This interface provides a name, a playlist list, and tracks for a playlist. It
is enough for basic playback.

The `playlist` package also has three optional interfaces:

| Interface | What it enables | Methods |
|---|---|---|
| `Authenticator` | Interactive sign-in on first use | `Authenticate() error` |
| `Refresher` | `Ctrl+R` drops the cached lists and loads them again | `Refresh()` |
| `RefreshablePlaylist` | `Ctrl+R` reloads one open playlist in place. Do not implement it for positional IDs | `CanRefreshPlaylist(id)` |

## Capability Interfaces (optional)

Implement any of these interfaces to enable more UI features. All interfaces
are in `provider/interfaces.go`.

| Interface | What it enables | Methods |
|---|---|---|
| `Searcher` | Track search overlay and IPC search | `SearchTracks(ctx, query, limit)` |
| `ArtistBrowser` | Hierarchical artist browsing | `Artists()`, `ArtistAlbums(id)` |
| `TrackArtistResolver` | Jump from a highlighted provider track to its artist or creator with `N` | `ArtistForTrack(track)` |
| `BrowseEntryProvider` | Add non-playable shortcuts into the provider playlist pane | `BrowseEntries()`. Each entry can set `AfterID`, `AfterSection`, and `OpenInPlaylist` |
| `BrowseModeProvider` | Restrict the inferred browser routes. For example, podcast categories must open shows and not load every feed | `BrowseModes()` |
| `DefaultBrowseModeProvider` | Open a browse route at once when the provider is selected | `DefaultBrowseMode()` |
| `GenreBrowser` | Hierarchical category browsing with provider-defined sort views | `Genres()`, `GenreSortTypes()`, `GenreTracks(genreID, sortType)` |
| `GenreBrowseRouter` | Route multiple provider-pane entries to distinct category catalogues | `GenreBrowserFor(entryID)` |
| `GenreFavoriteToggler` | Favorite and unfavorite categories with `f` in the genre browser | `ToggleGenreFavorite(genreID)` |
| `GenreSearcher` | Search beyond the initially loaded category catalogue | `SearchGenres(ctx, query, limit)` |
| `GenreLabeler` | Name the category level, such as Countries | `GenreLabel()` |
| `AlbumBrowser` | Paginated album browsing with sort | `AlbumList(sort, offset, size)`, `AlbumSortTypes()`, `DefaultAlbumSort()` |
| `AlbumSortSaver` | Save the album sort that the user picks | `SaveAlbumSort(sort)` |
| `AlbumTrackLoader` | Album track listing | `AlbumTracks(albumID)` |
| `PlaybackReporter` | Playback reporting at track start and finish | `CanReportPlayback(track)`, `ReportNowPlaying(track, position, canSeek) error`, `ReportScrobble(track, elapsed, duration, canSeek) error` |
| `ProgressReporter` | Interim position updates while playing, in addition to the start and finish reports of `PlaybackReporter` | `ReportProgress(track, position) error` |
| `TrackPosition` | Server-side position for one track, read on every play | `CanTrackPosition(track)`, `TrackPosition(track)` |
| `ResumeTarget` | Server-side resume position of a playlist | `ResumeTarget(playlistID, tracks)` |
| `PlaybackStateReporter` | Played and in-progress markers from local state. It must do no I/O | `HasPlaybackState()`, `PlaybackState(track)` |
| `SubscriptionLister` | The subscriptions overlay | `Subscriptions()` |
| `ShowLister` | Load the newest episode of a show | `IsShowID(id)` and `AlbumTracks(id)` |
| `BrowseLabeler` | Relabel the two levels of the browse overlay, such as Authors and Books | `BrowseLabels()` |
| `PlaylistWriter` | Add a track to a playlist | `AddTrackToPlaylist(ctx, playlistID, track)` |
| `PlaylistBatchWriter` | Add many tracks in one write. `provider.AddTracks` falls back to `PlaylistWriter` | `AddTracksToPlaylist(ctx, playlistID, tracks)` |
| `PlaylistTargetFilter` | Hide playlists that cannot take new tracks from the picker | `CanAddToPlaylist(pl)` |
| `PlaylistPrepender` | Put tracks at the front of a saved playlist | `PrependTracksToPlaylist(ctx, playlistID, tracks)` |
| `PlaylistCreator` | Create a new playlist | `CreatePlaylist(ctx, name)` |
| `PlaylistDeleter` | Remove playlists and tracks | `DeletePlaylist(name)`, `RemoveTrack(name, index)` |
| `PlaylistRenamer` | Rename a playlist | `RenamePlaylist(oldName, newName)` |
| `PlaylistSaver` | Overwrite the ordered track list of a playlist | `SavePlaylist(name, tracks)` |
| `PlaylistDocumenter` | Snapshot and restore a playlist document for undo | `PlaylistDocument(name)`, `RestorePlaylistDocument(name, data)` |
| `PlaylistDirSourceManager` | Directory sources of a playlist | `DirSources`, `AddDirSource`, `RemoveDirSource`, `SetDirRecursive` |
| `TrackPager` | Fill the queue one page at a time | `TracksPage(playlistID, offset)` |
| `CustomStreamer` | Custom URI decode pipeline | `URISchemes()`, `NewStreamer(uri)` |
| `FavoriteToggler` | Favorite toggling for provider list items, such as stations and shows | `ToggleFavorite(id)` |
| `TrackFavoriter` | Copy the ♥ favorite of a track to the service, such as liked or starred songs. The UI calls it in a `tea.Cmd` and keeps the local favorite when it fails | `CanFavoriteTrack(track)`, `SetTrackFavorite(ctx, track, favorite) error` |
| `CatalogLoader` | Lazy paging of a large catalog | `LoadCatalogPage(offset, limit)` |
| `CatalogSearcher` | Server-side catalog search that fills the pane | `SearchCatalog(query)`, `ClearSearch()`, `IsSearching()` |
| `LocationConsenter` | Ask before the provider uses the location of the listener | `NeedsLocationConsent()`, `LocationConsentID()`, `LocationPrompt()`, `SetLocationConsent(allowed)` |
| `SectionedList` | Sections in the playlist list | `IDPrefix(id)`, `IsFavoritableID(id)` |
| `SectionTitler` | Section headings that change at run time | `SectionTitle(prefix)` |
| `Closer` | Cleanup on shutdown | `Close()` |

## Steps

### 1. Create the package

Create `external/myservice/provider.go`:

```go
package myservice

import (
    "context"
    "net/http"
    "time"

    "github.com/bjarneo/cliamp/config"
    "github.com/bjarneo/cliamp/internal/httpclient"
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
    http    *http.Client
}

// NewFromConfig returns nil when the [myservice] section is not set.
func NewFromConfig(cfg config.MyServiceConfig) *Provider {
    if !cfg.IsSet() {
        return nil
    }
    return &Provider{
        baseURL: cfg.URL,
        token:   cfg.Token,
        http:    httpclient.NewAPI(30 * time.Second),
    }
}

func (p *Provider) Name() string { return "My Service" }

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
  For a URI that becomes a signed URL when playback starts, add a source
  resolver in `registerPlayerHooks`.
- **`Stream: true`**: Set this for HTTP URLs. The player then uses the streaming
  pipeline.
- **`ProviderMeta`**: Add provider-specific metadata in a string map with
  namespaced keys. cliamp uses this metadata for features such as scrobbling
  and favorites:

```go
playlist.Track{
    Path:         "https://my-server/stream/123",
    Title:        "Song Title",
    Artist:       "Artist Name",
    Stream:       true,
    ProviderMeta: map[string]string{"myservice.id": "123"},
}
```

### 3. Add configuration

`config.Load` reads `config.toml` with its own parser. It does not use struct
tags. Add a struct with an `IsSet` method and a `set` method to
`config/config.go`:

```go
// MyServiceConfig holds the credentials of a My Service server.
// Enable rule: credentials.
type MyServiceConfig struct {
    URL   string
    Token string
}

// IsSet reports whether both credentials are present.
func (c MyServiceConfig) IsSet() bool { return c.URL != "" && c.Token != "" }

// set applies one key of the [myservice] section.
func (c *MyServiceConfig) set(key, val string) {
    switch key {
    case "url":
        c.URL = parseString(val)
    case "token":
        c.Token = parseString(val)
    }
}
```

Add the `MyService MyServiceConfig` field to `Config`. Then add a case to the
section switch of `Load`:

```go
case "myservice":
    cfg.MyService.set(key, val)
```

Users write this section:

```toml
[myservice]
url = "https://myservice.example.com"
token = "your-api-key"
```

### 4. Register in providers.go

Add the provider to `buildProviders` in `providers.go`:

```go
if p := myservice.NewFromConfig(cfg.MyService); p != nil {
    add("myservice", p)
}
```

You do not register the capabilities that follow. `providers.go` finds them in
the provider list:

- For each provider that implements `CustomStreamer`, cliamp registers
  `NewStreamer` for each scheme that `URISchemes` returns.
- At shutdown, cliamp calls `Close` on each provider that implements `Closer`.

If the provider resolves its own URI scheme when playback starts, such as
Qobuz `qobuz://` URIs, add a case to `registerPlayerHooks` in `providers.go`.
When the resolved URL is a finite file, set `Buffered` in the
`player.ResolvedSource` that the resolver returns. The player then buffers the
file for seeking, and no URL matcher is necessary:

```go
return player.ResolvedSource{URL: u, Buffered: true}, err
```

If the provider needs the buffered download pipeline for stream URLs, such as
Navidrome Subsonic endpoints, add its URL matcher to `isBufferedProviderURL` in
`providers.go`. The matcher covers every provider, configured or not, because
history, favorites and saved playlists keep these URLs. Append the matcher to
the existing list. Do not remove the other matchers:

```go
func isBufferedProviderURL(u string) bool {
    return navidrome.IsSubsonicStreamURL(u) ||
        jellyfin.IsStreamURL(u) ||
        emby.IsStreamURL(u) ||
        plex.IsStreamURL(u) ||
        audiobookshelf.IsStreamURL(u) ||
        lyrion.IsStreamURL(u) ||
        yandex.IsStreamURL(u) ||
        myservice.IsStreamURL(u)
}
```

### 5. Add a provider key

Add the key and the display name to `providerKeys` in `providers.go`. The
`--provider` flag and its help text then accept the key. `buildProviders`
takes the display name from the table. Set `optional` when the provider
registers only when it is configured. The log then names the provider when it
is not configured.

```go
{key: "myservice", name: "My Service", optional: true},
```

## What the UI Does Automatically

Do not change the UI code for these features. The UI uses the interfaces your
provider implements to do the following:

- Show the browse overlay (`N`) when the active provider implements
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
- Show the search overlay (`Ctrl+F`) when the active provider implements
  `Searcher`, and serve the `provider.search` IPC operation. Without
  `Searcher`, `Ctrl+F` searches YouTube
- Enable add-to-playlist in search results when the searched provider implements `PlaylistWriter`
- Report playback at track start and finish when `PlaybackReporter` is
  implemented, in the TUI and in headless mode. Log failures that the
  provider returns.
- Run interactive authentication on first use when `Authenticator` is implemented
- Put the cursor on the active track and start at its stored position when `ResumeTarget` is implemented
- Send the listening position every 15 seconds while a track plays when `ProgressReporter` is implemented
- Set the browse overlay levels to your own nouns when `BrowseLabeler` is implemented
- Copy a ♥ favorite to the service when `TrackFavoriter` is implemented
- Call `Close()` during shutdown when `Closer` is implemented

`N` and `Ctrl+F` act on the active provider. The one exception is the artist
jump from a highlighted track, which uses the provider of that track.
