# UX1A-1 — API migration + contract freeze

Worker UX1A-1, wave ux1. Migrated the Spotify provider's write endpoints and
search to the February-2026 Web API (shapes verified in UX1R-1-api-shapes.md),
removed the deleted ArtistTopTracks surface, and froze the Recommender /
ArtistDetail contracts for later waves. All old `{"ids":[...]}` bodies,
`/v1/users/{id}/playlists`, `/v1/playlists/{id}/tracks`, `/v1/me/tracks`,
`/v1/me/following` writes, and `/v1/playlists/{id}/followers` are gone.

## What changed, per endpoint

| Operation | Old | New |
|---|---|---|
| CreatePlaylist (provider.go) | `POST /v1/users/{userID}/playlists`, body `{"name","public":false}`, 200/201 | `POST /v1/me/playlists`, same body, 201 expected (200+201 accepted). `currentUserID` dependency dropped from this function; `currentUserID` itself stays (SearchAll Owned tagging) |
| AddTrackToPlaylist (provider.go) | `POST /v1/playlists/{id}/tracks`, body `{"uris":[uri]}` | `POST /v1/playlists/{id}/items`, same body (position omitted = append) |
| AddTracksToPlaylist (writer.go) | `POST /v1/playlists/{id}/tracks`, 100-URI chunks | `POST /v1/playlists/{id}/items`, unchanged chunking (`maxAddTracksPerRequest` = 100), response still `{"snapshot_id":...}` 201 |
| RemoveTrackFromPlaylist (writer.go) | `DELETE /v1/playlists/{id}/tracks`, body `{"tracks":[{"uri":...}]}` | `DELETE /v1/playlists/{id}/items`, body `{"items":[{"uri":...}]}` (field renamed `tracks`→`items`; no positions field exists). URI-based resolution and position-is-bookkeeping-only contract kept; cache invalidation kept |
| ToggleTrackLike (writer.go) | GET `/v1/me/tracks/contains?ids=ID` then PUT/DELETE `/v1/me/tracks` with `{"ids":[ID]}` body | GET `/v1/me/library/contains?uris=spotify:track:ID` (bare `[bool]`) then PUT (save) / DELETE (remove) `/v1/me/library?uris=spotify:track:ID` — **URIs in the query string, no request body**, max 40 URIs (we send 1). Accepts 200 and 204 for the empty response |
| setPlaylistFollowed (writer.go) | PUT/DELETE `/v1/playlists/{id}/followers` (PUT carried a `"{}"` body workaround) | PUT/DELETE `/v1/me/library?uris=spotify:playlist:ID`, no body, 200+204 accepted. The "requires a body" workaround is gone. listCache invalidation kept |
| setArtistFollowed (writer.go) | PUT/DELETE `/v1/me/following?type=artist` with `{"ids":[...]}` body | PUT/DELETE `/v1/me/library?uris=spotify:artist:ID`, no body, 200+204 accepted. See residual risk #1 |
| SearchTracks (provider.go) + SearchAll (search.go) | limit clamped to [1,50] | limit clamped to [1,10]; doc comments note "Spotify's accepted range of 1..10" and server default 5. UI callers still pass 20 — the provider clamps silently (expected; search tabs show up to 10 per type now) |
| ArtistTopTracks | `GET /v1/artists/{id}/top-tracks` | **Deleted entirely** (endpoint removed from the API, no replacement). Removed: the func + interface-check var in search.go, `ArtistTopTracksLoader` in provider/interfaces.go, the capability probe in ui/model/spot_search_tabs.go, the branch in ui/model/commands.go fetchSpotArtistCmd (artist drill now goes straight to ArtistBrowser.ArtistAlbums), the fake's implementation, and its tests |

Untouched on purpose (still on pre-Feb-2026 shapes or unconfirmed): `RenamePlaylistByID` (`PUT /v1/playlists/{id}` — not in my task list; the playlist-object docs still show a change-details endpoint), the `GET /v1/me/following` read path in browse.go `Artists()`, `/v1/me/playlists`, `/v1/me/tracks` totals probe, pager.go item fetches (already on `/v1/playlists/{id}/items`), browse.go saved albums, `/v1/me/top/{type}` (still max 50).

## Frozen contracts (for ux2/ux3 — code against these EXACT shapes)

provider/interfaces.go:

```go
// Recommender is implemented by providers that can recommend additional
// tracks related to the current queue (e.g. Smart Shuffle).
type Recommender interface {
    RecommendTracks(ctx context.Context, seed []playlist.Track, limit int) ([]playlist.Track, error)
}
```
(placed right after TrackLiker)

