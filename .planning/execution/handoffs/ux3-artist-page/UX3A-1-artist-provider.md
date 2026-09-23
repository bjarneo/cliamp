# UX3A-1 — Artist page provider (Spotify ArtistDetailLoader)

Wave ux3 thread A, part 1. Landed uncommitted on `spotify-library-parity`.
Scope touched: `external/spotify/artist.go` (new), `external/spotify/artist_test.go`
(new), and `external/spotify/provider_shared.go` (ONE addition: `Popularity int
json:"popularity"` on `spotifyItem` — nothing else). Nothing else.

## Implementation surface

`external/spotify/artist.go` implements the frozen contract
`provider.ArtistDetailLoader` (`var _ provider.ArtistDetailLoader =
(*SpotifyProvider)(nil)` lives in artist.go, `//go:build !windows` like its
siblings). Entry: `ArtistDetail(artistID string) (provider.ArtistDetail, error)`.

Whole flow bounded by its own `context.WithTimeout(context.Background(), 30s)`
(const `artistDetailTimeout`), layered like recommend.go's.

## Endpoint inventory (in call order)

1. `GET /v1/artists/{id}` — header. Uses `name`, `genres` (deprecated but
   present, verified Sept 2026), `followers.total` (deprecated but present).
   Local decode struct `spotifyArtistFull` (provider_shared's `spotifyArtist`
   has ID/Name only and is shared; do not widen it).
