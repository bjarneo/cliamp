# Wave Roadmap — spotify-library-parity

## wave1-contract-lock (sequential, critical path)

- **W1C-1** — Lock shared contracts. Own: `provider/interfaces.go`,
  `provider/types.go`, `playlist/provider.go`, `config/config.go`.
  Adds: TrackPager, MultiSearcher + SearchResults, ArtistTopTracksLoader,
  TrackLiker, PlaylistFollower, ArtistFollower, PlaylistTrackRemover,
  RemotePlaylistRenamer; `PlaylistInfo.Owned bool`; `SpotifyAlbumSort`
  config field + `config.SaveSpotifySort`. Purely additive; tree stays green.
  Gate: `go build ./... && go test ./...`.

## wave2-core-impl (parallel, disjoint scopes)

- **W2A-1** — Spotify provider core (plan phases 1–3 provider side). Own:
  `external/spotify/**`. Browse (Artists/ArtistAlbums/AlbumList+sorts/
  AlbumTracks/SaveAlbumSort), Library rows (Top Tracks / Recently Played),
  TracksPage + Tracks() refactor, `Owned` tagging in Playlists().
  Gate: `go vet ./external/spotify/... && go test ./external/spotify/...`.
- **W2B-1** — UI/model (plan phases 1,3,4,5 UI side). Own: `ui/model/**`.
  Nav 0-album-count tweak, incremental loading via TrackPager +
  tracksAppendedMsg, search tabs + drill-down, write-op keys, pl_picker
  Spotify section, commandRegistry + help. Tests use fake providers.
  Gate: `go vet ./ui/... && go test ./ui/...`.

Dependencies: both depend on W1C-1. W2A-1 and W2B-1 are file-disjoint and
code against the locked contracts (runtime integration verified at gate 2).

## wave3-features-docs (parallel, disjoint scopes)

- **W3A-1** — Spotify search + write suite (plan phases 4–5 provider side).
  Own: `external/spotify/**` (after W2A-1 merged). SearchAll,
  ArtistTopTracks, batch add, ToggleTrackLike, follow/unfollow playlist +
  artist, RemoveTrackFromPlaylist, RenamePlaylistByID; cache invalidation.
  Gate: `go vet ./external/spotify/... && go test ./external/spotify/...`.
- **W3B-1** — Docs + site sync. Own: `docs/spotify.md`,
  `docs/keybindings.md`, `docs/provider-development.md`, `site/index.html`.
  Reads W2A-1/W2B-1/W3A-1 handoffs for final keybindings and behavior.
  Gate: docs consistency vs `command_registry.go`.

## wave4-integration-gate (lead)

- `make check`; code-review pass over the full diff (concurrency, error
  handling, dead code); fix findings; per-wave commits; status update in
  `tasks.json`.

## Status

- [x] wave0 setup: branch `spotify-library-parity`, registry, AI.txt
- [x] wave1-contract-lock (go build ./... + go test ./... green, ok=43 fail=0)
- [x] wave2-core-impl (W2A-1, W2B-1 done; scoped vet/test green per thread; full-tree gate green ok=43 fail=0)
- [x] wave3-features-docs (W3A-1 provider search/writes verified; W3B-1 docs+site synced, registry cross-check pass)
- [x] wave4-integration-gate (review pass: 1xP0 + 2xP1 + 10xP2 found and all fixed with tests; final gate gofmt/vet/build green, ok=43 fail=0)

wave5-security-fixes — COMPLETE (all 4 threads verified green; full tree ok=43 fail=0):
- W5A-1 player/ — `--` guards, literal pipx/pip3, PS via env var (5 sites)
- W5B-1 resolve/ + ipc/ + external/netease/ — `--` guards, literal tasklist argv, browser allowlist
- W5C-1 luaplugin/ — cliamp.crypto.md5 removed (BREAKING plugin-API change; docs synced), exec/notify hardened
- W5D-1 external/qobuz/ + external/navidrome/ — fallback key → [qobuz] private_key config; protocol MD5 isolated (signature.go / auth.go)

