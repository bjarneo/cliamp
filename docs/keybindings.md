# Keybindings

Press `Ctrl+K` from any mode, or `?` from the player, to see keybindings. The
keymap starts with actions for the screen you opened it from, followed by player
and library commands.

## Playback

| Key | Action |
|---|---|
| `Space` | Play / Pause |
| `s` | Stop |
| `>` `.` | Next track |
| `<` `,` | Previous track |
| `Left` `Right` | Seek -/+5s |
| `Shift+Left` `Shift+Right` | Seek -/+30s (configurable) |
| `N` then `j` | Seek to N×10% of the track (e.g. `7j` jumps to 70%, `0j` to the start) |
| `+` `-` | Volume up/down |
| `]` `[` | Speed up/down (±0.25x) |
| `m` | Toggle mono |
| `Ctrl+J` | Jump to time |

## Navigation

| Key | Action |
|---|---|
| `Tab` | Cycle visible controls (Playlist / EQ / Source / Speed on full and compact layouts) |
| `j` `k` / `Up` `Down` | Playlist scroll / EQ band adjust (wraps around) |
| `PageUp` `PageDown` / `Ctrl+U` `Ctrl+D` | Scroll playlist/file browser by page (outside text input) |
| `Home` `End` / `g` `G` | Go to top/end of playlist/file browser |
| `Shift+Up` `Shift+Down` | Move track up/down in playlist/queue |
| `h` `l` | EQ cursor left/right |
| `Enter` | Play selected track |
| `/` | Search playlist (navigate results with `↑` `↓` / `Ctrl+N` `Ctrl+P`; `Ctrl+U` clears the query) |
| `Ctrl+X` | Expand/collapse playlist |
| `Ctrl+Z` | Undo the last playlist removal or queue clear |
| `o` | Open file browser |
| `b` `Esc` | Back to provider |

At the minimal `40x10` layout, `Tab` keeps playback focus on the playlist, so
EQ, source, and speed settings cannot be changed accidentally. `Esc` still
opens the separate, visible provider-list view.

## Text Input

Playlist and native-provider search, URL, playlist-name, keymap, jump, and
Home filter/new-playlist fields support these editor keys:

| Key | Action |
|---|---|
| `Left` `Right` / `Home` `End` | Move cursor |
| `Backspace` `Delete` | Delete before/at cursor |
| `Ctrl+W` | Delete previous word |
| `Ctrl+U` | Clear text before cursor |


## EQ and Appearance

| Key | Action |
|---|---|
| `e` | Cycle EQ preset |
| `t` | Choose theme |
| `v` | Cycle visualizer |
| `Ctrl+V` | Pick visualizer from a list (live preview) |
| `V` | Full screen visualizer |
| `Ctrl+H` | Toggle album headers |

Theme and visualizer pickers support `/` filtering. While browsing, arrow keys
preview the highlighted option, `Enter` keeps it, and `Esc` restores the option
active when the picker opened. While typing a filter, `Enter` finishes it and
`Esc` clears it.

## Features

| Key | Action |
|---|---|
| `f` | Toggle bookmark ★ on selected track (or favorite radio station in radio browser) |
| `Ctrl+F` | Search — active provider's native search (Spotify, Qobuz, Navidrome, Jellyfin, Emby, Plex, NetEase, Local) or YouTube fallback. Available from playlist and provider-browser views. |
| `u` | Load URL (stream/playlist) |
| `y` | Show or close lyrics |
| `r` | Retry lyrics lookup while lyrics are open |
| `i` | Show track metadata (`↑`/`↓` scrolls) |
| `Ctrl+S` | Save track to `~/Music/cliamp` |
| `w` | Write the highlighted track/selection to a playlist — local playlists always, plus the owning provider's playlists (a "Spotify Playlists" section, with new-playlist creation) when the tracks come from it; selections are added in batches |
| `N` | Open the active provider browser when available |
| `H` | Open the Home view — the active provider's library in a two-pane browser |
| `L` | Browse local playlists (with cliamp radio) |
| `R` | Open radio provider |
| `S` | Open Spotify provider |
| `P` | Open Plex provider |
| `J` | Open Jellyfin provider |
| `E` | Open Emby provider |
| `Y` | Open YouTube provider |
| `C` | Open SoundCloud provider |
| `M` | Open NetEase provider |
| `Q` | Open Qobuz provider |

## Playlist and Queue

| Key | Action |
|---|---|
| `a` | Toggle queue (play next) |
| `A` | Queue manager |
| `x` | Remove the highlighted track from the current playlist — and from the remote playlist the queue mirrors (Spotify) |
| `*` | Like/unlike the highlighted track on its provider (Spotify) |
| `p` | Playlist manager |
| `r` | Cycle repeat (Off / All / One) |
| `z` | Toggle shuffle |
| `Z` | Toggle Smart Shuffle — recommended tracks mix into the end of the queue (Spotify queues; rows marked ✚) |

