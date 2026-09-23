# UX3B-1 — Artist screen (inline overlay UI)

Wave ux3 thread B, part 1 (UI half). Landed uncommitted on `spotify-library-parity`
alongside the earlier uncommitted waves.

Files touched (write scope honored — no `external/spotify/**`, `provider/**`,
`playlist/**`, `config/**`, `docs/**`, `site/**`):

- New: `ui/model/artist_screen.go`, `ui/model/inline_overlays_artist.go`,
  `ui/model/artist_screen_test.go`
- Edited: `ui/model/state.go`, `commands.go`, `update.go`, `model.go`,
  `keys.go`, `keys_nav.go`, `inline_overlays.go`, `scroll.go`, `keymap.go`,
  `command_registry.go`, `spot_search_tabs.go`,
  `spotify_write_test.go` (ONE field: `artistDetails map[string]provider.ArtistDetail`
  on `fakeSpotifyProvider`)

## Wiring summary — all 7 points touched

| Point | Where |
|---|---|
| topLevelScreen const | `screenArtist` after `screenSpotSearch` in `model.go`; `label()` returns "Artist" |
| activeScreen | `case m.artist.visible: return screenArtist` — inserted between `fileBrowser` and `spotSearch` (the artist screen stacks ABOVE the search overlay and the nav browser) |
| activeOverlay | `case m.artist.visible` in `activeOverlay()` (inline_overlays.go), same position, pieces: `artistHeaderLine` / `artistHelpLine` / `renderArtistBody` |
| handleKey precedence | `if m.artist.visible { return m.handleArtistKey(msg) }` in `keys.go`, between the fileBrowser and spotSearch blocks (plPicker/fileBrowser still stack above the artist screen, which is what `p` relies on) |
| usesContentFirstLayout | `m.artist.visible` added to the OR chain in `model.go` |
| clampActiveScrollState | `case screenArtist` in `scroll.go`: drill level clamp when `m.artist.drill` is non-empty, else `artistMaybeAdjustScroll()` |
| keymap mode | `commandModeArtist` const + 7 registry entries (esc/up-down/enter/s/f/*) in `command_registry.go`; `keymapContext` case → `commandModeArtist, "Artist"` |

State: `artistScreenState` in `state.go` (prov, visible, info, loading, detail,
sort, cursor, scroll, drill `[]spotDrillLevel`, cancel func). Request counter:
`requests.artist` (`requestState`). Async pair in `commands.go`:
`fetchArtistDetailCmd` → `artistDetailMsg`, `fetchArtistAlbumTracksCmd` →
`artistAlbumTracksMsg` — both carry `gen`, `providerName`, `artistID`; handlers
in `update.go` drop stale gen, wrong provider, wrong artistID, and (for the
album msg) crumb-mismatched drill levels. 30s timeout ctx with a stored cancel
(`newArtistRequestContext` / `cancelArtistRequest`, mirroring the spotSearch
pattern). A second `openArtistScreen` supersedes the first via the gen bump
plus the artistID check.

## Open paths

- **Search artist tab** (`spot_search_tabs.go` `spotDrillFromTab`): when the
  searched provider implements `provider.ArtistDetailLoader`, Enter opens
  `m.openArtistScreen(prov.Name(), artist)` instead of pushing the album
  drill level. Providers without the interface keep the old
  `fetchSpotArtistCmd` album fallback (TestSpotSearchArtistDrillLoadsAlbums
  still pins it).
- **Nav browser artist enter** (`keys_nav.go` `handleNavArtistListKey`): same
  gate; the nav browser stays mounted underneath with its cursor intact.
  Providers without the interface keep the old tracks/albums drill.
- The opening surface is never torn down, so Esc returns to it unchanged.

## Key handling table (artist screen, top level)

| Key | Action |
|---|---|
| up/k/ctrl+p, down/j/ctrl+n | move cursor over selectable rows (wraps; headers skipped) |
| ctrl+u / ctrl+d | page by list budget |
| g/home, G/end | top / end of the sectioned list |
| Enter / l | Popular+Liked row: play it + enqueue the section remainder after it (500 cap, nav-browser pattern, screen stays open). Discography row: push album drill (`fetchArtistAlbumTracksCmd`), render crumb + tracks; Enter there plays + enqueues the album remainder |
| s | cycle Popular sort: popularity → recency → liked-first → popularity (toast "Popular sorted by X"; the Popular header always shows the active sort) |
| f | follow/unfollow via `provider.ArtistFollower` (see below) |
| a / q | appendTrack / queueTrackNext on track rows (same handlers as the search drill; screen stays open — nav-browser convention) |
| * / S | likeTrack on track rows (same handler as `*` in nav / `S` in search drill) |
| p | add-to-playlist via the shared `openPlaylistPicker` (plPicker) — see deviations |
| ctrl+x | toggle expanded view |
| Esc / Backspace / h / left | pop one level: inside a drill → back to artist sections (cursor preserved); at top level → close the screen, return to the opener |

No provider quick-switch (Shift+letters) on this screen — matches the spot
search overlay, and S/P/L would collide with row actions.

## Follow-state decision

Same knowledge source as every other `f` handler (`keys_spotify_write.go`):
the provider interfaces expose no follow-state query, so state is the
session-local `m.followState[followKey("artist", prov, id)]` map — first
toggle of a session assumes unfollowed. The toggle dispatches the existing
`followArtistCmd` → `artistFollowedMsg` handler unchanged, which already
toasts ("Following X"/"Unfollowed X"), updates the map, and refreshes a
visible nav artist list. The artist screen header info line shows a
"Following" part only when the map says true (never renders a "not followed"
state — consistent with the provider thread's note that absence is unknown,
not false).

## Sort semantics

Applied to a copy of `detail.Popular` (`artistPopular()`); the Liked section
derives its order from the same sorted pool (`artistLiked()` filters it), so
one sort governs both.

| Mode | Ordering |
|---|---|
| popularity (default) | `ProviderMeta[MetaSpotifyPopularity]` desc, stable; missing/malformed meta = 0 |
| recency | `Track.Year` desc, tie by popularity desc |
| liked-first | `ProviderMeta[MetaSpotifyLiked]=="true"` rows first, then popularity desc |

`s` resets cursor/scroll to 0 (plManager sort convention) and toasts.

## Render layout

- Header line: `sepHeaderN("Artist — NAME", cursor+1, rowCount)` (drill level
  count while drilled in).
- Body: one dim info line — `N followers` (plain number; no humanize helper
  exists in the codebase) · genres joined ", " · "Following" — omitted
  entirely when the provider supplied none; then the sectioned list with
  `labeledSeparator` headers styled like the library/plPicker section rows:
  **Popular (by <sort>)**, **Liked Songs**, **Discography**. Track rows match
  the search drill rows (`Artist - Title`); album rows match spot album rows
  (`Name — Artist (Year)`). Empty sections are omitted (a lone header renders
  nothing useful); all-empty renders "No popular tracks or albums".
- Loading body: spinner "Loading artist…". Error: toast "Artist load failed:
  …" + immediate pop (the failed-drill convention adapted to a screen with no
  parent level inside itself).
- Scroll: cursor over selectable rows, scroll window counts section header
  lines (`artistRenderedRows`, mirroring `albumSeparatorRows`/`playlistScroll`).
  Drill scroll uses `clampScroll` over `spotDrillCount` with the crumb-line
  budget minus 1, exactly like `spotDrillMaybeAdjustScroll`.

## For wave-4 Home

Open it with (method on `*Model`, returns the fetch `tea.Cmd` or nil when the
provider lacks the capability — nil also toasts "Artist profiles are not
supported"):

```go
cmd := m.openArtistScreen(providerName string, artist provider.ArtistInfo)
```

- Resolve the provider by display name via `m.providerNamed` (active provider
  first, then `m.providers`). The Home sidebar already holds the provider and
  its name — pass `prov.Name()`.
- Messages the screen owns (already handled in `update.go`; Home needs no new
  handling): `artistDetailMsg{artistID, detail, err, providerName, gen}` and
  `artistAlbumTracksMsg{artistID, crumb, tracks, err, providerName, gen}`.
  Guards: `m.requests.artist` gen + providerName + artistID (+ crumb for the
  album msg). Home can simply `return tea.Batch(m.openArtistScreen(...))`
  from its Enter handler; keeping Home mounted underneath gives Esc-back for
  free.
- The screen renders in the playlist region via the standard overlay pieces;
  a Home two-pane right side can either show this overlay on top (like the
  search/nav do) or reuse `m.artistRows()`/render helpers directly.

## Test inventory (`ui/model/artist_screen_test.go`, 12 tests)

1. `TestArtistScreenOpenFromSearchArtistTab` — opens screen + `artistDetailMsg`
   (not the album fallback: spot drill stays empty); search overlay mounted
   underneath, tab/cursor intact.
2. `TestArtistScreenOpenFromNavBrowserArtist` — nav enter opens it; Esc pops
   back to the nav list, cursor intact.
3. `TestArtistScreenLoadingState` — loading render right after open.
4. `TestArtistScreenStaleGenDropped` — second open supersedes the first.
5. `TestArtistScreenWrongProviderDropped` — providerName guard.
6. `TestArtistScreenErrorPopsWithToast` — toast + pop, returns to search.
7. `TestArtistScreenSectionsInOrder` — info line, headers in order
   Popular < Liked Songs < Discography, row labels.
8. `TestArtistScreenSortCycles` — fixture with distinct popularity/year/liked
   orderings; all three modes + wrap-around asserted.
9. `TestArtistScreenEnterPopularPlaysAndQueues` — playlist gains whole Popular
   section, index at played row, "+2 queued" toast.
10. `TestArtistScreenEnterLikedQueuesLikedSection` — liked row enqueues only
    the liked section.
11. `TestArtistScreenDiscographyDrillAndBack` — Enter pushes album drill
    (`artistAlbumTracksMsg`), crumb renders, Esc → artist sections (cursor
    preserved), Esc → search results.
12. `TestArtistScreenFollowToggle` — f follows, f unfollows (fake calls +
    toasts) via the existing `artistFollowedMsg` path.
13. `TestArtistScreenRowActionsMirrorSearchDrill` — `q` queues next via the
    shared handler, screen stays open.
14. `TestRegistryCoversArtistKeys` — s/f/* registered for commandModeArtist.

Fake plumbing: `ArtistDetail` method defined on the shared
`fakeSpotifyProvider` in the test file (fake ArtistFollower already existed);
new `artistDetails map[string]provider.ArtistDetail` field added to the struct
in `spotify_write_test.go`. A nil map keeps the fake OFF the contract so the
older drill-fallback tests are untouched.

## Verify output (scoped per thread rules)

```
. ~/cliamp-libs/env.sh
go build ./...                          # OK
go vet ./ui/...                         # OK
go test -count=1 ./ui/...               # ok github.com/bjarneo/cliamp/ui
                                        # ok github.com/bjarneo/cliamp/ui/model
gofmt -l ui                             # (no output)
```

## Deviations from the letter of the task (deliberate, documented)

- **Discography drill stack**: it reuses the search drill *machinery*
  (`spotDrillLevel` crumbs/loading/cursor/scroll + esc-pops-one-level +
  crumb-guarded gen fetch) but lives in `m.artist.drill`, NOT
  `m.spotSearch.drill`. Literally pushing onto the spot stack cannot work for
  the nav/Home open paths: `spotDrillLoadedMsg` is guarded by
  `isCurrentSpotRequest`, which requires `spotSearch.visible` and its own gen
  counter, and the spot overlay is not mounted on those paths.
- **`p` row action** routes through the shared `openPlaylistPicker` (plPicker)
  instead of the spotSearch-internal playlist screen — the artist screen is
  not part of the spotSearch state machine, and the plPicker is the reusable
  add-to-playlist surface (queue `w`, playlist manager) that stacks above
  this overlay. Same destination handlers; picker closes back onto the artist
  screen.
- **`a`/`q`/Enter keep the artist screen open** (nav-browser convention for a
  browse surface) instead of closing it like the transient search overlay
  does; the handlers (`appendTrack`, `queueTrackNext`, play+enqueue) are the
  same ones the search drill dispatches.
- **Section jumps**: no dedicated section-skip keys invented; g/G (top/end)
  exist because plManager/theme pickers already use home/g + end/G in exactly
  this shape.

## Residual risks

- The provider thread notes `Popular` is capped at 10, so "Liked Songs" only
  surfaces liked rows within that top-10 pool (the full pool is not
  exported). If a fuller liked list is wanted later, the provider needs a new
  surface — UI code would not change (`artistLiked` just filters).
- Zero-popularity/missing meta rows sort as 0 (they sink in popularity mode
  but keep pool order among themselves) — matches the provider's stable
  ordering, but recency mode can interleave them above zero-year rows; Year 0
  sinks in recency.
- `p`'s remote section depends on `m.providerLists` freshness (existing
  plPicker behavior, unchanged).
- Sticky section headers are not re-shown when the scroll window opens
  mid-section (headers render only when a section's first visible row is in
  window); the cursor row is always kept visible regardless.
- browse.go `ArtistAlbums` limit=50 paging gap flagged by UX3A-1 (out of my
  scope) can affect the nav fallback path for providers without
  ArtistDetailLoader only.
