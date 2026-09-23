# Spotify UX parity — unified roadmap (5 waves, same worktree)

> Handoff artifact for executing agents. Created 2026-09-12 by the planning
> session. This plan is NOT started — verified: no `Recommender`, no
> `ArtistDetail`, no `Track.Smart`/`AddSmart`, no recommend.go/artist.go/home
> view exist in the tree as of writing.

## Executor notes (read first)

- Build env: `. ~/cliamp-libs/env.sh` before ANY go/gofmt command (toolchain +
  cgo audio sysroot are persistent there now; the old /tmp prefix is dead).
- Do NOT run `git commit`/`git add` — the Mimosa PreToolUse hook force-blocks
  commits on by-design findings; the user commits from their own terminal.
- The working tree holds uncommitted `wave6-post-review-fixes` from a prior
  agent (see ROADMAP.md wave6 section); the user commits that first. Do not
  revert it.
- Conventions: read AGENTS.md. Scoped go commands per thread while parallel
  agents run (`go vet ./<owned>/...`, `go test ./<owned>/...`); full-tree
  `make check` only at wave gates. Table-driven tests; mocked-transport
  pattern in external/spotify/*_test.go; fakes in ui/model tests.
- Structure: wave-sequenced; parallel threads only on disjoint scopes
  (external/spotify + playlist vs ui/model vs docs). ui/model waves (3 and 4)
  are strictly sequential — wave 3's screen is the component wave 4 embeds.

## Wave 1 — February 2026 API migration (prerequisite for everything)

`external/spotify/**` + tests. FIRST step: confirm the new `/me/library` and
`/playlists/{id}/items` request/response body shapes from the live reference
pages linked off
https://developer.spotify.com/documentation/web-api/references/changes/february-2026
(docs restructured; old URLs 404).

1. `CreatePlaylist` → `POST /v1/me/playlists` (drop the userID dependency).
2. `AddTrackToPlaylist`/`AddTracksToPlaylist` → `POST /v1/playlists/{id}/items`
   (same `{"uris":[...]}` body, 100-URI chunks); `RemoveTrackFromPlaylist` →
   `DELETE /v1/playlists/{id}/items` (same `{"tracks":[...]}` body; keep the
   URI-based resolution wave6 added — position is caller bookkeeping only).
3. `ToggleTrackLike` → `GET /v1/me/library/contains` + `PUT|DELETE /v1/me/library`
   (types=tracks).
4. Follow/unfollow playlist & artist → `PUT|DELETE /v1/me/library`
   (types=playlists / artists, ids in body).
5. `/v1/search` limit max is now 10: clamp `SearchTracks` and `SearchAll` to
   [1,10]; docs note "up to 10 per type" (offset paging can reach ~50 if the
   UI later wants more).
6. `GET /v1/artists/{id}/top-tracks` was REMOVED with no replacement: delete
   the `ArtistTopTracks` impl (external/spotify/search.go), the
   `ArtistTopTracksLoader` interface (provider/interfaces.go), and the UI
   probe (ui/model/spot_search_tabs.go) — artist drill falls back to the
   existing discography path, which Wave 3 replaces with the real page.
7. Lock new contracts in provider/interfaces.go so later waves code against
   frozen shapes:
   - `Recommender { RecommendTracks(ctx context.Context, seed []playlist.Track, limit int) ([]playlist.Track, error) }` (Wave 2)
   - `ArtistDetail` struct in provider/types.go: `{ Info ArtistInfo; Genres []string; Followers int; Popular []playlist.Track; Discography []AlbumInfo }` — Popular tracks carry `ProviderMeta["spotify.popularity"]` ("0"-"100") and `ProviderMeta["spotify.liked"]` ("true" when saved); plus `ArtistDetailLoader { ArtistDetail(artistID string) (ArtistDetail, error) }` (Wave 3).
8. Update mocked-transport tests to the new paths/bodies; sync
   docs/spotify.md + site/index.html (search cap, removed features).

## Wave 2 — Smart Shuffle

- `Z` toggles (key verified unbound; main-mode Shift+letters are provider
  switches — Z is free). Only meaningful when the queue is owned by a
  provider implementing `Recommender` (resolve by CustomStreamer URI scheme,
  same as the like-toggle); toast otherwise.
- Sources: familiar = `GET /v1/me/top/tracks?time_range=medium_term&limit=50`
  filtered against queue URIs; discovery = `GET /v1/me/top/artists?limit=20`,
  artists absent from the queue → up to 3 chains
  `/v1/artists/{id}/albums?include_groups=album` → `/v1/albums/{id}/tracks`.
  Dedupe, cap at limit, 30s context, partial/empty on failure (never fatal).
  Impl: `external/spotify/recommend.go`.
- playlist pkg: `Track.Smart bool` (additive, like Bookmark);
  `EnableSmart/DisableSmart/Smart()`, `SmartPending()`;
  `AddSmart(tracks...)` appends into `p.tracks` + inserts into the upcoming
  order tail with the Add()-style tail reshuffle (never before current pos;
  no-op when not shuffled); `DisableSmart` removes unplayed Smart rows.
  All under `p.mu`; Snapshot/Restore carry it.
- UI: `[Smart]` header chip beside `[Shuffle]` (renderPlaylistHeader);
  `✚` marker in the 6-char row marker block; prefetch copies the
  preloadNext/tick pattern — when shuffle+smart on and remaining upcoming
  order ≤ max(2, 10%), dispatch gen-guarded `smartRecommendsCmd` →
  `smartRecommendsMsg` → validate gen + provider + queue tail identity
  (tracksAppendedMsg guard pattern) → `AddSmart`. Volume per cycle:
  `clamp(len/6, 3, 30)`. Model keeps a session set of injected URIs (no
  repeats). Persist `smart_shuffle` via the configSaver shuffle pattern.
- Threads: `playlist/`+`external/spotify/` in parallel with `ui/model/`
  (fake Recommender in tests). Registry entry + docs + site.

## Wave 3 — Artist page (the search → profile experience)

Reusable screen opened from (a) search artist-tab drill, (b) the Wave-4 Home
content pane, (c) nav browser artist enter.

- Provider (`external/spotify/artist.go`, impls `ArtistDetailLoader`): one
  `/v1/artists/{id}` (genres + followers — the API has NO bio text; header
  shows genres + follower count), discography via `/v1/artists/{id}/albums`,
  and a synthesized popular pool (top-tracks is dead): the artist's
  album+singles tracks plus `/v1/search?q=artist:"NAME"&type=track`
  (offset-paged to ~30), deduped, each carrying popularity + album release
  year in ProviderMeta; top ~10 by popularity = "Popular". Liked marks on the
  pool via batched contains checks (50/call) →
  `ProviderMeta["spotify.liked"]`. Play counts are NOT in the API — the
  popularity score is the honest substitute (document this in docs).
- UI (new inline-overlay screen; the 7 wiring points: topLevelScreen const,
  activeScreen, activeOverlay, handleKey precedence block,
  usesContentFirstLayout, clampActiveScrollState, keymap mode): header
  (name, followers, genres, follow-state via existing `f`), sectioned list —
  **Popular** (`s` cycles: popularity / recency / liked-first),
  **Liked songs** (pool tracks where liked), **Discography** (albums+singles;
  enter → album tracks via the existing drill stack). Enter plays + enqueues
  the rest of the section; row actions `*`/`p`/`q` work; esc pops.
- Threads: provider vs UI-with-fakes in parallel.

## Wave 4 — Home view (the "electron app in a terminal" surface)

New full-screen overlay (`H`), keyboard-first, with an internal two-pane
renderer (its own lipgloss JoinHorizontal inside the overlay body — the
global single-column layout and `ui.PanelWidth` global stay untouched; this
is the codebase's first two-list overlay, fully self-contained).

- Left sidebar, sectioned: **Playlists** (yours + followed, via `Playlists()`),
  **Albums** (`AlbumList` + the persisted-sort pattern), **Artists**
  (`Artists()`), and a "+ New playlist" row (existing `PlaylistCreator` +
  shared textinput infra). Sidebar-wide `/` filters all three lists
  client-side; `s` cycles sidebar ordering (recents / recently-added /
  alphabetical — client-side sorts mirroring plManager.sortMode).
- Right content pane: opens the Wave-3 artist page, album track lists
  (`AlbumTracks`), or playlist tracks (`TracksPage`, incremental). Tab /
  ctrl+arrows switch pane focus; breadcrumb like the nav browser.
- Mouse: deferred by design (zero mouse usage exists in the codebase;
  bubbletea v2 supports it — stretch goal after keyboard UX lands).
- One thread (all ui/model) + docs thread.

## Wave 5 — Docs + final gate

Per-wave docs/site sync is embedded above; final wave = read-only integration
review over the full diff (concurrency, async guards, contract misuse) +
fixes, full-tree `make check`, and commit commands handed to the user.

## Non-goals (API reality, do not attempt)

Native Smart Shuffle, play/view counts, artist bios, "artist pick",
per-track suggestion feedback, playlist track reordering — none are exposed
by the Web API. Related-artists endpoint status unconfirmed — deferred; if it
lives, it can layer into `RecommendTracks` later.

## Handoff contract

Executing agents: write per-wave handoffs under
`.planning/execution/handoffs/<wave-id>/` and update ROADMAP.md status; the
lead (or user) gates each wave with scoped vet/test then `make check`.
