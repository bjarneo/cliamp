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

At the minimal `40x10` and simplified layouts, `Tab` keeps playback focus on
the playlist, so EQ, source, and speed settings cannot be changed accidentally.
`Esc` still opens the separate, visible provider-list view.

## Text Input

Playlist and native-provider search, URL, playlist-name, keymap, and jump
fields support these editor keys:

| Key | Action |
|---|---|
| `Left` `Right` / `Home` `End` | Move cursor |
| `Backspace` `Delete` | Delete before/at cursor |
| `Ctrl+W` | Delete previous word |
| `Ctrl+U` | Clear text before cursor |


## EQ and Appearance

| Key | Action |
|---|---|
| `e` | Cycle EQ preset, including the saved Custom curve |
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
| `n` | Toggle favorite ♥ on selected track (cross-playlist; favorited tracks appear in the "Favorites" virtual playlist) |
| `Ctrl+F` | Search — active provider's native search (Spotify, Qobuz, Tidal, Navidrome, Lyrion, Jellyfin, Emby, Plex, Audiobookshelf, Mixcloud, NetEase, Local) or YouTube fallback. Available from playlist and provider-browser views. |
| `u` | Load URL (stream/playlist) |
| `y` | Show or close lyrics |
| `r` | Retry lyrics lookup while lyrics are open |
| `i` | Show track metadata (`↑`/`↓` scrolls) |
| `Ctrl+S` | Save track to `~/Music/cliamp` |
| `w` | Write the highlighted track to a local playlist |
| `N` | Open the active provider browser; on a selected Mixcloud show, jump directly to that creator's Uploads/Favorites |
| `L` | Browse local playlists (with cliamp radio) |
| `R` | Open radio provider |
| `S` | Open Spotify provider |
| `P` | Open Plex provider |
| `J` | Open Jellyfin provider |
| `E` | Open Emby provider |
| `Y` | Open YouTube provider |
| `C` | Open SoundCloud provider |
| `X` | Open Mixcloud provider |
| `M` | Open NetEase provider |
| `Q` | Open Qobuz provider |
| `T` | Open Tidal provider |
| `B` | Open Audiobookshelf provider |

## Playlist and Queue

| Key | Action |
|---|---|
| `a` | Toggle queue (play next) |
| `A` | Queue manager |
| `x` | Remove the highlighted track from the current playlist |
| `p` | Playlist manager |
| `r` | Cycle repeat (Off / All / One) |
| `z` | Toggle shuffle |

### Inside the playlist manager

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor |
| `/` | Filter (incremental); `Esc` clears |
| `Enter` / `→` | List screen: open the highlighted playlist · Tracks screen: play the **highlighted** track |
| `p` | Tracks screen: play all from the top |
| `w` | List: save the current queue through the playlist picker. Tracks: copy marked/highlighted tracks to another playlist. |
| `Space` | Tracks: mark/unmark highlighted track and advance |
| `[` `]` | Tracks: move highlighted track and save the playlist |
| `s` | Tracks: sort and save, cycling `track`, `title`, `artist`, `album`, `artist+album`, `path` |
| `o` | Tracks: open file browser to add files to this playlist |
| `D` | List: open the file browser to add `[[dir]]` sources to the highlighted playlist. Tracks: open the directory-sources screen |
| `a` | List: new playlist — after naming, the file browser opens at `~`: descend with `Enter`, select folders and/or files with `Space`, confirm with `Enter`, or finish with `Esc`. Tracks: mark/unmark all visible tracks. |
| `r` | List: rename the playlist (`Recently Played` cannot be renamed) |
| `d` | List: delete playlist (confirms; `Recently Played` cannot be deleted). Tracks: remove marked tracks, or highlighted track when none are marked |
| `u` | Undo the last manager edit |
| `←` `Backspace` `h` | Tracks screen: go back to the list |
| `Esc` | Close the playlist manager or go back |

Shift-letter keys are reserved for provider switching, so playlist-manager track actions use lowercase or punctuation keys. `D` is the one exception: it opens the directory-sources screen.

#### Directory sources screen (`D` from the tracks screen)

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Navigate directory sources |
| `a` | Open the file browser to add a directory as a `[[dir]]` source |
| `d` then `y` | Remove the highlighted source (`y` confirms, anything else cancels) |
| `r` | Toggle `recursive` on the highlighted source |
| `←` `Backspace` `h` `Esc` | Back to the tracks screen |

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
| `D` | Add all selected folders — or the highlighted folder, or the directory being browsed when neither applies — as a live `[[dir]]` source to the target playlist; the browser stays open so you can add more |
| `~` `.` | Jump to home / current working directory |
| `Esc` `o` | Close file browser |

