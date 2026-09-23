# UX5G-1 — ux-parity integration review + final gate

Lead-executed (2026-09-12). Covers the full ux-parity diff (waves ux1-ux5) on
top of the pre-existing uncommitted wave6 work.

## Review

Read-only review agent over the ux surface (external/spotify, provider/,
playlist/, ui/model/, config/), verified per finding. Findings and resolutions:

- **P1-1 Smart accept-guard (FIXED)**: `handleSmartRecommends` checked
  `isActiveProvider` while dispatch resolves the *queue owner* (which can be a
  non-active provider after a provider switch). Result: completions dropped
  forever + one full RecommendTracks flow per tick. Fix: re-resolve
  `recommenderForQueue()` on accept and compare names. Regression test:
  `TestSmartAcceptsQueueOwnerAcrossProviderSwitch`.
- **P1-2 Home shared gen counter (FIXED)**: one `requests.home` counter for
  lists/albums/artists/create meant any section bump made other sections'
  in-flight completions stale, wedging their loading flags permanently and
  disabling lazy album paging. Fix: per-section counters
  (`homeLists`/`homeAlbums`/`homeArtists`/`homeCreate`) +
  `isCurrentHomeSectionRequest(gen, name, *uint64)`. Regression test:
  `TestHomeSectionGensIndependent`.
- **P2-1 (FIXED)**: `openArtistScreen` now calls `cancelArtistRequest()` before
  the state reset (a supersede previously orphaned the prior cancel).
- **P2-2 (DOCUMENTED)**: artist fetch ctx/cancel machinery cannot abort
  provider work (frozen ArtistDetail/AlbumTracks contracts take no ctx);
  comment added at the dispatch site. The provider's own 30s timeout governs.
- **P2-3 (FIXED)**: "+ New playlist" Enter now guarded by a `creating` flag
  (cleared on every terminal path of `homeCreatedMsg`); duplicate-Enter test
  added (`TestHomeNewPlaylistEnterGuardBlocksDuplicateCreate`).
- **P2-4 (DOCUMENTED)**: one-read residual race between the queue's offset-0
  TracksPage read and an in-flight Home page read (provider pager cursor,
  last-write-wins). UI guards both directions; full close needs per-caller
  cursors in the provider. Comment added at `fetchProviderTracks`.
- **P2-5 (RESOLVED — non-issue)**: `GET /v1/me/following?type=artist` verified
  live and unchanged (cursor paging, limit 0-50, default 20; followers/genres/
  popularity fields deprecated but present). Only the write side moved to
  /v1/me/library. `Artists()` needs no migration.
- **Also fixed pre-review**: `H` was not bound in the Home content-pane key
  handler (registry claimed it) — added `case "H": closeHomeView()` +
  `TestHomeHKeyClosesFromContentPane`.

Clean areas (reviewer-verified): provider mutex usage and cache invalidation
on the new endpoints; TracksPage/queue guards in both directions; artist
screen entry-point precedence and supersede paths; smart prefetch guards;
frozen-contract usage; no dead ArtistTopTracksLoader references; error
wrapping and non-fatal policies.

## Gate

- `make check` (gofmt + go vet + go test ./...): **green** (all packages ok).
- New regression tests all pass (`-count=1`, ui/model).

## Residual risks / known scope notes

- Liked-songs section on the artist page covers liked tracks among the
  popular pool only (the frozen ArtistDetail contract carries top-10 Popular).
- Followers/genres/popularity are deprecated-but-present API fields; if
  Spotify executes the removal, the artist header degrades and the popular
  ranking falls back to pool order (documented in docs/spotify.md).
- Home artist/album/playlist content needs the provider pane's capabilities;
  non-owned playlist items may 403 per the Feb-2026 API (docs note added in ux1).

## Commit split for the user (Mimosa gate — commit from your own terminal)

The tree holds wave6 + all ux-parity work, uncommitted. Suggested split:

```
git add player/ resolve/ ipc/ luaplugin/ external/netease/ external/qobuz/ external/navidrome/ docs/plugins.md docs/qobuz.md
git commit -m "security: exec hardening, plugin md5 removal, qobuz key config"

git add provider/ playlist/ config/ external/spotify/ ui/ docs/ site/ config.toml.example .planning/ AI.txt
git commit -m "feat(spotify): Feb-2026 API migration, Smart Shuffle, artist page, Home view"
```

- Do NOT commit `.mimosa/` or `.video_agent/` (session/scan artifacts).
- `config.toml.example` carries both themes' keys (qobuz private_key +
  album_sort + smart_shuffle); it rides in the feature commit.
- If you prefer the ux work separate from the earlier feature waves: not
  cleanly possible — the earlier waves and ux-parity edit the same files
  (e.g. ui/model/keys.go, docs/spotify.md) and were never committed.