### Inside the playlist manager

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor |
| `/` | Filter (incremental); `Esc` clears |
| `Enter` / `→` | List screen: open the highlighted playlist · Tracks screen: play the **highlighted** track |
| `p` | Tracks screen: play all from the top |
| `a` | List: add the now-playing track to the highlighted playlist. Tracks: mark/unmark all visible tracks. |
| `w` | List: save the current queue through the playlist picker. Tracks: copy marked/highlighted tracks to another playlist. |
| `Space` | Tracks: mark/unmark highlighted track and advance |
| `[` `]` | Tracks: move highlighted track and save the playlist |
| `s` | Tracks: sort and save, cycling `track`, `title`, `artist`, `album`, `artist+album`, `path` |
| `o` | Tracks: open file browser to add files to this playlist |
| `r` | List: rename the playlist |
| `d` | List: delete playlist (confirms). Tracks: remove marked tracks, or highlighted track when none are marked |
| `u` | Undo the last manager edit |
| `←` `Backspace` `h` | Tracks screen: go back to the list |
| `Esc` | Close the playlist manager or go back |

Shift-letter keys are reserved for provider switching, so playlist-manager track actions use lowercase or punctuation keys.

## File browser

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor |
| `←` `→` / `h` `l` / `Enter` | Back / open directory or file |
| `/` | Filter files |
| `Space` | Select or unselect file/directory |
| `a` | Select/unselect all visible audio files |
| `R` | Replace the current queue with selected files (confirm when it is non-empty) |
| `w` | Write selected files to a local playlist |
| `~` `.` | Jump to home / current working directory |
| `Esc` `o` | Close file browser |

## Provider browser (`N` key)

When you press `N` to drill into a provider (Navidrome, Plex, Jellyfin, Emby, Spotify, Qobuz, YouTube Music), the album/artist/track screens use:

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps top↔bottom) |
| `←` `→` / `h` `l` | Back / drill in |
| `/` | Filter the visible list (search bar appears under the title) |
| `Enter` | Open the highlighted artist (artist page where supported — Spotify; otherwise their albums or tracks) or album · play the highlighted track and queue the rest of the visible list |
| `R` | Replace the queue with all visible tracks (start from the top, confirm when non-empty) |
| `a` | Append all visible tracks to the queue |
| `q` | Queue the highlighted track to play next |
| `f` | Follow/unfollow the highlighted artist (artist list; Spotify) |
| `s` | Cycle album sort (album list only) |
| `*` | Like/unlike the highlighted track (track screen; Spotify) |
| `S` `N` `P` `J` `E` `Y` `C` `M` `Q` `L` | Quick-switch to that provider without going back through the main pane. `R` replaces the queue on the track screen. |
| `Esc` `b` | Walk back one level / close the browser |

The header shows a source breadcrumb such as `Navidrome / Miles Davis / Kind of Blue / Tracks`, so the current provider and drill-down location remain visible. Track rows show right-aligned durations when the provider returns them.

## Provider playlist list

