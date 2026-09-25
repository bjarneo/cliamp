# W2B-1 Handoff

## Status

- done (all 6 deliverables implemented; 1 partial descoping noted under "Deviations")

## What Changed

All UI/model behavior for Spotify library parity, coded against the frozen
wave-1 capability contracts (`provider/interfaces.go` lines 142-196). No
provider implementations were touched; every feature is gated on runtime
capability checks, so non-Spotify providers keep their existing behavior.

### 1. Nav artist rows

`navArtistLabel` (ui/model/inline_overlays_nav.go) hides the "(N albums)"
suffix when `ArtistInfo.AlbumCount <= 0`. Providers that report counts
(Navidrome etc.) render exactly as before.

### 2. Incremental provider-track loading (`provider.TrackPager`)

- `fetchProviderTracks` routes through `TracksPage(id, 0, 200)` when the
  active provider implements TrackPager; page 0 arrives as a normal
  `tracksLoadedMsg` (existing replace semantics byte-for-byte, including
  wrapper-URL expansion), then the handler chains background fetches
  `TracksPage(id, 200, 200), (id, 400, 200), ...` — the offset advances by the
  number of tracks actually returned, so providers that clamp the page size
  below 200 still load correctly. Each page arrives as
  `tracksAppendedMsg{gen, playlistID, tracks, total}`.
- Non-TrackPager providers use the old `Tracks()` path unchanged (verified by
  test). IPC paths are untouched (they call the blocking provider API).
- **Append guard (documented per spec):** a page is applied only when ALL of
  these hold: (a) `msg.gen == m.requests.tracks` and the provider is still
  active (a newer load discards stale pages); (b) `trackPaging.active` and
  `trackPaging.playlistID == msg.playlistID`; (c) queue length equals the
  expected loaded offset; (d) tail-identity check — the last previously loaded
  track's Path still sits at `offset-1`, which catches queue replacements of
  the same length. Shuffle does NOT block appends (shuffle reorders playback
  order only; row positions stay intact). Any guard trip silently deactivates
  paging — the user keeps what loaded so far and the queue is never corrupted.
- Subtle indicator: a trailing spinner line ("Loading more tracks…") is
  appended to the playlist pane while a page is outstanding (same style as the
  radio "Loading more stations…" line; no layout change).
- **UX:** loading a large playlist shows the first 200 tracks immediately
  (playback can start), remaining pages stream in within a second or two.

### 3. Multi-type search (`provider.MultiSearcher`)

- When the searched provider implements MultiSearcher, Enter on the query
  calls `SearchAll(ctx, query, 20)` (MultiSearcher is preferred over
  Searcher). The results screen renders a tab bar — `Tracks (N)  Albums (N)
  Artists (N)  Playlists (N)` with counts — one line above the list; the
  active tab is highlighted. All four tabs always render; empty tabs show
  "No results".
- Plain Searcher providers keep the single-list overlay, unchanged (the only
  additions there: `S` like key, and the add-to-playlist picker now skips any
  playlist ID containing a space, which generalizes the old "YOUR MUSIC"
  skip to "TOP TRACKS"/"RECENTLY PLAYED" too).
- Drill-down: enter/l on an album row → `AlbumTrackLoader.AlbumTracks`;
  artist row → `ArtistTopTracksLoader.ArtistTopTracks` when implemented, else
  `ArtistBrowser.ArtistAlbums` (album rows in that list drill into tracks —
  two levels deep); playlist row → `TrackPager.TracksPage(id, 0, 200)` when
  implemented else `Tracks(id)`.
- Drilled track lists carry the same row actions as the track tab: enter
  play, `a` append, `q` queue next, `p` add-to-playlist (existing picker
  flow), `S` like. esc/backspace/h/left pop one drill level; popping the last
  level returns to the tab bar with the previous tab AND cursor restored
  (drill levels own their cursors; the tab cursor is never touched while
  drilling).
- A one-line crumb shows the drill path, e.g. `Artist — Fleetwood Mac /
  Album — Rumours`, followed by the list.
- All fetches are generation-guarded under `requests.spotSearch` (each drill
  bumps the gen, so only the newest drill applies; stale crumb checks guard
  the fill). Loading state = spinner line; errors pop the pending level and
  surface as the overlay's inline error.

