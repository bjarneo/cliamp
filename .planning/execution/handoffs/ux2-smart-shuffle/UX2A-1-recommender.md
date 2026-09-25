# UX2A-1 — Smart Shuffle backend (playlist Smart API + Spotify Recommender)

Wave ux2 thread A, part 1. Landed uncommitted on `spotify-library-parity`.
Scope touched: `playlist/` (playlist.go, smart_test.go) and
`external/spotify/` (recommend.go, recommend_test.go). Nothing else.

## Playlist Smart API — exact semantics (for the ui/model thread)

`Track.Smart bool` is additive (mirrors `Bookmark`; not serialized in
M3U/PLS — same as Bookmark). All methods take `p.mu`.

| Method | Semantics |
|---|---|
| `Smart() bool` | Returns the smart-mode flag. The flag alone changes NOTHING — no injection, no removal. Pairing with shuffle is the caller's concern. |
| `EnableSmart()` | Sets the flag only. |
| `DisableSmart()` | Clears the flag AND removes every unplayed Smart row: rows whose track sits in the order tail strictly AFTER `p.pos` are dropped from the playback order and from `p.tracks`, with the same order/queue/position fixups `Remove(idx)` performs (its body is now the shared `removeLocked`). Smart rows at order pos <= `p.pos` (played + the current row) STAY, still marked `Smart`. Safe on empty playlists. |
| `SmartPending() int` | Count of Smart rows in `p.order[p.pos+1:]` (upcoming tail only). Queue entries never count (queue items are never Smart). Current row does not count. |
| `AddSmart(tracks ...Track)` | FULL no-op unless `p.shuffle` is on (nothing appended, nothing marked — check `Shuffled()` first or gate the call). When on: marks each track `Smart`, appends to `p.tracks`, and mixes the new indices into the upcoming order tail with the identical mechanics as `Add()`'s shuffle branch (Fisher-Yates on `p.order[p.pos+1:]`; empty-playlist/inconsistent-pos recovery matches `Add`). Never places a Smart row at or before the current position. Current track unchanged. |

Snapshot/Restore round-trips the flag; marks ride on the cloned Track values,
so Smart rows and their order positions survive restore exactly.

Caller recipes matching the plan:
- Toggle on: gate on provider Recommender support, then `EnableSmart()` (UI
  also toggles shuffle itself if it wants injection to work — `AddSmart`
  silently no-ops otherwise).
- Toggle off: `DisableSmart()` — pending rows vanish from the upcoming order,
  played history (and current row) is untouched.
- Prefetch check: `SmartPending() <= max(2, len/10)` on the remaining tail.

## RecommendTracks behavior (external/spotify/recommend.go)

Implements `provider.Recommender` (`var _ provider.Recommender =
(*SpotifyProvider)(nil)` lives in recommend.go). Signature frozen:
`RecommendTracks(ctx, seed, limit) ([]playlist.Track, error)`.

- Own `context.WithTimeout(ctx, 30s)` layered under the caller ctx; all calls
  bounded by it.
- `limit < 1` returns `(nil, nil)` with zero API calls. Results capped at
  `limit`, familiar first then discovery, each source in API order.
- Familiar: `GET /v1/me/top/tracks?time_range=medium_term&limit=50` (one
  page, the endpoint max), tracks already in the seed (by URI via
  `spotifyTrackURI`: track Path or `spotify.id` meta) filtered out.
- Discovery: `GET /v1/me/top/artists?limit=20` -> artists absent from the
  seed's artist universe (best-effort: read from
  `ProviderMeta["spotify.artist_ids"]` comma-separated; `trackFromItem`
  doesn't populate it today, so in practice all top artists qualify) -> up to
  3 artists -> per artist `GET /v1/artists/{id}/albums?include_groups=album&limit=10`
  (one page, the new per-request max), sorted newest release_date first ->
  per album `GET /v1/albums/{id}/tracks?limit=50` (one page), tracks merged
  with album name/year/art URL and `spotify.id` meta, like `AlbumTracks`.
- Dedupe by URI within results and against seed. Early stop: discovery is
  skipped entirely when familiar fills the limit; album iteration stops at
  the limit.
- Failure policy — never fatal: familiar failing skips that source; top
  artists failing skips discovery; an artist-albums or album-tracks failure
  skips that chain/album. `(partial, nil)` whenever anything was gathered
  (including empty-but-not-total-failure). Error returned ONLY when both the
  top-tracks AND top-artists calls errored (single wrapped error, both %w).

## Test inventory

playlist/smart_test.go (7): flag toggle; AddSmart no-op when not shuffled
(nothing appended/marked); AddSmart inserts into upcoming tail only after
current pos + marks + current unchanged; SmartPending counts only unplayed;
DisableSmart removes unplayed Smart rows, keeps played+current, order stays a
valid permutation, current survives; snapshot/restore roundtrip (flag +
marks + order + tracks); empty-playlist safety.

external/spotify/recommend_test.go (8, mockAPI transport pattern):
familiar-only path (query shapes medium_term/limit=50, discovery never runs);
dedupe against seed (Path + meta id) and within results; full discovery chain
(artist-universe filter via `spotify.artist_ids` meta, include_groups=album +
limit=10 shapes, newest-album-first ordering with albums delivered
oldest-first, album metadata merge, seed artist never queried); 3-artist cap
(4th never queried); partial failure (artist-albums call fails -> familiar
results, nil error); top-tracks failure falls back to discovery; total
failure -> error; limit<=0 clamp (zero API calls).

## Verify output (scoped, per thread rules)

```
. ~/cliamp-libs/env.sh
go build ./...                                       # clean
go vet ./playlist/... ./external/spotify/...         # clean
go test -count=1 ./playlist/... ./external/spotify/...  # ok playlist; ok external/spotify
gofmt -l playlist external/spotify                   # no output
```

Extra stability runs: `go test -count=25 ./playlist/...` and
`go test -count=3 ./external/spotify/...` clean (shuffle-random paths).
Whole-tree `go vet ./...` also clean — the new `Track.Smart` field breaks no
other package's tests (no unkeyed Track literals exist).

## Notes for the UI thread

- `AddSmart` is silent when shuffle is off by design — gate your `Z` handler
  on `Shuffled()` (or enable shuffle) before expecting injections.
- After `Next()` advances onto a Smart row it becomes "played";
  `DisableSmart` then keeps it (marked) — render `✚` from `Track.Smart`.
- `RecommendTracks` returns tracks with `Path` = canonical `spotify:track:`
  URIs; discovery tracks carry `ProviderMeta["spotify.id"]` + album
  name/year/art. Use `spotifyTrackURI`-equivalent identity (Path) for the
  session no-repeat set.
