# UX3C-1 — Docs + site (artist page)

Wave ux3 thread C. Docs-only; landed uncommitted on `spotify-library-parity`
alongside UX3A-1/UX3B-1. No code edits, no git operations.

## Files changed

- `docs/keybindings.md`
- `docs/spotify.md`
- `site/index.html`

(Plus this handoff.)

## What was documented and where

### docs/keybindings.md

- New `## Artist page` section after "Search results overlays": opening
  paths (search artist tab, provider browser artist list — "providers that
  support it — Spotify; others keep the album-list drill-down"), header
  (followers, genres, follow state), sections (Popular / Liked Songs /
  Discography), and the mode-scoped key table.
- "Search results overlays" `Enter` row: artist now "open[s] the highlighted
  artist's page (Spotify; other providers load their album list)".
- "Provider browser (`N` key)" `Enter` row: artists open the artist page
  "where supported — Spotify; otherwise their albums or tracks".

Key table verified 1:1 against `handleArtistKey` / `handleArtistDrillKey` in
`ui/model/artist_screen.go` (and the `commandModeArtist` registry entries in
`ui/model/command_registry.go`):

| Docs row | Code case |
|---|---|
| `↑↓`/`jk` move (wraps, headers skipped) | up/k/ctrl+p, down/j/ctrl+n |
| `Ctrl+U`/`Ctrl+D` page | ctrl+u, ctrl+d (artistListVisible budget) |
| `g`/`G`, Home/End | g/home, G/end |
| `Enter`/`l` play+enqueue section / drill album | enter, l → artistPlayFrom or fetchArtistAlbumTracksCmd |
| `s` cycle Popular sort popularity→recency→liked-first | s → (sort+1)%artistSortCount, labels "popularity"/"recency"/"liked" |
| `f` follow/unfollow | artistToggleFollow → followArtistCmd |
| `a` append, `q` queue next | appendTrack, queueTrackNext |
| `p` add to playlist | openPlaylistPicker (shared plPicker) |
| `*` `S` like/unlike | "*", "S" → likeTrack |
| `Ctrl+X` expand | ctrl+x → toggleExpandedView |
| `Esc`/`Backspace`/`←`/`h` pop level | esc, backspace, h, left |

Trailing paragraph covers the Discography album drill (same row actions, Esc
returns to sections with cursor preserved).

### docs/spotify.md

- Search drill paragraph: "an artist opens their artist page" (was the wave-ux1
  "album list"/"top tracks" phrasing); breadcrumb example shortened to
  `Album — Rumours` (the artist level no longer renders as a drill crumb for
  Spotify).
- New `## Artist page` section: open paths, header (follower count, genres,
  `Following` marker), the three sections with honest Liked Songs scoping
  ("liked tracks among the artist's popular picks (not your complete
  liked-songs-by-artist list)"), `s` sort cycle, Enter play+enqueue-section
  semantics, row actions, Esc pops.
- Honesty sentence (plan-required, user-voiced, no endpoint paths): "Spotify's
  API doesn't expose play counts, so the Popular ordering uses its popularity
  score instead, and follower and genre data come from fields Spotify has
  deprecated but still returns."
- Library Browser: "By Artist" bullet now opens the artist page (was "loads
  every track across all their releases"); "By Artist / Album" bullet says the
  artist page's Discography replaces the album-list drill-down (verified in
  `keys_nav.go`: the `ArtistDetailLoader` gate precedes the mode check, so both
  nav artist modes open the page for Spotify).
- Library Browser "Artist or album list" `Enter` row split: artist list opens
  the page, album list drills in.
- Write operations table: `*`, `S`, `p`, `f` "Where" cells extended with
  "artist page".

### site/index.html

- Spotify source card: added "open artist pages (popular tracks, liked songs,
  full discography)" to the browse clause. No API/prefetch details, no Home
  view, no Smart Shuffle internals.

## Consistency notes

- docs/ and site/ carry the same feature set: artist page exists, opened from
  search artist tab and nav browser artist lists, Popular/Liked Songs/
  Discography sections, follow, sort cycle, drill+Esc, row actions.
- Provider-scoping kept everywhere: keybindings.md says Spotify opens the
  page while other providers keep the album-list/tracks drill — no promise of
  artist pages for every provider. spotify.md/site are Spotify-scoped docs so
  they state it directly.
- docs/navidrome.md still describes the album-list drill for its browser —
  correct, Navidrome does not implement `ArtistDetailLoader` (out of write
  scope, intentionally untouched).
- Excluded per instructions: Home view, Smart Shuffle internals,
  prefetch/API/endpoint details (the honesty sentence names no endpoints).
- Verified code before writing: `ui/model/artist_screen.go`,
  `ui/model/keys_nav.go` (nav gate), `ui/model/spot_search_tabs.go` (search
  gate), `ui/model/command_registry.go` (registry), `ui/model/state.go`
  (artistSortLabels), `ui/model/inline_overlays_artist.go` (section names
  "Popular (by <sort>)" / "Liked Songs" / "Discography", "N followers",
  "Following").