### 4. Write-operation keys

All operations resolve the owning provider by matching the track's URI scheme
against `CustomStreamer.URISchemes()` across `m.providers` (active provider
preferred) — nothing hardcodes "spotify". Toasts use the existing status bar.

### 5. pl_picker remote section

`openPlaylistPicker` now returns a `tea.Cmd`. When ALL selected tracks share
one URI scheme owned by a provider implementing `PlaylistWriter`, a second
picker section "<Provider> Playlists" is appended after the local section
(local first, unchanged when no remote applies). The remote list comes from
`m.providerLists` when that provider is active, else a background `Playlists()`
fetch (`plPickerRemoteMsg`) shows "Loading <name> playlists…" meanwhile.
Synthetic IDs (any ID containing a space) are skipped. Writing uses
`PlaylistBatchWriter` when implemented, else per-track `PlaylistWriter`.
"+ New <Provider> Playlist..." reuses the new-name input screen and creates
via that provider's `PlaylistCreator` then batch-adds. The picker closes
optimistically on dispatch; the outcome arrives via `pickerRemoteWriteMsg`
("Added N to %q" / "Created %q & added N tracks" / error toast) and refreshes
the provider pane when the target is the active provider.

## Files

Modified (ui/model/): state.go, model.go, commands.go, providers.go, update.go,
keys.go, keys_nav.go, keys_spotify_search.go, inline_overlays.go,
inline_overlays_nav.go, view.go, pl_picker.go, command_registry.go.
New: keys_spotify_write.go, spot_search_tabs.go, spotify_write_test.go,
spot_search_tabs_test.go, tracks_paging_test.go, pl_picker_remote_test.go.

## Verification

- `. /tmp/cliamp-libs/env.sh && gofmt -l -w ui`: pass (clean)
- `. /tmp/cliamp-libs/env.sh && go vet ./ui/...`: pass
- `. /tmp/cliamp-libs/env.sh && go test ./ui/... -count=1`: pass (ok 2/2)

Scoped-package commands only (`./ui/...`); no repo-wide go commands were run
(external/spotify is concurrently owned by another thread).

## Blockers Or Risks

- **Follow state is session-local.** `PlaylistFollower`/`ArtistFollower` are
  Follow/Unfollow pairs with no state query in the frozen contract, so `f`
  assumes "not followed" on first press per session (map keyed
  `kind:provider:id`). If the artist/playlist was already followed remotely,
  the first press Follows again (Spotify returns OK for idempotent follows;
  an error surfaces as a toast). If a later wave adds a state query (or
  SearchResults carries followed flags), `Model.followState` should seed from
  it.
- **Remote `x` remove:** after a successful remote removal the queue row is
  dropped only if the row still matches the removed track (path + position
  verified). If the queue changed mid-flight the remote playlist is still
  updated but the queue row remains until the user reloads. TrackCount in the
  provider pane is decremented locally instead of refetching.
- **Remote picker writes are optimistic-close** — the picker closes before the
  write completes; failures arrive as toasts (the local picker flow still
  keeps the picker open on error). Documented as a deliberate async choice.
- Nav-browser artist refresh after `f` re-fetches the artist list but resets
  its cursor to row 0 (navArtistsLoadedMsg behavior); acceptable, noted here.
- The provider pane confirm/rename states are reset on provider switch, on
  track load, and on refresh (ctrl+r).

## Next Thread Should Know

### Final keybinding table (docs thread: write user docs from this)

