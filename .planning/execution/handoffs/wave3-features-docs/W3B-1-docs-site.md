# W3B-1 Handoff

## Status

- done

## What Changed

Docs and site brought up to date with the Spotify library-parity feature
set (browse, library rows, incremental loading, multi-type search tabs,
write operations). Auth/setup/troubleshooting content untouched.

- **docs/spotify.md**: Usage now documents the provider-pane sections
  (Library: Your Music / Top Tracks / Recently Played; Your playlists;
  Followed playlists) and incremental loading ("Loading more tracks…").
  Controls table gained `N`, `Ctrl+F`, `D`, `r`, `/`, `Ctrl+R` and its
  old incorrect "Esc / b opens provider browser" row was corrected to
  "Back to the playlist pane" (matches docs/keybindings.md). New
  "Library Browser" section (Navidrome-doc style): three modes, browser
  key tables including `f` follow-artist and `*` like, album-sort table
  (`recent`/`title`/`artist`/`year` newest first, `s` cycles, persisted
  as `[spotify] album_sort`). New "Search" section: four tabs with
  counts, tab switching, drill semantics, breadcrumb, 20-per-type,
  episodes merged into tracks. "Playlists" gained a write-operations
  table plus caveats (Premium; tracks-only likes; `x` refusal
  conditions; 403 on non-owned followed playlists; unfollow-owned
  deletes).
- **docs/keybindings.md**: `w` row covers the remote picker section and
  batching; Playlist and Queue table gained `*` and the remote-mirror
  behavior on `x`; provider-browser table gained `f` (artist list) and
  `*` (track screen); provider-playlist-list table gained `D` and `r`
  and its trailing paragraph names the Library rows + incremental
  loading; search-results overlay table gained tab switching, `S`, `f`,
  drill-aware `Enter`/`Esc` rows and a drill-list paragraph.
- **docs/provider-development.md**: capability table extended with
  `MultiSearcher`, `ArtistTopTracksLoader`, `TrackPager`, `TrackLiker`,
  `PlaylistFollower`, `ArtistFollower`, `PlaylistTrackRemover`,
  `RemotePlaylistRenamer` (exact signatures from
  provider/interfaces.go); added a note naming Spotify as reference
  implementation, `SearchResults` (provider/types.go), and
  `PlaylistInfo.Owned` semantics; updated the spotify reference line.
- **site/index.html**: Spotify source card now mentions library browse
  (`N`), search (`Ctrl+F`), like/follow, and playlist create/rename/
  delete; also fixed its stale `<kbd>F</kbd>` to `<kbd>Ctrl+F</kbd>`
  (consistent with the other provider cards).

## Files

- docs/spotify.md (edited)
- docs/keybindings.md (edited)
- docs/provider-development.md (edited)
- site/index.html (edited, Spotify source card only)
- .planning/execution/handoffs/wave3-features-docs/W3B-1-docs-site.md (this file)

## Verification

Cross-check of every newly documented key against
ui/model/command_registry.go: **pass, no mismatches.**

| Key documented | Registry entry | Result |
|---|---|---|
| `*` main/queue like | commandModeMain `*` "Like/unlike track", Enabled: playlist focus + cursor + TrackLiker | pass |
| `x` remote removal | commandModeMain `x` "Remove selected track from playlist" (same key; remote path is handler behavior) | pass |
| `D` provider delete/unfollow | commandModeProvider `D` "Delete/unfollow playlist", Destructive, Enabled: PlaylistFollower | pass |
| `r` provider rename | commandModeProvider `r` "Rename playlist", Enabled: RemotePlaylistRenamer + Owned row | pass |
| `*` nav browser like | commandModeNavBrowser `*`, Enabled: track screen + navTrackLikerAvailable | pass |
| `f` nav browser follow artist | commandModeNavBrowser `f`, Enabled: list screen, ByArtist/ByArtistAlbum modes, ArtistFollower | pass |
| `←/→`, `Tab`/`Shift+Tab` search tabs | commandModeSpotSearch "Switch result tab", Enabled: multi + results screen | pass |
| `S` search like | commandModeSpotSearch `S` "Like/unlike track", Enabled: results screen | pass |
| `f` search follow | commandModeSpotSearch `f` "Follow/unfollow artist or playlist", Enabled: Artists tab (ArtistFollower) / Playlists tab (PlaylistFollower) | pass |

Non-registry keys documented in drill lists (`a`, `q`, `p`, `Enter`) are
handler-level actions (ui/model/spot_search_tabs.go), same as the
pre-existing search-overlay rows; no registry entries exist for them and
none are claimed.

Behavior facts cross-checked against code, not just handoffs:
- `S` in multi results is track-tab gated and also active in drill
  lists (spot_search_tabs.go handleSpotTabsKey / drill handler);
  registry Enabled is screen-level (broader) — documented the tighter
  handler behavior, which matches the W2B-1 handoff table.
- Section names ("Library", "Your playlists", "Followed playlists"),
  row names (Your Music / Top Tracks / Recently Played), and sort
  labels/IDs (`recent` "Recently saved", `title`, `artist`, `year`)
  verified in external/spotify/provider.go and browse.go.
- `album_sort` persistence verified in config/config.go
  (SaveSpotifySort).
- Interface signatures copied verbatim from provider/interfaces.go;
  `SearchResults` from provider/types.go; `PlaylistInfo.Owned` from
  playlist/provider.go.

## Blockers Or Risks

- None blocking. One deliberate deviation: the task's feature facts say
  search `p` is "now batched when adding from multi-track flows" — the
  batching lives in the `w` picker path (PlaylistBatchWriter), while `p`
  in the search overlay still adds the single selected track. Docs
  attach batching to `w`, not to search `p`.
- The old spotify.md Controls row "Esc / b — Open provider browser" was
  wrong before this change (from the provider panel those keys go back
  to the playlist pane); fixed in passing since the table was being
  edited anyway.

## Next Thread Should Know

- docs/spotify.md is now the full Spotify user reference: setup (BYO
  client_id / dev-mode caveat / shared fallback), usage, library
  browser, search tabs, write operations, podcasts, troubleshooting.
  Any future Spotify UX change must update the Controls / Library
  Browser / Search / write-ops sections here, docs/keybindings.md, and
  the site's Spotify source card together.
- site/index.html carries no keybinding tables; its only
  Spotify-specific surface is the source card in the sources grid.
- The capability table in docs/provider-development.md now includes
  `PlaylistBatchWriter`'s siblings but not `PlaylistBatchWriter`
  itself (not in this task's mandated list). If a later thread
  documents it, it belongs next to `PlaylistWriter`.
