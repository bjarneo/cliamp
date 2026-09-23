# W2A-1 Handoff

## Status

- done

## What Changed

Spotify provider now implements the library-browse capability contracts
(plan phases 1-3, provider side):

- **Browse** (new `browse.go`): `Artists`, `ArtistAlbums`, `AlbumList` +
  `AlbumSortTypes`/`DefaultAlbumSort`/`SaveAlbumSort`, `AlbumTracks`.
  Compile-time assertions for `provider.ArtistBrowser`, `AlbumBrowser`,
  `AlbumSortSaver`, `AlbumTrackLoader`, `TrackPager`.
- **Library rows + paging** (new `pager.go`): `TracksPage` (TrackPager),
  shared page fetcher `fetchTracksPage`, 60s caches for Top Tracks /
  Recently Played, availability probes used by `Playlists()`.
- **provider.go**: `Playlists()` gained the synthetic Library rows (Top
  Tracks, Recently Played; best-effort, silently omitted when their probe
  fails) and sets `Owned: true` on playlists owned by the current user.
  `Tracks()` was refactored to loop the shared page fetcher; snapshot-cache
  behavior and the 2-minute overall timeout are preserved, full loads still
  populate `trackCache`.
- **provider_shared.go**: `spotifyArtist` gained an `id` field; extracted
  `releaseYear` helper (now also used by `trackFromItem`); added synthetic
  ID constants (`yourMusicID`/`topTracksID`/`recentlyPlayedID`) and the
  `itemKey` dedupe helper. No behavior change.

## Files

- external/spotify/browse.go (new)
- external/spotify/pager.go (new)
- external/spotify/browse_test.go (new)
- external/spotify/pager_test.go (new)
- external/spotify/provider.go (edited)
- external/spotify/provider_shared.go (edited)

## Verification

- `. /tmp/cliamp-libs/env.sh && gofmt -l -w external/spotify`: pass (no files reformatted)
- `. /tmp/cliamp-libs/env.sh && go vet ./external/spotify/...`: pass
- `. /tmp/cliamp-libs/env.sh && go test ./external/spotify/...`: pass (also with `-race`)
- `. /tmp/cliamp-libs/env.sh && go test ./config/... ./playlist/... ./provider/...`: pass
- `GOOS=windows CGO_ENABLED=0 go build ./external/spotify/...`: pass (shared-file changes don't break the stub)

## Blockers Or Risks

- None blocking. Two judgment calls to flag:
  - `DefaultAlbumSort` reads the persisted sort lazily via `config.Load()`
    (memoized in `p.browseSort`). main.go was not touched, so the provider
    has no config threading; this matches the spec's fallback instruction.
    If the lead later prefers constructor threading (`NewFromConfig`
    style), `browseSort` is the single field to initialize.
  - Sort direction for `year` is **newest year first** (ties broken by
    title). The spec didn't pin a direction; docs thread should state it.

## Next Thread Should Know

- **Synthetic playlist IDs** (constants in provider_shared.go):
  `YOUR MUSIC` (liked songs, pre-existing), `TOP TRACKS`, `RECENTLY
  PLAYED`. All are Section `"Library"`, inserted after "Your Music" in
  `Playlists()`. Probes: Top Tracks row count = `/v1/me/top/tracks` total;
  Recently Played row count = distinct tracks in the last 50 plays (matches
  what opening the row shows).
- **Sort IDs** (browse.go): `recent` (default, "Recently saved", API
  order), `title`, `artist` (artist then title), `year` (desc). Persisted
  via `config.SaveSpotifySort` -> `[spotify] album_sort`. Unknown IDs are
  rejected by `SaveAlbumSort` and `AlbumList` returns an error for them.
- **Endpoints per method**:
  - `Artists()` -> `GET /v1/me/following?type=artist` (50/page)
  - `ArtistAlbums(id)` -> `GET /v1/artists/{id}/albums?include_groups=album,single` (50/page, API order kept)
  - `AlbumList` -> `GET /v1/me/albums` (full fetch, 50/page) cached 5 min, then client-side sort + window
  - `AlbumTracks(id)` -> `GET /v1/albums/{id}` once for metadata (name, art URL, year) + `GET /v1/albums/{id}/tracks` (50/page). Tracks carry `ProviderMeta["spotify.id"]` (const `metaSpotifyID`) — `trackFromItem` does NOT set ProviderMeta anywhere else yet.
  - `TracksPage("YOUR MUSIC")` -> `/v1/me/tracks`; playlists -> `/v1/playlists/{id}/items` with the existing field projection (`playlistItemsFields` const in pager.go); both loop ≤50-per-call until the requested limit is collected. Returns API total.
  - `TracksPage("TOP TRACKS")` -> `/v1/me/top/tracks?time_range=short_term`, capped at 200, cached 60s, windowed (total = cached length).
  - `TracksPage("RECENTLY PLAYED")` -> `/v1/me/player/recently-played?limit=50` single page, deduped by URI keeping the newest play, cached 60s (total = deduped length).
  - `TracksPage` clamps limit to [1,200]; offset past the end returns an empty page with the real total.
- **Caches another thread must invalidate on writes**:
  - save/unsave album (library writes) -> call `p.invalidateAlbumCache()` (5 min TTL cache backing AlbumList).
  - follow/unfollow playlist, create/delete/rename playlist -> set `p.listCache = nil` under `p.mu` (same as `AddTrackToPlaylist` already does).
  - like/unlike track (YOUR MUSIC writes) -> `delete(p.trackCache, yourMusicID)` and consider clearing `p.recentTracks` (recently-played cache, 60s).
  - follow/unfollow artist -> `Artists()` is uncached, nothing to do.
  - All library-row/album caches are also cleared on session change via `resetSessionScopedStateLocked`.
- **Deviations from spec**: only the two judgment calls above (lazy config
  read, year direction). Everything else follows the spec; probes are
  bounded by a 30s sub-timeout inside `Playlists()`' 5-minute context.
