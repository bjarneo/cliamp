# Creating a Provider

Providers live in `external/<name>/` (e.g. `external/jellyfin/`). A provider is
a Go package that implements the base `playlist.Provider` interface and
optionally implements capability interfaces from the `provider/` package. The UI
discovers capabilities at runtime via type assertions and enables features
accordingly.

See the existing providers for reference:
- `external/navidrome/` — Subsonic API, browsing, scrobbling
- `external/plex/` — Plex Media Server, search, album tracks
- `external/spotify/` — Spotify, search, playlist management, custom streaming
- `external/radio/` — internet radio, favorites
- `external/local/` — local TOML playlist files

## Base Interface (required)

Every provider must implement `playlist.Provider`:

```go
type Provider interface {
    Name() string
    Playlists() ([]playlist.PlaylistInfo, error)
    Tracks(playlistID string) ([]Track, error)
}
```

This gives the provider a name, a list of playlists, and the ability to return
tracks for a playlist. That's enough for basic playback.

## Capability Interfaces (optional)

Implement any combination of these to unlock additional UI features. All
interfaces are defined in `provider/interfaces.go`.

| Interface | What it enables | Methods |
|---|---|---|
| `Searcher` | Track search overlay | `SearchTracks(ctx, query, limit)` |
| `ArtistBrowser` | Hierarchical artist browsing | `Artists()`, `ArtistAlbums(id)` |
| `AlbumBrowser` | Paginated album browsing with sort | `AlbumList(sort, offset, size)`, `AlbumSortTypes()` |
| `AlbumTrackLoader` | Album track listing | `AlbumTracks(albumID)` |
| `Scrobbler` | Playback reporting | `Scrobble(track, submission)` |
| `PlaylistWriter` | Add track to playlist | `AddTrackToPlaylist(ctx, playlistID, track)` |
| `PlaylistCreator` | Create new playlist | `CreatePlaylist(ctx, name)` |
| `PlaylistDeleter` | Remove playlists/tracks | `DeletePlaylist(name)`, `RemoveTrack(name, index)` |
| `CustomStreamer` | Custom URI decode pipeline | `URISchemes()`, `NewStreamer(uri)` |
| `FavoriteToggler` | Favorite toggling | `ToggleFavorite(id)` |
| `Closer` | Cleanup on shutdown | `Close()` |
| `Authenticator` | Interactive sign-in flow | `Authenticate() error` (in `playlist` package) |

## Steps

### 1. Create the package

Create `external/<name>/provider.go`:

```go
package jellyfin

import (
    "context"
    "cliamp/playlist"
    "cliamp/provider"
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

When building `playlist.Track` values:

- **`Path`** — the playable URL or file path. For HTTP streams, use a full URL.
  For custom URI schemes (e.g. `spotify:track:xxx`), implement `CustomStreamer`.
- **`Stream: true`** — set this for HTTP URLs so the player uses the streaming
  pipeline.
- **`ProviderMeta`** — attach provider-specific metadata as a string map with
  namespaced keys. This is used for features like scrobbling:

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

Add a config struct to `config/config.go`:

```go
type JellyfinConfig struct {
    URL   string `toml:"url"`
    Token string `toml:"token"`
}
```

Add the field to the top-level `Config` struct and a TOML section:

```toml
[jellyfin]
url = "https://jellyfin.example.com"
token = "your-api-key"
```

### 4. Register in main.go

Wire up the provider in the `run()` function in `main.go`:

```go
if cfg.Jellyfin.URL != "" && cfg.Jellyfin.Token != "" {
    jfProv := jellyfin.New(cfg.Jellyfin.URL, cfg.Jellyfin.Token)
    providers = append(providers, ui.ProviderEntry{
        Key: "jellyfin", Name: "Jellyfin", Provider: jfProv,
    })
}
```

If your provider needs a custom audio pipeline (like Spotify's `spotify:` URIs),
register a streamer factory:

```go
if cs, ok := myProv.(provider.CustomStreamer); ok {
    for _, scheme := range cs.URISchemes() {
        p.RegisterStreamerFactory(scheme, cs.NewStreamer)
    }
}
```

If your provider needs the buffered download pipeline for its stream URLs
(like Navidrome's Subsonic endpoints), register a URL matcher:

```go
p.RegisterBufferedURLMatcher(jellyfin.IsStreamURL)
```

### 5. Add a `--provider` flag value

In `main.go`'s help text, add your provider key to the `--provider` line so
users can set it as their default.

## What the UI Does Automatically

You don't need to touch the UI code. Based on which interfaces your provider
implements, the UI will automatically:

- Show the browse overlay ("N") if any registered provider implements `ArtistBrowser` or `AlbumBrowser`
- Show the search overlay ("F") if any registered provider implements `Searcher`
- Enable add-to-playlist in search results if the searched provider implements `PlaylistWriter`
- Scrobble playback if `Scrobbler` is implemented
- Run interactive auth on first use if `Authenticator` is implemented
- Call `Close()` on shutdown if `Closer` is implemented

The "N" and "F" shortcuts work regardless of which provider is currently active
— they find the first registered provider with the needed capability.