```go
// ArtistDetailLoader is implemented by providers that can return a rich
// artist profile (header info plus popular tracks and discography).
type ArtistDetailLoader interface {
    ArtistDetail(artistID string) (ArtistDetail, error)
}
```
(placed where ArtistTopTracksLoader used to be, between MultiSearcher and TrackLiker)

provider/types.go:

```go
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
```

New meta keys beside the existing Meta* block:
`MetaSpotifyPopularity = "spotify.popularity"`, `MetaSpotifyLiked = "spotify.liked"`.

No implementations exist yet — purely additive; later waves implement against these shapes. Notes for implementers: artist `followers`/`genres`/`popularity` and track/album `popularity` all still exist but are marked Deprecated on the live docs (UX1R-1 sections 9-10) — usable, don't assume longevity.

## Test inventory updated

- external/spotify/writer_test.go: add-track tests moved to `/v1/playlists/pl1/items`; ToggleTrackLike asserts `uris=spotify:track:t1` in query on both contains and flip, and empty flip body; playlist/artist follow tests moved to `/v1/me/library` asserting method + query URI + no body; remove test asserts the `items:[{uri}]` body shape on DELETE `/v1/playlists/pl1/items`; stale comment updated.
- external/spotify/search_test.go: limit-clamp table now expects 10 as the ceiling; TestArtistTopTracks deleted.
- ui/model/spotify_write_test.go: fakeSpotifyProvider's `artistTop` field + ArtistTopTracks method removed.
- ui/model/spot_search_tabs_test.go: TestSpotSearchArtistDrillTopTracks deleted; TestSpotSearchArtistDrillFallsBackToAlbums renamed TestSpotSearchArtistDrillLoadsAlbums (no fallback exists anymore); albumOnlyProvider comment updated.
- ui/model/commands_test.go: no changes needed (no references).

## Verification

```
. ~/cliamp-libs/env.sh
go build ./...                                              # ok
go vet ./external/spotify/... ./provider/... ./ui/...        # ok
go test -count=1 ./external/spotify/... ./provider/... ./ui/ # all ok
gofmt -l <edited files>                                     # clean
```

Counts: external/spotify 39 top-level + 46 subtests PASS (0 fail);
ui/model 143 top-level + 207 subtests PASS (0 fail); ui 434 PASS total incl.
ui/model; provider has no test files.

## Residual risks

1. **Artist-URI library edge (unconfirmed):** the save/remove library docs
   omit `spotify:artist:{id}` from the supported `uris` types while
   library/contains includes it (UX1R-1 surprise #10). setArtistFollowed
   implements PUT/DELETE `/v1/me/library?uris=spotify:artist:...` as
   specified; if Spotify rejects it, follow/unfollow artist will surface an
   http-status error. The code comment in writer.go marks the spot to revisit.
2. **Non-owned playlist items 403:** per UX1R-1 section 6,
   `GET /v1/playlists/{id}/items` now requires playlist-read-private and only
   returns items for own/collab playlists; drilling into a followed playlist's
   tracks may 403. Surfacing that honestly is UI work for a later wave — not
   addressed here (drill path still calls Tracks()).
3. **Search result count dropped to 10 per type** (from up to 50/20): the UI
   still requests 20 and the provider clamps to 10 silently. If search tabs
   need more results, offset paging must be added later.
4. **Exact 4xx bodies unverified** (UX1R-1 #10): error paths rely on
   webAPIWithBody's generic status-line + body-snippet errors.
5. `GET /v1/me/following` (Artists() browse read) was NOT migrated — it wasn't
   in this task's endpoint list and UX1R-1 only confirms removal of the
   `/contains` variants. If Feb-2026 also killed the list endpoint, Artists()
   will fail at runtime; flagging for a follow-up verification.

## For ux2/ux3

- Recommender and ArtistDetailLoader/ArtistDetail are frozen exactly as
  specified above; implement, don't reshape, without coordinator sign-off.
- The artist drill from search tabs now lands on the album list only — the
  rich artist page (ux3) plugs in via ArtistDetailLoader without disturbing
  the current flow.
- ArtistTopTracksLoader no longer exists anywhere; any stale references in
  docs/ are owned by the docs thread (UX1B-1).
- Popular-track metadata should use the frozen MetaSpotifyPopularity /
  MetaSpotifyLiked keys.