The playlists pane (visible when focus is on a provider — Spotify, Navidrome, Local Playlists, etc.):

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps) |
| `Ctrl+U` `Ctrl+D` | Scroll by page |
| `Enter` | Load the highlighted playlist's tracks into the queue |
| `/` | Filter the playlist list |
| `Ctrl+F` | Online/server search (Spotify/Navidrome/NetEase/etc.'s own search) |
| `Ctrl+R` | Refresh — re-pull the playlist list from the provider |
| `D` | Delete (owned) / unfollow (followed) the highlighted playlist — inline confirm, `Enter`/`y` confirms (Spotify) |
| `r` | Rename the highlighted playlist you own — inline input (Spotify) |
| `S` `N` `P` `J` `E` `Y` `C` `M` `Q` `L` `R` | Switch to that provider |
| `Tab` | Switch focus to EQ |
| `Esc` `b` | Back to the playlist pane |

Playlist rows show `Name · N tracks · 1h 23m` when the provider returns track counts and total duration. The header identifies the scope as `Provider / Playlists`. The currently loaded playlist is marked with a `▶` prefix. Spotify groups its playlists under section headers (`── library ──` with Your Music, Top Tracks, and Recently Played, `── your playlists ──`, `── followed playlists ──`). Large Spotify playlists load incrementally — the first 200 tracks appear immediately and the rest stream in behind a "Loading more tracks…" indicator.

## Home view (`H` key)

Press `H` from the main view to open the Home overlay: a two-pane browser for the active provider's library. The sidebar is sectioned into Playlists, Albums, and Artists — sections appear only when the provider supports them (Spotify shows all three, plus a `+ New playlist` row). The content pane opens the highlighted row: a playlist's tracks (loaded incrementally), an album's tracks, or an artist's page. `H` closes Home again.

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` / `Ctrl+N` `Ctrl+P` | Move the sidebar cursor (wraps; section headers are skipped) |
| `1` `2` `3` | Jump to the first row of Playlists / Albums / Artists |
| `Ctrl+U` `Ctrl+D` | Scroll by page |
| `g` `G` / `Home` `End` | Top / end of the list |
| `Enter` / `→` (`l`) | Open the highlighted row: playlist → its tracks in the content pane · album → its tracks · artist → their artist page (`Esc` returns) · `+ New playlist` → name input |
| `Tab` `Shift+Tab` / `Ctrl+Arrows` | Switch pane focus (the content pane is focusable once a row is open) |
| `/` | Filter every sidebar list by name · `Enter` commits, `Esc` cancels and clears, `/` on a committed filter clears it |
| `s` | Cycle library order — recents / recently added / alphabetical |
| `S` | Cycle the saved-albums sort and persist it (same setting as the provider browser's `s`) |
| `H` | Close Home |
| `Ctrl+X` | Expand/collapse view |
| `Esc` `Backspace` `←` `h` | Close the content pane (focus returns to the sidebar), else close Home |

`s` reorders locally: "recently added" only changes the Albums section (it reverses the saved order) — playlists and artists keep provider order because their APIs expose no added-at. `S` is the only server-side sort; it refetches the album list and saves the choice. Shift-letter provider quick-switches don't work inside Home; close it first.

With the content pane focused, track rows carry the usual actions: `Enter`/`l` plays the highlighted track and enqueues the rest of the list, plus `a` (append), `q` (queue next), `*` (like), and `p` (add to playlist). Movement keys match the sidebar. `Esc`/`Backspace` (also `←`/`h`) pops back to the sidebar; `H` closes Home from either pane. The unfocused pane is dimmed so the active one reads at a glance.

## Search results overlays

When `Ctrl+F` opens provider search or YouTube/SoundCloud net search and you're viewing the results list:

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` / `Ctrl+N` `Ctrl+P` | Move cursor (single item) |
| `Ctrl+U` `Ctrl+D` | Scroll results by page |
| `←` `→` / `Tab` `Shift+Tab` | Switch result tab — Tracks / Albums / Artists / Playlists, each with a count (multi-type provider search: Spotify) |
| `Enter` | Play the selected track now · multi-type results: drill into the highlighted album (its tracks) or playlist (its tracks), or open the highlighted artist's page (Spotify; other providers load their album list) |
| `a` | Append the selected track to the playlist |
| `q` | Queue the selected track to play next |
| `p` | (Spotify only) Add the selected track to a Spotify playlist |
| `S` | (Spotify only) Like/unlike the selected track (track tab) |
| `f` | (Spotify only) Follow/unfollow the highlighted artist or followed playlist (Artists / Playlists tab; owned playlists are deleted via `D` in the provider pane) |
| `Esc` `Backspace` | Back to the search input · from a drill-down list, up one level |

Drill-down lists (album, artist, and playlist tracks) carry the same track actions as the track tab — `Enter` play, `a` append, `q` queue next, `p` add-to-playlist, `S` like — and show a breadcrumb of the drill path above the list. Backing out of the last drill level returns to the tab bar with the previous tab and cursor intact.

## Artist page

Press `Enter` on an artist in the search results artist tab, the provider browser's artist list, or the Home view's Artists section to open their artist page (providers that support it — Spotify; others keep the album-list drill-down). The header shows follower count, genres, and follow state, and the body is sectioned into Popular, Liked Songs, and Discography.

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor over the sectioned list (wraps; headers are skipped) |
| `Ctrl+U` `Ctrl+D` | Scroll by page |
| `g` `G` / `Home` `End` | Top / end of the list |
| `Enter` / `l` | Popular or Liked row: play the track and enqueue the rest of its section · Discography row: drill into the album's tracks |
| `s` | Cycle the Popular sort: popularity → recency → liked-first |
| `f` | Follow/unfollow the artist |
| `a` | Append the highlighted track to the playlist |
| `q` | Queue the highlighted track to play next |
| `p` | Add the highlighted track to a playlist |
| `*` `S` | Like/unlike the highlighted track |
| `Ctrl+X` | Expand/collapse playlist |
| `Esc` `Backspace` (also `←` `h`) | Pop one level: album drill → artist sections, artist page → back to what opened it |

Inside a Discography album drill, track rows carry the same actions as Popular rows, and `Esc` returns to the artist sections with the cursor preserved.

## Fuzzy search

The local search boxes match fuzzily: your query characters only need to appear in order, not contiguously, and results are ranked by relevance (best match first). For example, `skr` or `saku` both find a track titled "Sakura".

This applies to:

- `/` playlist search
- `/` file browser filter
- `Ctrl+F` when the active provider is Local (your saved playlists)

Other `Ctrl+F` providers (Spotify, Qobuz, Navidrome, Jellyfin, Emby, Plex, NetEase, YouTube) send your query to their own search API, so matching there follows each service's rules.

## General

| Key | Action |
|---|---|
| `?` / `Ctrl+K` | Show keymap |
| `q` | Quit |