2. `GET /v1/artists/{id}/albums?include_groups=album,single&limit=10&offset=N`
   — discography, offset-paged until the paging envelope's `total` is covered.
   `limit=10` is the endpoint's per-request max (UX1R-1 s11). Items are
   SimplifiedAlbums mapped via the existing `albumFromSpotify` (same
   conventions as browse.go's `ArtistAlbums`); raw albums are ALSO kept for
   the popular pool (release dates + art). `Info.AlbumCount = len(discography)`.
3. Popular pool (top-tracks endpoint is dead — this synthesizes it):
   - Per discography album: `GET /v1/albums/{id}/tracks?limit=50&offset=N`
     paged to exhaustion (AlbumTracks' internals, but threaded on the shared
     30s ctx, unlike `AlbumTracks` which owns a 2-min timeout). A failed
     album-tracks call skips that album and keeps going.
   - `GET /v1/search?q=artist:"NAME"&type=track&limit=10&offset=N` offset-paged
     up to 3 pages (~30 tracks); `NAME` is the header name, quotes included.
   - Pool order: album tracks in discography order, then search tracks.
   - Dedupe by URI (`itemKey`: canonical `spotify:track:` URI, ID fallback),
     first occurrence wins — an album copy beats its search duplicate even
     when the search copy carries a higher popularity.
   - Light artist filter: an item with a non-empty `artists` array is dropped
     unless some artist matches the artist ID or exact header name; items
     with no artists array are kept (not checkable). URI dedupe is the real
     correctness bar.
4. Liked marks: `GET /v1/me/library/contains?uris=...` over the FULL pool's
   URIs in 40-URI chunks (`libraryContainsChunk`, the endpoint max, UX1R-1
   s3). Positional boolean array -> `ProviderMeta["spotify.liked"] = "true"`
   on contained tracks.
5. Popular = top 10 (`popularTrackCount`) of the pool by `item.Popularity`
   descending, `sort.SliceStable`: equal scores keep pool order and
   zero-popularity tracks sink to the end in pool order.

## Partial-failure semantics

| Failure | Behavior |
|---|---|
| ensureSession | error (auth gate) |
| Header (`/v1/artists/{id}`) | **error** (wrapped `spotify: artist detail: ...`); discography never fetched |
| Discography (`/artists/{id}/albums`) | **error** (page is useless without it) |
| One album's tracks | skip that album, keep going |
| Search (any page) | skip the search source entirely |
| Contains (transport/parse) | skip ALL marks (first failure aborts remaining chunks); detail still returned |
| Everything pool-side failing | detail returned with empty `Popular`, nil error |

## Meta-key usage (exact strings, via provider constants)

- `provider.MetaSpotifyPopularity` = `"spotify.popularity"`: value
  `strconv.Itoa(popularity)` ("1"-"100"), set ONLY when `item.Popularity > 0`
  by the local wrapper `popularPoolTrack` — deliberately NOT in
  `trackFromItem` (hot path for playlist fetching untouched). Zero-popularity
  tracks carry NO key and typically NO ProviderMeta map at all.
- `provider.MetaSpotifyLiked` = `"spotify.liked"`: value `"true"`, set ONLY
  on contained tracks. Absent otherwise (never "false").
- Pool tracks do NOT carry `spotify.id` meta (unlike AlbumTracks/recommend
  output) — identity is the canonical `Path` URI (`spotify:track:...`), same
  as `spotifyTrackURI` resolves.

## Pool track shape (for the UI thread)

`popularPoolTrack` (the wrapper) on top of `trackFromItem`:
- Album-sourced candidates (simplified track items): Album name, AlbumArtURL,
  and Year merged from the parent discography album (like AlbumTracks);
  popularity meta absent (simplified items carry no popularity).
- Search-sourced candidates (full track objects): Album/Year from the item's
  own `album` block; popularity meta present when the API reports > 0.
- `Popular` is capped at 10; liked marks were applied to the full pool before
  slicing, so every marked survivor in the top 10 carries `liked=true`.

## Test inventory (artist_test.go, 7, mockAPI transport pattern)

1. `TestArtistDetailHappyPath` — 2-page discography (12 albums, asserts
   `include_groups=album,single`, `limit=10`, offsets 0/10), genres
   ["rock","pop"], followers 1234, `Info.AlbumCount=12`, discography mapped
   via albumFromSpotify, Popular ordering (popularity desc then pool order,
   capped at 10), popularity meta "90"/"85"/"80" then absent, liked marks on
   exactly the two contained URIs, album metadata merge (Album/Year/ArtURL
   from parent album) and item-album fields for search tracks.
2. `TestArtistDetailSearchPaging` — 3 pages of 10 at offsets 0/10/20
   (exactly 3 calls), top-10 = page 1 in order.
3. `TestArtistDetailDedupeAndArtistFilter` — URI dedupe across albums and
   between albums and search (album copy wins, no popularity meta on it),
   foreign-artist search result filtered out.
4. `TestArtistDetailLikedBatchingAtForty` — 45 pool URIs -> contains called
   twice with chunks of 40 and 5, first URI is the first pool URI, all top-10
   marked.
5. `TestArtistDetailContainsFailureSkipsMarks` — no contains handler -> no
   marks, nil error, Popular intact.
6. `TestArtistDetailPoolFailureLeavesPopularEmpty` — album-tracks AND search
   both fail -> empty Popular, nil error, header+discography intact.
7. `TestArtistDetailHeaderFailureReturnsError` — no header handler -> error
   mentioning "artist detail", zero discography calls.

## Verify output (scoped, per thread rules)

```
. ~/cliamp-libs/env.sh
go build ./...                          # clean
go vet ./external/spotify/...           # clean
go test -count=1 ./external/spotify/... # ok github.com/bjarneo/cliamp/external/spotify
gofmt -l external/spotify               # no output
```

Stability: `go test -count=3 ./external/spotify/...` clean; all 7
TestArtistDetail* pass verbosely.

## Notes for the UI thread

- Zero-popularity tracks carry NO popularity meta key; treat missing as 0.
- `liked` is `"true"` only when the library contained the URI — absent means
  unmarked (not-saved or contains-check failed). Don't render a "not liked"
  state off the meta.
- Deprecation reality: artist `genres`/`followers` and track `popularity` are
  deprecated-but-present fields (UX1R-1 s9/s10) — they work today; if Spotify
  removes them the page degrades to name+discography+unranked pool.
- `Popular` is max 10 tracks; "Liked songs" sections beyond those 10 need
  their own fetch (the pool itself is not exported).
- Follow-state is NOT part of ArtistDetail — use the existing `f` flow
  (ArtistFollower via `/v1/me/library`).
- Known adjacent gap (NOT touched, out of scope): browse.go's
  `ArtistAlbums` still pages with `limit=50` while the endpoint max is now
  10 — if the server clamps to 10, its offset-by-50 loop can skip items.
  artist.go's own discography paging uses 10 correctly. Flagged for the
  wave-5 review or a browse.go owner.
