# UX4A-1 — Home view (two-pane full-screen overlay)

Wave ux4, single thread. Landed uncommitted on `spotify-library-parity`
alongside the earlier uncommitted waves (ux1-ux3 + wave6 post-review fixes).
Nothing outside the write scope was touched — no `docs/**`, `site/**`,
`provider/**`, `external/**`, `playlist/**`, `config/**`.

## File inventory

New (self-contained feature; the codebase's first two-list overlay):

- `ui/model/home_overlay.go` — all state (`homeState`, content/paging/sort
  types), messages (`homeListsMsg`, `homeAlbumsMsg`, `homeArtistsMsg`,
  `homeContentMsg`, `homePageMsg`, `homeCreatedMsg`), fetch/create command
  constructors, open/close, gen guards, sidebar row model + ordering/filter,
  scroll adjusters, all key handlers, completion handlers.
- `ui/model/inline_overlays_home.go` — renderers only: `homeHeaderLine`,
  `homeHelpLine`, `renderHomeBody` (the internal `lipgloss.JoinHorizontal`),
  sidebar/content pane renderers, breadcrumb, name-input body.
- `ui/model/home_overlay_test.go` — 15 tests (see inventory below) plus a
  `homeFakeProvider` test fake (wraps `*fakeSpotifyProvider`, adds
  `AlbumBrowser` + `AlbumSortSaver` so the base fake stays off those
  contracts).

Edited (minimal diffs, 7 wiring points + registry + queue guard):

- `ui/model/model.go` — `screenHome` const + `label()` "Home", `home
  homeState` field, `activeScreen` case (after navBrowser, before
  themePicker), `usesContentFirstLayout` OR-chain.
- `ui/model/state.go` — `requestState` gains `home` (sidebar lists +
  creation) and `homeContent` (content pane + paging) counters.
- `ui/model/keys.go` — precedence block `if m.home.visible { return
  m.handleHomeKey(msg) }` (after the navBrowser block; artist screen and
  plPicker/fileBrowser/keymap/device still stack ABOVE Home, which is what
  artist-Enter and content-pane `p` rely on); `case "H"` in both the main
  switch and the provider-pane switch (`m.openHomeView()`).
- `ui/model/inline_overlays.go` — `activeOverlay` case → `homeHeaderLine` /
  `homeHelpLine` / `renderHomeBody`, same position as activeScreen.
- `ui/model/scroll.go` — `clampActiveScrollState` case → `homeMaybeAdjustScroll()`.
- `ui/model/keymap.go` — `keymapContext` case → `commandModeHome` /
  `commandModeHomeFilter` / `commandModeHomeInput`.
- `ui/model/command_registry.go` — the three mode consts; main-mode `H`
  entry; 10 `commandModeHome` entries; `commandModeHomeFilter |
  commandModeHomeInput` appended to the shared text-editor / esc-cancel /
  enter-confirm entries.
- `ui/model/update.go` — six message cases (guards first, then the
  `handleHome*` methods).
- `ui/model/providers.go` — `fetchProviderTracks` now cancels Home content
  paging of the same playlist first (the queue-side of the TracksPage guard).

## State machine

```
main view ──H──▶ HOME (library screen)
                   │  screen: homeScreenLibrary | homeScreenNewName
                   │  focus:  homePaneSidebar ↔ homePaneContent
                   │            (content focus only while content.kind != none)
                   ├─ sidebar Enter on playlist ─▶ content: playlist (paged)
                   ├─ sidebar Enter on album    ─▶ content: album tracks
                   ├─ sidebar Enter on artist   ─▶ artist screen ON TOP
                   │                                (esc there returns to Home)
                   ├─ sidebar Enter on "+ New playlist" ─▶ name input screen
                   │        (enter creates → toast → list refresh → cursor fixup)
                   ├─ Esc/Backspace/h/left: filter input? cancel it;
                   │     else content open? pop it (focus → sidebar);
                   │     else close Home
                   └─ H anywhere in the library screen closes Home
Text inputs (filter, new-playlist name) claim keys before panes; `H`/`S`
typed into them are text, not commands.
```

Open (`openHomeView`) captures `m.provider` into `home.prov` (Home stays
consistent even if the active provider changes underneath), resets all state,
reuses the provider pane's cached `m.providerLists` (synthetic rows filtered
out via `isSyntheticProviderRow`) when it belongs to the same provider and is
settled, and gen-guarded-fetches whatever is missing under one shared
`requests.home` generation (`tea.Batch` of lists/albums/artists as needed).
`m.provider == nil` is the only "provider supports none" case (Playlists is
in the base contract, so it is always available): H toasts "No active
provider" and no-ops. Close bumps both Home generations, killing in-flight
sidebar fetches and the content page chain.