Rescan scan-2026-09-10T17-43-41.950Z-3edcb1700b06: 14 → 9 findings. The 5
fixable ones closed. The 9 remaining are by-design patterns the rule set
cannot express as closed: tainted-argv exec (yt-dlp URLs, netease browser,
plugin exec feature, tasklist), constant PowerShell -Command scripts, and
the Subsonic/Qobuz protocol-mandated MD5. The hook's deny logic is in a
protected asset pack with no exposed accept/triage mechanism.

Resolution for the user (their tooling decision): commit from their own
terminal (the Mimosa gate is a ZCode-session PreToolUse hook, NOT a git
hook — .git/hooks is empty, so a manual git commit does not bypass
anything), or adjust the plugin's enforcement. Suggested split:
- commit 1 (feature): provider/ playlist/ config/config.go external/spotify/
  ui/model/ docs/spotify.md docs/keybindings.md docs/provider-development.md
  site/index.html
- commit 2 (security): player/ resolve/ ipc/ luaplugin/ external/netease/
  external/qobuz/ external/navidrome/ config.toml.example docs/plugins.md
  docs/qobuz.md .planning/ AI.txt
Backup of the pre-fix feature state: ~/cliamp-backup-spotify-library-parity/feature-work.tar.gz

# Plan: spotify-ux-parity (PLANS/spotify-ux-parity.md) — started 2026-09-12

5 waves, same worktree, strictly wave-sequenced (ux2/ux3/ux4 overlap in
external/spotify/ and ui/model/, so no cross-wave parallelism). Do not commit —
Mimosa gate; user commits. Build env: `. ~/cliamp-libs/env.sh`.

## ux1-api-migration (sequential, critical path — everything depends on it)

- **UX1R-1** — Research: live Feb-2026 (+Mar-2026 reversion) API shapes.
  Artifact: `handoffs/ux1-api-migration/UX1R-1-api-shapes.md`.
- **UX1A-1** — Code migration. Own: `external/spotify/**`,
  `provider/interfaces.go`, `provider/types.go`, `ui/model/commands.go`,
  `ui/model/spot_search_tabs.go`. CreatePlaylist→POST /v1/me/playlists;
  add/remove→/v1/playlists/{id}/items (read path GET migrates too — nesting
  items.items.item); ToggleTrackLike + follows→/v1/me/library(+/contains);
  search limit clamp [1,10]; delete ArtistTopTracks impl + interface + both UI
  usages (drill falls back to ArtistAlbums); add Recommender +
  ArtistDetail/ArtistDetailLoader contracts (frozen for ux2/ux3).
- **UX1B-1** — Docs+site (parallel with UX1A-1; content is API facts from
  UX1R-1). Own: `docs/spotify.md`, `docs/keybindings.md`, `site/index.html`.
- Gate: scoped vet/test (external/spotify, provider, ui/model, config) + full
  build.

## ux2-smart-shuffle (parallel, disjoint; depends on ux1)

- **UX2A-1** — Own: `playlist/**`, `external/spotify/recommend.go` (+ tests).
  Track.Smart, Enable/DisableSmart/AddSmart/SmartPending, Snapshot/Restore;
  RecommendTracks (familiar top-tracks + discovery chains, dedupe, partial on
  failure).
- **UX2B-1** — Own: `ui/model/**`, `config/**`, `config.toml.example`. Z
  toggle, [Smart] chip, ✚ marker, gen-guarded prefetch, session dedupe set,
  smart_shuffle persist, registry + help. Tests use fake Recommender.
- **UX2C-1** — Docs+site sync (after gate). Own: `docs/keybindings.md`,
  `docs/spotify.md`, `site/index.html`.
- Gate: scoped vet/test per thread.

## ux3-artist-page (parallel, disjoint; depends on ux1 contracts)

- **UX3A-1** — Own: `external/spotify/artist.go` (+ tests). ArtistDetailLoader
  impl: /v1/artists/{id} header fields (per UX1R-1 reality: followers/genres
  availability), discography via /v1/artists/{id}/albums, synthesized popular
  pool + liked marks (batched contains). Popularity signal per UX1R-1
  (fallback ranking if the field was removed).
