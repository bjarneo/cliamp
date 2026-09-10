# W1C-1 Handoff

## Status

- done

## What Changed

- Added 8 capability interfaces to `provider/interfaces.go`: TrackPager,
  MultiSearcher, ArtistTopTracksLoader, TrackLiker, PlaylistFollower,
  ArtistFollower, PlaylistTrackRemover, RemotePlaylistRenamer.
- Added `SearchResults` struct to `provider/types.go`.
- Added `Owned bool` field to `playlist.PlaylistInfo`.
- Added `AlbumSort string` to `config.SpotifyConfig` (TOML key `album_sort`
  under `[spotify]`) and `config.SaveSpotifySort(sortType string) error`
  mirroring `SaveNavidromeSort`.

## Files

- provider/interfaces.go
- provider/types.go
- playlist/provider.go
- config/config.go

## Verification

- go build ./...: pass (43 pkgs)
- go test ./...: pass (ok=43 fail=0)

## Blockers Or Risks

- None. All changes are additive; no existing behavior changed.
- Note: this environment needs `. /tmp/cliamp-libs/env.sh` before any go
  command (Go toolchain at /tmp/gotool + local alsa/flac/ogg/vorbis dev
  prefix; no sudo available).

## Next Thread Should Know

Frozen signatures (provider/interfaces.go, lines 142-196):

```go
type TrackPager interface {
	TracksPage(id string, offset, limit int) (tracks []playlist.Track, total int, err error)
}
type MultiSearcher interface {
	SearchAll(ctx context.Context, query string, limit int) (SearchResults, error)
}
type ArtistTopTracksLoader interface {
	ArtistTopTracks(artistID string) ([]playlist.Track, error)
}
type TrackLiker interface {
	ToggleTrackLike(ctx context.Context, track playlist.Track) (liked bool, err error)
}
type PlaylistFollower interface {
	FollowPlaylistByID(ctx context.Context, playlistID string) error
	UnfollowPlaylistByID(ctx context.Context, playlistID string) error
}
type ArtistFollower interface {
	FollowArtist(ctx context.Context, artistID string) error
	UnfollowArtist(ctx context.Context, artistID string) error
}
type PlaylistTrackRemover interface {
	RemoveTrackFromPlaylist(ctx context.Context, playlistID string, position int) error
}
type RemotePlaylistRenamer interface {
	RenamePlaylistByID(ctx context.Context, playlistID, newName string) error
}
```

provider/types.go:

```go
type SearchResults struct {
	Tracks    []playlist.Track
	Albums    []AlbumInfo
	Artists   []ArtistInfo
	Playlists []playlist.PlaylistInfo
}
```

playlist/provider.go — `PlaylistInfo` gained:

```go
	// Owned reports whether the current user owns this playlist (only
	// meaningful for remote providers that expose ownership).
	Owned bool
```

config: field `SpotifyConfig.AlbumSort` (TOML `album_sort`), parsed in the
`[spotify]` case as `case "album_sort": cfg.Spotify.AlbumSort = parseString(val)`;
persist via `config.SaveSpotifySort(sortType)`. Navidrome reference
implementation for the provider side: `external/navidrome/client.go:155-161`
(`SaveAlbumSort` delegating to the config saver).
