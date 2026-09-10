# W3A-1 Handoff

## Status

- done

## What Changed

Spotify provider now implements the multi-type search and full write suite
(plan phases 4-5, provider side) against the frozen contracts in
`provider/interfaces.go`:

- **search.go (new)**: `SearchAll` (provider.MultiSearcher) and
  `ArtistTopTracks` (provider.ArtistTopTracksLoader), with compile-time
  assertions for both.
- **writer.go (new)**: the write suite with compile-time assertions for all
  six interfaces — `AddTracksToPlaylist` (PlaylistBatchWriter),
  `ToggleTrackLike` (TrackLiker), `FollowPlaylistByID`/
  `UnfollowPlaylistByID` (PlaylistFollower), `FollowArtist`/
  `UnfollowArtist` (ArtistFollower), `RemoveTrackFromPlaylist`
  (PlaylistTrackRemover), `RenamePlaylistByID` (RemotePlaylistRenamer).
- **browse_test.go (edited)**: the shared `mockAPI` helper now records every
  request's `(method, body)` per path (new `apiCall` type + `recordedCalls`
  accessor). Existing helpers (`calls`, `last`) and every existing test are
  unchanged.
- All cache invalidation follows the W2A-1 list exactly (details below).

## Files

- external/spotify/search.go (new)
- external/spotify/writer.go (new)
- external/spotify/search_test.go (new)
- external/spotify/writer_test.go (new)
- external/spotify/browse_test.go (edited: mock recording only)

## Verification

- `. /tmp/cliamp-libs/env.sh && gofmt -l -w external/spotify`: pass (clean)
- `. /tmp/cliamp-libs/env.sh && go vet ./external/spotify/...`: pass
- `. /tmp/cliamp-libs/env.sh && go test ./external/spotify/...`: pass (also with `-race`)
- `. /tmp/cliamp-libs/env.sh && GOOS=windows CGO_ENABLED=0 go build ./external/spotify/...`: pass (stub green; new files are `!windows`)

## Blockers Or Risks

- None blocking. Two limitations to document:
  - `ToggleTrackLike` is **music tracks only**. Episodes are saved via
    `/v1/me/episodes` (a separate endpoint family); per spec only
    `/v1/me/tracks` is implemented. An episode/unresolvable track yields a
    clear error ("track has no spotify track ID"), not a wrong API call.
  - `SearchAll`'s per-type limit means a result tab can show up to N items
    *per type* (N clamped to [1,50]), not N total.

## Next Thread Should Know

### Endpoints and bodies (docs thread: this is the authoritative list)

- `SearchAll(ctx, query, limit)` → `GET /v1/search?q=&type=track,album,artist,playlist,episode&limit=N`
  (no market param — user token scopes to the account country). Result semantics:
  - `limit` is **per-type** (Spotify applies it to each type independently), clamped to [1,50].
  - `Tracks`: music tracks first, then **episodes merged into the same
    slice**; episodes keep their `spotify:episode:` URI (same as
    `SearchTracks`), show name fills artist/album.
  - `Albums`: `AlbumInfo{ID, Name, Artist+ArtistID (first artist), Year
    (release_year), TrackCount (total_tracks)}`.
  - `Artists`: `ArtistInfo{ID, Name}`.
  - `Playlists`: `PlaylistInfo{ID, Name, TrackCount, Owned}` — `Owned` is
    `owner.id == current user` via the best-effort `/v1/me` lookup
    (`currentUserID`, same as `Playlists()`); `Section` intentionally empty
    (UI search tabs provide grouping). TrackCount reads `tracks.total` or
    `items.total` (post-episode-rename API shape), whichever is present.
- `ArtistTopTracks(artistID)` → `GET /v1/artists/{id}/top-tracks` (no market).
- `AddTracksToPlaylist(ctx, playlistID, tracks)` → `POST /v1/playlists/{id}/tracks`
  with `{"uris":[...]}` in chunks of **at most 100** per request (250 tracks →
  3 POSTs of 100/100/50). URI resolution per track: `Path` when it is a
  `spotify:track:`/`spotify:episode:` URI, else synthesized from
  `ProviderMeta["spotify.id"]` as `spotify:track:<id>`; tracks with neither
  are **skipped** (counted, not sent). Returns `(added, skipped, err)`.