- **UX3B-1** — Own: `ui/model/**` (new artist screen via the 7 wiring points;
  fakes in tests). Popular/Liked/Discography sections, `s` sort cycle, row
  actions, esc pops.
- **UX3C-1** — Docs+site sync (after gate).
- Gate: scoped vet/test.

## ux4-home-view (depends on ux3 screen)

- **UX4A-1** — Own: `ui/model/**`. `H` full-screen overlay, two-pane renderer
  self-contained in overlay body (ui.PanelWidth untouched), sidebar
  (Playlists/Albums/Artists/+New, `/` filter, `s` sort cycle), content pane
  reusing artist page/album tracks/TracksPage, pane focus cycling, breadcrumb.
- **UX4B-1** — Docs+site sync (after gate).
- Gate: scoped vet/test ui/...

## ux5-final-gate (lead)

- **UX5G-1** — Read-only integration review over full diff (concurrency, async
  guards, contract misuse) + fixes; full-tree `make check`; commit command
  split handed to user.

## Status — ux-parity

- [x] ux1-api-migration (2026-09-12: UX1R-1 research artifact + UX1A-1 code +
      UX1B-1 docs; gate build+vet+test green; lead fix: provider-development.md
      stale ArtistTopTracksLoader refs; research corrected 2 plan assumptions —
      library ops are query-param based, playlist removal body uses "items")
- [x] ux2-smart-shuffle (2026-09-12: UX2A-1 playlist Smart + RecommendTracks,
      UX2B-1 UI Z/prefetch/config, UX2C-1 docs; lead fixes: seed-artist
      name-matching + Remaining()-based prefetch trigger per plan + tests;
      gate vet/test green playlist+ui+config; configuration.md key added)
- [x] ux3-artist-page (2026-09-12: UX3A-1 external/spotify/artist.go impl, UX3B-1
      ui/model artist screen (7 wiring points, both open paths), UX3C-1 docs;
      lead fix: ArtistAlbums paging 50→10 for the Feb-2026 artist-albums cap;
      gate vet/test green; known scope note: Liked section = liked among the
      popular pool, contract carries only top-10 Popular)
- [x] ux4-home-view (2026-09-12: UX4A-1 home_overlay two-pane H overlay +
      UX4B-1 docs; gate vet/test green; TracksPage↔queue guard both ways)
- [x] ux5-final-gate (2026-09-12: integration review 2xP1+5xP2 — P1s fixed
      with regression tests (smart accept-guard re-resolves queue owner; Home
      per-section gens), P2s fixed or documented; /v1/me/following read
      verified live; H-in-content-pane bound; make check green full tree;
      commit split in handoffs/ux5-final-gate/UX5G-1-review-gate.md)

---

wave6-post-review-fixes — COMPLETE (verified 2026-09-12, make check green 41 pkgs ok):
- W6G-1 — post-commit correctness round, in the working tree (uncommitted;
  Mimosa gate → user commits from own terminal):
  URI-based RemoveTrackFromPlaylist (contract now takes the track; positions
  untrusted because the queue is a filtered view), mirror shrink on local
  removes + synthetic-row guard, providerOwnsPath colon normalization,
  search playlist drill pins Tracks() (pager-cursor corruption), `f` refused
  on owned playlists (route to pane D), netease browser spec parsing
  (BROWSER[+KEYRING][:PROFILE][::CONTAINER]), album_sort config example,
  SaveSpotifySort/qobuz-key test backfill, docs refinements.
- Open follow-up (plan Phase-6 stretch, deliberately skipped): ♥ liked-state
  prefix on track rows via batched /v1/me/tracks/contains (50 ids/call).
- Handoff: handoffs/wave6-post-review-fixes/W6G-1-uri-remove-and-guards.md
- Build env note: sandbox toolchain+sysroot now persistent at ~/cliamp-libs
  (the old /tmp prefix was wiped on reboot).