## Sort-mode semantics per section (`s` cycles, all client-side)

| Section | recents (default) | recently added | alphabetical |
|---|---|---|---|
| Playlists | provider order (Spotify's `/me/playlists` is already most-recent-first; there is no created-at in the API) | same as recents — no added-at data exists; documented rather than invented | by name, case-insensitive, stable |
| Albums | fetch order under the persisted provider sort (see `S` below) | reverse of the fetch order — the honest approximation of added-at the data gives; no server call is made | by name, case-insensitive, stable |
| Artists | provider order | same as recents — followed-artists lists expose no added-at | by name, case-insensitive, stable |

The provider-side album sort is a separate, orthogonal control mirrored from
the nav browser: `S` cycles `AlbumSortTypes()`, calls `SaveAlbumSort`
(`AlbumSortSaver`), refetches page 0 of `AlbumList`, and toasts the label.
`home.sortType` seeds from `DefaultAlbumSort()` at open (the persisted-sort
pattern). Album pages lazy-load (nav-browser pattern: within 10 rows of the
loaded section end, unfiltered only, `navAlbumPageSize` chunks).

## The TracksPage guard (both directions)

Wave-6 note honored verbatim: a `TracksPage` read rewrites the provider's
per-playlist page cursor, so Home and the queue must never page the same
playlist concurrently.

- Home → queue direction (`homeQueueConflict`, checked before Enter pages and
  again before arming the chain after page 1): if the queue is incrementally
  paging (`m.trackPaging.active` + same playlist + same provider) or has a
  first load in flight (`m.provLoading` + `activeProviderPlaylistID`), the
  content pane falls back to one-shot `Tracks()` exactly like the search
  drill (`fetchHomePlaylistTracksCmd`).
- Queue → Home direction (`homeContentPaging`, called from
  `fetchProviderTracks` in providers.go): if Home is mid-chain on the
  playlist the queue is about to load, Home's chain is cancelled (gen bump +
  paging zeroed) before the queue issues its offset-0 read. Already-loaded
  Home tracks stay visible.

Home's own paging chain runs under the open's `requests.homeContent`
generation; every `homePageMsg` re-validates gen + provider + kind + id +
`msg.offset == paging.offset`. Esc-pop of the content pane, closing Home, or
opening another row supersedes the chain.

## Key table (Home library screen)