- `ToggleTrackLike(ctx, track)` → `GET /v1/me/tracks/contains?ids=<id>`, then
  `PUT /v1/me/tracks` body `{"ids":[id]}` to save or `DELETE /v1/me/tracks`
  with the same body to remove. Returns the **new** state. DELETE-with-body
  works through the existing `webAPIWithBody` helper (it buffers/replays
  bodies; Go's client sends DELETE bodies) — no helper change was needed.
- `FollowPlaylistByID` → `PUT /v1/playlists/{id}/followers` with body `{}`
  (Spotify rejects a bodyless follow with 400).
- `UnfollowPlaylistByID` → `DELETE /v1/playlists/{id}/followers` (no body).
  **On playlists the user owns this deletes the playlist server-side.**
- `FollowArtist` → `PUT /v1/me/following?type=artist` body `{"ids":[id]}`;
  `UnfollowArtist` → same path/method DELETE, same body. Spotify answers
  **204 No Content** here (both 200/204 accepted), unlike the playlist endpoints.
- `RemoveTrackFromPlaylist(ctx, playlistID, position)` → resolves the URI at
  the zero-based position from the cached track list when present and long
  enough, else `GET /v1/playlists/{id}/items?offset=P&limit=1&fields=<shared
  projection>`; then `DELETE /v1/playlists/{id}/tracks` body
  `{"tracks":[{"uri":"...","positions":[P]}]}`.
- `RenamePlaylistByID(ctx, playlistID, newName)` → `PUT /v1/playlists/{id}`
  body `{"name":newName}`.

### Cache invalidation (follows the W2A-1 list exactly)

- Batch add: where a `trackCache` entry exists, its `snapshot_id` is updated
  from the response and the cached track list is cleared (refetch on next
  `Tracks()`); `listCache = nil` (same as `AddTrackToPlaylist`).
- Toggle like: `delete(trackCache, "YOUR MUSIC")` and clear the
  recently-played cache (`recentTracks`/`recentTracksAt`).
- Follow/unfollow playlist: `listCache = nil`.
- Follow/unfollow artist: nothing (`Artists()` is uncached).
- Remove track: `delete(trackCache, playlistID)` and `listCache = nil`.
- Rename playlist: `listCache = nil`.

### API caveats for docs

- **Dev-mode catalog block**: `/v1/search` (used by `SearchAll` and
  `SearchTracks`) returns a misleading 400 "Invalid limit" for developer apps
  in Development Mode since Nov 27 2024. `SearchAll` applies the same
  friendly rewrite as `SearchTracks` ("search blocked — your client_id is
  too new…").
- **403 on followed playlists**: add/remove/rename on a playlist the user
  does not own returns 403 from Spotify (surfaced as a wrapped error). The UI
  should gate write actions on `PlaylistInfo.Owned`; following/unfollowing
  works on any playlist.
- Unfollowing an **owned** playlist deletes it — docs should warn.
- All write ops return wrapped errors (`spotify: <op>: <cause>`); 429 retry
  with Retry-After comes free via `webAPIWithBody`.

### Deviations from spec

- `AddTracksToPlaylist`: besides updating the snapshot where a cache entry
  exists, the cached **tracks** are cleared too — `Tracks()` serves cached
  tracks without snapshot comparison, so keeping them would serve stale
  results after the add. (Spec only said "update the snapshot cache".)
- `RemoveTrackFromPlaylist` also clears `listCache` (spec mandated only the
  playlist's `trackCache`) — track counts in the playlist list go stale
  otherwise; matches `AddTrackToPlaylist`'s existing behavior.
- `UnfollowPlaylistByID` sends no body; the `{}` body requirement applies to
  the PUT (follow) only.
- Everything else per spec. Existing tests untouched; the only shared-file
  edit is the additive mock recording in `browse_test.go`.