When the browser is adding to a playlist (opened with `D` from the manager's
list screen, `o` from its tracks screen, or automatically after creating a
playlist with `a`), selected folders become `[[dir]]` sources and selected
audio files are written as explicit tracks.
In this mode `Esc` acts as "done": any pending selection is committed before
the browser closes.

## Provider browser (`N` key)

When you press `N` to drill into a provider (Navidrome, Lyrion, Plex, Jellyfin, Emby, Audiobookshelf, Spotify, Qobuz, Tidal, Mixcloud, YouTube Music), the album/artist/track screens use:

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps top↔bottom) |
| `←` `→` / `h` `l` | Back / drill in |
| `/` | Filter the visible list; while filtering Mixcloud's Genres list, `Enter` searches the full server-side genre/tag catalogue |
| `f` | In Mixcloud's Genres list, favorite/unfavorite the highlighted genre locally and update `[mixcloud].styles` |
| `Enter` | Open (artists/albums) · play the highlighted track and queue the rest of the visible list |
| `R` | Replace the queue with all visible tracks (start from the top, confirm when non-empty) |
| `a` | Append all visible tracks to the queue |
| `q` | Queue the highlighted track to play next |
| `s` | Cycle album sort (album list only) |
| `S` `N` `P` `J` `E` `Y` `C` `X` `M` `Q` `T` `L` | Quick-switch to that provider without going back through the main pane. `R` replaces the queue on the track screen. |
| `Esc` `b` | Walk back one level / close the browser |

Mixcloud's browser menu contains **By Show**, **By Creator**, **By Creator / Show**, and **Genres**. Genre favorites produce Latest/Popular rows in the provider pane and show-sort menu; they do not modify the Mixcloud website account. The header shows a source breadcrumb such as `Navidrome / Miles Davis / Kind of Blue / Tracks`, so the current provider and drill-down location remain visible. Track rows show right-aligned durations when the provider returns them.

For Mixcloud, selecting a Show, a creator's Uploads/Favorites collection, or a
genre's Latest/Popular view replaces the main playlist and closes the browser.
An empty result leaves the current playlist and browser unchanged.

## Provider playlist list

The playlists pane (visible when focus is on a provider — Spotify, Navidrome, Local Playlists, etc.):

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps) |
| `Ctrl+U` `Ctrl+D` | Scroll by page |
| `Enter` | Load the highlighted playlist's tracks into the queue |
| `/` | Filter the playlist list |
| `Ctrl+F` | Online/server search (Spotify/Navidrome/NetEase/etc.'s own search) |
| `Ctrl+R` | Refresh — re-pull the playlist list from the provider; for Mixcloud, also clear the cached `/me/` identity |
| `p` | Open the playlist manager (Local pane only; create, rename, delete, add dirs/tracks) |
| `S` `N` `P` `J` `E` `Y` `C` `X` `M` `Q` `L` `R` | Switch to that provider |
| `Tab` | Switch focus to EQ |
| `Esc` `b` | Back to the playlist pane |

Playlist rows show `Name · N tracks · 1h 23m` when the provider returns track counts and total duration. The header identifies the scope as `Provider / Playlists`. The currently loaded playlist is marked with a `▶` prefix. Spotify groups its playlists under section headers (`── library ──`, `── your playlists ──`, `── followed playlists ──`). For configured accounts, Mixcloud places Your Mixcloud first (Stream, then Favorites), followed by its Browse shortcuts, public collections, Discover charts, and each locally favorited genre's Latest/Popular pair under Music Styles. Backing out of a provider-pane Browse shortcut returns directly to the provider pane.

## Search results overlays

When `Ctrl+F` opens provider search or YouTube/SoundCloud net search and you're viewing the results list:

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` / `Ctrl+N` `Ctrl+P` | Move cursor (single item) |
| `Ctrl+U` `Ctrl+D` | Scroll results by page |
| `Enter` | Play the selected track now |
| `a` | Append the selected track to the playlist |
| `q` | Queue the selected track to play next |
| `p` | (Spotify only) Save the selected track to a Spotify playlist |
| `Esc` `Backspace` | Back to the search input |

## Fuzzy search

The local search boxes match fuzzily: your query characters only need to appear in order, not contiguously, and results are ranked by relevance (best match first). For example, `skr` or `saku` both find a track titled "Sakura".

This applies to:

- `/` playlist search
- `/` file browser filter
- `Ctrl+F` when the active provider is Local (your saved playlists)

Other `Ctrl+F` providers (Spotify, Qobuz, Tidal, Navidrome, Jellyfin, Emby, Plex, Audiobookshelf, Mixcloud, NetEase, YouTube) send your query to their own search API, so matching there follows each service's rules.

## General

| Key | Action |
|---|---|
| `?` / `Ctrl+K` | Show keymap |
| `q` | Quit |