| Key | Action |
|---|---|
| up/k/ctrl+p, down/j/ctrl+n | move sidebar cursor (or content cursor when content focused); wraps |
| ctrl+u / ctrl+d | page by pane budget (down/G also trigger the lazy album page) |
| g/home, G/end | top / end of the active pane's list |
| Enter / l / right (sidebar) | open row: playlist → content pane (paged), album → AlbumTracks, artist → `openArtistScreen` (artist screen on top), "+ New playlist" → name input |
| Enter / l (content) | play row + enqueue remainder (`artistPlayFrom`, 500 cap); a/q/*/p mirror the artist drill actions |
| Tab / Shift+Tab / Ctrl+arrows | cycle sidebar ↔ content focus (content only focusable while something is open); never leak to the main view while Home is open |
| `/` | open the sidebar-wide filter input (filters all three lists by name, case-insensitive); Enter commits, Esc cancels+clears, `/` on a committed filter clears it |
| s | cycle library order (recents / recently added / alphabetical) |
| S | cycle provider album sort + `SaveAlbumSort` (Albums section only) |
| H | close Home (works from the main view to open, and inside Home to close) |
| Esc / Backspace / h / left | cancel filter → pop content → close Home (in that precedence) |
| ctrl+x | expand/collapse view |
| ctrl+c | quit |

Focus indicator: pane header lines render `▸ Library · <order>` /
`▸ Home / <Section> / <name>` in the accent style when focused, `  `-prefixed
dim otherwise (mirrors the main `▸─ Playlist` header convention).

## Test inventory (`ui/model/home_overlay_test.go`)

- `TestHomeToggleOpenClose` — H open (screen + content-first layout), H close.
- `TestHomeStaleGenDropped` — close supersedes in-flight sidebar fetches.
- `TestHomeSectionsRender` — two-pane body: section headers + counts, rows,
  "+ New playlist", content hint, height within budget, panes share lines.
- `TestHomeSectionsOmittedWithoutInterface` — playlists-only and
  no-AlbumBrowser providers omit the missing sections.
- `TestHomeFilterFiltersAllSections` — `/road` narrows all three lists;
  headers stay with `n/total` counts.
- `TestHomeOrderCycles` — albums distinct order in all three modes; playlists
  provider-order vs alphabetical.
- `TestHomeAlbumSortCyclePersists` — `S` advances + persists + refetches.
- `TestHomeAlbumsLazyPageOnScroll` — 105 albums: page 2 loads on end-jump.
- `TestHomeFocusSwitching` — Tab no-ops without content; Tab/ctrl+arrows cycle
  once a playlist is open.
- `TestHomePlaylistEnterPagesIncrementally` — fake TrackPager (pageLimit 2):
  `0/2/4` offset chain, queue untouched, breadcrumb.
- `TestHomePlaylistEnterFallsBackWhenQueuePaging` — queue paging the same
  playlist → one-shot `Tracks()`, zero pager reads.
- `TestHomeQueueLoadCancelsContentPaging` — mid-chain queue load cancels
  Home paging, queue's `tracksLoadedMsg` intact.
- `TestHomeAlbumEnterLoadsTracks` — album breadcrumb, tracks, Enter
  play+enqueue remainder.
- `TestHomeArtistEnterOpensArtistScreen` — artist screen on top of Home, Esc
  returns to Home.
- `TestHomeNewPlaylistCreationFlow` — input screen, create via fake
  `PlaylistCreator`, refresh, cursor fixup onto the new row, toast.
- `TestHomeEscPopsContentThenCloses` — back order.
- `TestHomeHelpLines` — help renders for library/filter/input screens.
- `TestRegistryCoversHomeKeys` — H/s/S///tab/esc/enter/* reserved for
  `commandModeHome`; H reserved in `commandModeMain`.

## Verify output (scoped, per executor notes)

```
. ~/cliamp-libs/env.sh
go build ./...            # BUILD OK
go vet ./ui/...           # clean
go test -count=1 ./ui/... # ok  github.com/bjarneo/cliamp/ui
                          # ok  github.com/bjarneo/cliamp/ui/model
gofmt -l ui               # no output
```

## Residual risks / notes for the docs thread (ux4 docs)

- **`H` availability**: works in playlist, EQ, and provider-pane focus in the
  main view; not from the speed slider or the source pill (transient adjust
  modes), nor from inside other overlays. Phrase as "from the main view".
- **Docs must state the sort semantics honestly**: "recently added" is only
  meaningful for albums (reverse of the provider's recents fetch order);
  playlists and artists keep provider order in that mode because the APIs
  expose no added-at. Do not imply server-side sort calls from `s` — only `S`
  changes the server-side album sort and persists it.
- **`S` vs `*`**: the artist screen uses `*`/`S` for like; in Home, `S` is
  the album-sort cycle and `*` alone is the content-pane like key. Worth a
  docs callout.
- **Shift+letter provider quick-switches do not work inside Home** (Home is
  self-contained by design); Esc first. Mention in keybindings if space
  allows.
- Home content-pane playlist loads do not run `resolveWrapperURLs` (PLS/M3U
  wrapper expansion) unlike the queue's pager path — Home targets rich
  providers (Spotify/Navidrome-style) whose pager results are already
  resolved. If a radio-like TrackPager ever appears, that helper should be
  added to `fetchHomeTracksPageCmd`.
- Sidebar section load failures toast and render the section as `(none)`
  rather than an inline error row; the toast is the only error surface.
- Mouse remains deferred by design (wave-4 plan); no mouse handlers exist.
- Albums count in headers reflects loaded pages; `G`-to-end lazy-loads, so a
  huge library fills in as you scroll (same as the nav browser).