| Key | Context | Action | Confirm step |
|-----|---------|--------|--------------|
| `*` | Main queue (playlist focus), cursor row | Like/unlike track on its owning provider (TrackLiker). Toast "Added to liked tracks"/"Removed from liked tracks" | none |
| `S` | Search results (track tab, plain Searcher results, and drill-down track lists) | Same as `*` | none |
| `*` | Nav browser, track screen, cursor row | Same as `*` | none |
| `x` | Main queue, when queue mirrors a loaded remote playlist (PlaylistTrackRemover) | Remove track from remote playlist by position; queue row dropped on success | none (destructive but scoped to one track; refused with toast if queue shuffled or row outside the loaded mirror) |
| `D` | Provider pane, playlist row (PlaylistFollower) | Owned row: "Delete playlist %q?"; otherwise "Unfollow playlist %q?" — inline confirm pane | Enter/y confirms; any other key cancels. Toast "Deleted %q"/"Unfollowed %q"; list refreshes |
| `r` | Provider pane, owned playlist row (RemotePlaylistRenamer) | Inline rename input (prefilled, esc cancels, enter commits) | Enter commits; toast "Renamed to %q"; list refreshes. Non-owned rows: toast "Only playlists you own can be renamed" |
| `f` | Nav browser, artist list (ArtistFollower) | Follow/unfollow artist. Toast "Following X"/"Unfollowed X"; artist list refreshes | none (first press follows) |
| `f` | Search results, artist tab (ArtistFollower) | Follow/unfollow artist. Toast | none |
| `f` | Search results, playlist tab (PlaylistFollower) | Follow/unfollow playlist. Toast "Followed playlist %q"/"Unfollowed playlist %q" | none |
| left/right, tab/shift+tab | Search results (multi-type) | Switch result tab (wraps); cursor resets per tab | none |
| enter/l | Search results tabs | Track tab: play immediately. Album/artist/playlist tabs: drill down (see below) | none |
| esc/backspace | Search results | Tabs screen: back to query input. Drill screen: pop one level | none |
| enter/a/q/p | Drill-down track lists | Play / append / queue next / add-to-playlist (same as track tab) | none |

**Key-collision decisions (rationale):** the preferred like key `S` is taken
in the main queue (`S` = open Spotify provider) and in the nav browser
(Shift+letter = provider quick-switch), so the fallback `*` was taken there
(`*` was unbound and verified absent from handlers, registry, and bundled
plugin keys; `=` was not needed — it collides with volume anyway). `S` is free
inside the search overlay (no quick-switch handler there), so it stayed.
`D` on the provider pane: verified free (built-in quick-switch letters are
S/N/P/J/E/Y/C/M/Q/L/R — no D). `r` on the provider pane: lowercase `r` was
unbound there (main-mode `r` = repeat does not apply in that focus; `ctrl+r`
refresh is a different chord). `f` in the nav browser and search tabs:
lowercase `f` unbound in those handlers (provider-pane `f` favorite-toggle is
a different focus). All new keys have `commandRegistry` entries in the
correct mode; like/follow/rename/delete entries carry `Enabled` guards on the
capability interfaces; `*`/`D`/`r`/nav-`f` appear in the Ctrl+K keymap, and
`D`/`r` also show in the provider-pane context help.

### Tab bar & drill-down behavior (for docs)

- Tabs: Tracks / Albums / Artists / Playlists with live counts; switch with
  ←/→ or Tab/Shift+Tab (wraps); switching resets the cursor.
- Drill targets: album → its tracks; artist → top tracks (or the artist's
  albums when the provider has no top-tracks — enter on an album then loads
  its tracks); playlist → first 200 tracks (paged).
- Crumb line above drilled lists: "Album — Rumours" or
  "Artist — X / Album — Y" for nested drills.
- Back: esc/backspace (also h/left) pops one level; the tab bar reappears
  with its previous tab and cursor.

### Incremental loading UX (for docs)

Opening a large provider playlist shows the first 200 tracks instantly and
starts playback immediately; remaining pages stream in while a spinner line
("Loading more tracks…") shows at the bottom of the queue. Editing the queue
during the stream (removing/replacing/reordering) simply stops the stream —
no partial-append corruption is possible.

### Deviations / descoping

- The spec's "and `=` if the preferred key is taken" fallback chain for like
  stopped at `*` (free everywhere it was needed) — `=` remains volume-up.
- Like on "now-playing" specifically was implemented as like-on-the-selected-
  queue-row (`*`), matching every other row action; there is no separate
  now-playing-scoped binding.
- Search-tab follow toggles do not refresh the search results themselves
  (they are a stale snapshot); the provider pane list refreshes when the
  followed provider is active.
- No user-visible docs/site changes in this thread (owned by the docs thread
  per the wave plan).
