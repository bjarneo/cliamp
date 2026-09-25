# UX4B-1 — Home view docs + site

Wave ux4 docs thread. Uncommitted on `spotify-library-parity` alongside
waves ux1-ux3, wave 6, and UX4A-1. No code edits; no git operations.

## Files changed

- `docs/keybindings.md`
  - Text Input intro extended with "Home filter/new-playlist fields".
  - Features table: `H` row added after `N`.
  - New `## Home view (H key)` section between "Provider playlist list" and
    "Search results overlays" (Artist-page table structure: intro, table,
    two trailing paragraphs).
  - Artist page intro now lists "the Home view's Artists section" as an
    entry point.
- `docs/spotify.md`
  - Controls table: `H` row after `N` (H is bound in the provider-pane
    switch).
  - New compact `## Home` section after Library Browser / Album sort order.
  - Artist page intro lists the Home view's Artists section.
  - Write-operations table: `*` and `p` "Where" columns now include
    "Home view".
- `site/index.html` — Spotify `source-desc` clause extended: "Browse with
  N or the two-pane Home view (H: playlists, albums, followed artists,
  new-playlist creation)". Matches existing density; artist pages and Smart
  Shuffle wording already present from ux3/ux2.
- This file.

## Keybinding → source mapping (verified 1:1 against handlers)

Routing: `ui/model/keys.go:242-244` (`m.home.visible` → `handleHomeKey`),
which orders ctrl+c → name screen → filter input → content pane → sidebar.

Open/close:
| Doc claim | Source |
|---|---|
| `H` opens from the main view | keys.go:799-800 (main switch), keys.go:402-403 (focusProvider block) |
| `H` closes Home (sidebar) | home_overlay.go:918-919 |
| `ctrl+c` quits | home_overlay.go:814-817 |
| no provider → toast, no-op | home_overlay.go:238-241 |

Sidebar (`handleHomeSidebarKey`, home_overlay.go:830-929):
| Doc claim | Source |
|---|---|
| up/k/ctrl+p, down/j/ctrl+n move, wrap | 849, 851 (wrap via move() 832-843) |
| ctrl+u / ctrl+d page | 855-862 / 863-870 |
| g/home, G/end top/end | 871 / 874 |
| down/ctrl+d/G/end lazy-load next album page | 854, 870, 879 → maybeLoadHomeAlbums 751-770 (within 10 rows of loaded section end, unfiltered) |
| Enter/l/right open row | 884-900: new-name 891, playlist 895, album 897, artist 899 |
| playlist → content pane, incremental | homeOpenPlaylist 708-724 (TracksPage, providerTrackPageSize=200, commands.go:489; one-shot Tracks() fallback 715-716) |
| album → AlbumTracks | homeOpenAlbum 727-737 |
| artist → artist page, Esc returns | 899 openArtistScreen; keys.go:240-241 comment + TestHomeArtistEnterOpensArtistScreen |
| `+ New playlist` → name input, Enter creates | 891 + handleHomeNameKey 1046-1073 (create 1053-1064) |
| Tab/Shift+Tab/Ctrl+arrows focus content (only when open) | 880-883 |
| `/` filter; Enter commits, Esc clears, `/` on committed clears | 901-909 + handleHomeFilterKey 1021-1043 |
| `s` cycle order recents/recently-added/alphabetical | 910-915; labels home_overlay.go:48 |
| `S` cycle persisted album sort + refetch | 916-917 → homeCycleAlbumSort 788-805 (SaveAlbumSort, offset 0 refetch) |
| Esc/Backspace/h/left pop content else close | 920-926 |
| ctrl+x expand/collapse | 846-848 |

Content pane (`handleHomeContentKey`, home_overlay.go:944-1018):
| Doc claim | Source |
|---|---|
| movement keys match sidebar | 960-989 (ctrl+x, up/k/ctrl+p, down/j/ctrl+n, ctrl+u, ctrl+d, g/home, G/end) |
| Enter/l play + enqueue rest | 990-994 (artistPlayFrom, artist_screen.go:202) |
| a append / q queue next / * like / p add-to-playlist | 995-998 / 999-1002 / 1003-1006 / 1007-1011 |
| Tab/Shift+Tab/Ctrl+arrows back to sidebar | 1012-1013 |
| Esc/Backspace/h/left pop content | 1014-1015 |
| **`H` NOT bound in content pane** | no case in 959-1016 — documented as such |

Registry (`ui/model/command_registry.go`): mode consts 47-49; main `H`
entry 112; commandModeHome entries 245-270; HomeFilter|HomeInput share the
text-editor/esc/enter entries 160, 169-170.

Sort semantics (docs state honestly per UX4A-1):
- Playlists: only alphabetical differs (homePlaylistsView 384-397).
- Albums: "recently added" reverses fetch order (homeAlbumsView 402-420,
  reversal 410-413); no server call.
- Artists: only alphabetical differs (homeArtistsView 425-438).
- Only `S` touches the server: refetch + SaveAlbumSort (788-805); same
  `album_sort` config as the nav browser's `s`.

Provider-capability claims:
- Sections omitted without AlbumBrowser/ArtistBrowser (homeSectionOmitted
  464-474); `+ New playlist` row gated on PlaylistCreator (447).
- Spotify implements all: Artists browse.go:92, AlbumList :180,
  AlbumSortTypes :299, DefaultAlbumSort :306, SaveAlbumSort :325,
  AlbumTracks :346, TracksPage pager.go:42, CreatePlaylist provider.go:657.

## Discrepancy found (docs matched code, flag for wave 5)

`command_registry.go:257` registers `H → "Close Home"` for
`commandModeHome` with no `Enabled` gate, but `handleHomeContentKey`
(home_overlay.go:944-1018) has no `H` case — with the content pane
focused, `H` is dropped. UX4A-1's handoff table said "H closes anytime".
Docs phrase it as: `H` closes Home (sidebar), and "`H` isn't bound there
[content pane], so press `Esc` or `Tab` first". If wave 5 adds `H` to
`handleHomeContentKey`, drop that clause from
`docs/keybindings.md` (Home section trailing paragraph).

## Consistency notes

- docs/ and site/ carry the same feature set: two-pane Home, `H`,
  Playlists/Albums/Artists sections, `+ New playlist` creation.
- Cross-doc link `keybindings.md#home-view-h-key` follows the existing
  anchored-link convention (cf. configuration.md → cli.md#setup-wizard);
  slug matches GitHub rules for `## Home view (H key)`.
- No stale exclusivity claims: grep for "only way"/"only place" across
  docs/ is empty; Library Browser (`N`) docs never claimed to be the sole
  browse surface. Artist-page intros in both docs now list Home as a
  third entry point.
- Deferred-by-design items omitted as instructed: mouse support, prefetch
  / TracksPage-guard internals, Smart Shuffle internals (existing Smart
  Shuffle docs from ux2 untouched).
- Other residual risks from UX4A-1 folded in tersely: sort semantics
  paragraph (spotify.md + keybindings.md), `S`-vs-`*` like callout
  (spotify.md Home), shift-letter quick-switches unavailable inside Home
  (keybindings.md Home).
- Earlier-wave uncommitted edits in these three files (Smart Shuffle,
  artist page, search-tabs changes) were preserved; diffs are additive
  only.
