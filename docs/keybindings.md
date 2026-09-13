# Keybindings

Press `Ctrl+K` in any mode, or `?` in the player, to view keybindings. The
keymap first shows commands for the active screen. It then shows player and
library commands.

## Playback

| Key | Action |
|---|---|
| `Space` | Play / Pause |
| `s` | Stop |
| `>` `.` | Next track |
| `<` `,` | Previous track |
| `Left` `Right` | Seek -/+5s |
| `Shift+Left` `Shift+Right` | Seek -/+30s (configurable) |
| `N` then `j` | Seek to N * 10% of the track (for example, `7j` jumps to 70%, `0j` to the start) |
| `+` `-` | Volume up/down |
| `]` `[` | Change speed by 0.25x |
| `m` | Toggle mono |
| `Ctrl+J` | Jump to time |

## Navigation

| Key | Action |
|---|---|
| `Tab` | Cycle visible controls: Playlist / Source / Volume / EQ / Shuffle / Repeat / Speed / Playlist |
| `Shift+Tab` | Cycle the same controls in reverse |
| `j` `k` / `Up` `Down` | Move playlist cursor (wraps); see focused settings below for control actions |
| `PageUp` `PageDown` / `Ctrl+U` `Ctrl+D` | Scroll playlist/file browser by page (outside text input) |
| `Home` `End` / `g` `G` | Go to top/end of playlist/file browser |
| `Shift+Up` `Shift+Down` | Move track up/down in playlist/queue |
| `h` `l` | Adjust the focused setting (EQ: select band) |
| `Enter` | Play selected track |
| `/` | Search playlist (navigate results with `↑` `↓` / `Ctrl+N` `Ctrl+P`; `Ctrl+U` clears the query) |
| `Ctrl+X` | Expand/collapse playlist |
| `Ctrl+Z` | Undo the last playlist removal or queue clear |
| `o` | Open file browser |
| `b` `Esc` | Back to provider |

In full and compact playback layouts, the first `Tab` from the playlist focuses
Source (`SRC`); `Shift+Tab` starts at Speed when it is visible. Source is skipped
when only one provider is available. Closing Settings skips EQ and Speed. A
short sidebar can omit Shuffle and Repeat together, removing both Tab stops.
Metadata is read-only and never a separate Tab stop.

In the minimal (`40x10`) and simplified layouts, `Tab` and `Shift+Tab` keep
playback focus on the playlist, even though simplified mode hides the list.
`Esc` still opens the separate provider-list view. Below `40x10`, only a resize
message is shown.

### Focused Settings

| Control | Keys |
|---|---|
| Source | `Left` `Right` / `h` `l` choose a provider; `Enter` opens it |
| Volume | `Right` `Up` / `l` `k` raise volume by 1 dB; `Left` `Down` / `h` `j` lower it |
| EQ | `Left` `Right` / `h` `l` select a band; `Up` `Down` / `k` `j` adjust its gain; `e` cycles presets |
| Shuffle | `Enter`, any arrow key, or `h` `j` `k` `l` toggles shuffle |
| Repeat | `Enter`, `Right` `Up` / `l` `k` cycle forward (Off / All / One); `Left` `Down` / `h` `j` cycle backward |
| Speed | `Right` `Up` / `l` `k` / `]` increase by 0.25x; `Left` `Down` / `h` `j` / `[` decrease by 0.25x |

## Text Input

Playlist search, native-provider search, URL, playlist-name, keymap, and jump
fields support these editor keys:

| Key | Action |
|---|---|
| `Left` `Right` / `Home` `End` | Move cursor |
| `Backspace` `Delete` | Delete before/at cursor |
| `Ctrl+W` | Delete previous word |
| `Ctrl+U` | Clear text before cursor |

The Metadata shortcut is inactive while a text input is active.

## EQ and Appearance

| Key | Action |
|---|---|
| `e` | Cycle EQ preset, including the saved Custom curve |
| `t` | Choose theme |
| `v` | Cycle visualizer |
| `Ctrl+V` | Pick visualizer from a list (live preview) |
| `V` | Full screen visualizer |
| `Ctrl+H` | Toggle album headers |
| `Ctrl+G` | Toggle the key-binding hint bar (remembered in `hide_help_bar`) |
| `Ctrl+B` | Open/close the settings pane (remembered in `hide_settings_pane`) |

Theme and visualizer pickers support `/` filtering. While you browse, arrow
keys preview the selected option. `Enter` keeps it. `Esc` restores the option
active when the picker opened. While you type a filter, `Enter` completes it
and `Esc` clears it.

## Features

| Key | Action |
|---|---|
| `f` | Toggle bookmark ★ on the selected track. In the radio browser, favorite the selected station. In the country browser, pin the selected country or region. On a podcast show, subscribe or unsubscribe. |
| `n` | Toggle favorite ♥ on the selected track. Favorited tracks appear in the cross-playlist "Favorites" virtual playlist. |
| `Ctrl+F` | Search with the active provider (Podcasts, Spotify, Qobuz, Tidal, Navidrome, Lyrion, Jellyfin, Emby, Plex, Audiobookshelf, Mixcloud, NetEase, Local), or search YouTube. Available in playlist and provider-browser views. |
| `u` | Load URL (stream/playlist) |
| `y` | Show or close lyrics |
| `r` | Retry lyrics lookup while lyrics are open |
| `i` | From the playlist, open full info for the highlighted item, including Path (`Up`/`Down` or `j`/`k` scroll; `i`/`Esc` closes) |
| `Ctrl+I` | Toggle Metadata below Settings for the highlighted playlist item (remembered in `show_metadata`; requires a terminal that distinguishes Ctrl+I from Tab) |
| `Ctrl+S` | Save track to `~/Music/cliamp` |
| `w` | Write the highlighted track to a local playlist |
| `N` | Open the active provider browser. On a selected Mixcloud show, open that creator's Uploads/Favorites. In the radio pane, open the country browser. |
| `L` | Browse local playlists (with cliamp radio) |
| `R` | Open radio provider |
| `O` (`Shift+O`) | Open Podcasts provider |
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

Metadata belongs to the main playback view, not provider browsers, and follows
the highlighted playlist row even when another item is playing. Enabling it
without a usable Settings sidebar opens the full info overlay instead; the
preference remains saved for a wider layout. See
[Metadata](configuration.md#metadata) for fields and layout behavior.

## Playlist and Queue

| Key | Action |
|---|---|
| `a` | Toggle the queue (play next) |
| `A` | Queue manager |
| `F` | Subscribed shows overlay (any provider that keeps subscriptions) |
| `x` | Remove the highlighted track from the current playlist |
| `p` | Playlist manager |
| `r` | Cycle repeat mode (Off / All / One) |
| `z` | Toggle shuffle |

### Inside the subscribed shows overlay

`F` lists the shows you subscribed to, without touching the network. Unlike
`Enter` in the provider list, every action here appends, so the playlist and
the queue survive.

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps) |
| `/` | Filter by show title or author; `Enter` applies, `Esc` clears |
| `Enter` | Append the show's episodes and play the newest |
| `a` | Append the show's episodes, leaving playback alone |
| `q` | Append the show's episodes and queue them in feed order |
| `l` | Append only the newest episode and queue it next |
| `L` | Append the newest episode of every subscribed show |
| `Esc` `F` | Close |

`L` fetches feeds concurrently and keeps subscription order. Shows whose feed
fails are counted in the overlay's error line rather than dropped silently.

### Inside the playlist manager

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor |
| `/` | Filter (incremental); `Esc` clears |
| `Enter` / `→` | List screen: open the selected playlist. Tracks screen: play the **selected** track. |
| `p` | Tracks screen: play all from the top |
| `w` | List: save the current queue with the playlist picker. Tracks: copy marked or selected tracks to another playlist. |
| `Space` | Tracks: mark/unmark highlighted track and advance |
| `[` `]` | Tracks: move highlighted track and save the playlist |
| `s` | Tracks: sort and save, cycling `track`, `title`, `artist`, `album`, `artist+album`, `path` |
| `o` | Tracks: open file browser to add files to this playlist |
| `D` | List: open the file browser to add `[[dir]]` sources to the selected playlist. Tracks: open the directory-sources screen. |
| `a` | List: create a playlist. After naming it, the file browser opens at `~`. Use `Enter` to enter a directory, `Space` to select folders or files, `Enter` to confirm, or `Esc` to finish. Tracks: mark or unmark all visible tracks. |
| `r` | List: rename the playlist (`Recently Played` cannot be renamed) |
| `d` | List: delete playlist (confirms; `Recently Played` cannot be deleted). Tracks: remove marked tracks, or highlighted track when none are marked |
| `u` | Undo the last manager edit |
| `←` `Backspace` `h` | Tracks screen: go back to the list |
| `Esc` | Close the playlist manager or go back |

Shift-letter keys switch providers. Playlist-manager track actions use lowercase
or punctuation keys. `D` is the exception. It opens the directory-sources
screen.

#### Directory sources screen (`D` from the tracks screen)

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Navigate directory sources |
| `a` | Open the file browser to add a directory as a `[[dir]]` source |
| `d` then `y` | Remove the selected source. `y` confirms; any other key cancels. |
| `r` | Toggle `recursive` on the highlighted source |
| `←` `Backspace` `h` `Esc` | Back to the tracks screen |

## File browser

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor |
| `←` `→` / `h` `l` / `Enter` | Go back; open a directory or file |
| `/` | Filter files |
| `Space` | Select or unselect file/directory |
| `a` | Select/unselect all visible audio files |
| `R` | Replace the current queue with selected files (confirm when it is non-empty) |
| `w` | Write selected files to a local playlist |
| `D` | Add selected folders as live `[[dir]]` sources to the target playlist. If none are selected, add the selected folder or the open directory. The browser stays open. |
| `~` `.` | Jump to home / current working directory |
| `Esc` `o` | Close file browser |

When the browser adds to a playlist, selected folders become `[[dir]]` sources.
Selected audio files become explicit tracks. This mode starts when you open the
browser with `D` from the manager list, with `o` from the tracks screen, or
after you create a playlist with `a`. In this mode, `Esc` means "done". cliamp
commits pending selections before it closes the browser.

## Provider browser (`N` key)

Press `N` to open a provider. These providers use the same album, artist, and
track screen keys: Navidrome, Lyrion, Plex, Jellyfin, Emby, Audiobookshelf,
Spotify, Qobuz, Tidal, Mixcloud, Podcasts, and YouTube Music.

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps from top to bottom) |
| `←` `→` / `h` `l` | Go back; open the selected item |
| `/` | Filter the visible list, including Radio's complete genre/tag index. In the Mixcloud Genres list, `Enter` searches the complete server-side genre/tag catalog. |
| `f` | In the Mixcloud Genres list, favorite or unfavorite the selected genre locally. Update `[mixcloud].styles`. On a podcast show, subscribe or unsubscribe. |
| `l` | On a podcast show, append its newest episode and queue it next, without replacing the playlist |
| `Enter` | Open the selected artist or album. A Radio tag loads up to 200 matching stations; a selected track plays and queues the rest of the visible list. |
| `R` | Replace the queue with all visible tracks (start from the top, confirm when non-empty) |
| `a` | Append all visible tracks to the queue |
| `q` | Queue the highlighted track to play next |
| `s` | Cycle album sort (album list only) |
| `S` `N` `P` `J` `E` `Y` `C` `X` `M` `Q` `T` `L` `O` | Switch to that provider without opening the main pane. `R` replaces the queue on the track screen. |
| `Esc` `b` | Go back one level; close the browser |

The Mixcloud browser menu has **By Show**, **By Creator**, **By Creator / Show**,
and **Genres**. Genre favorites add Latest/Popular rows to the provider pane and
show-sort menu. They do not change the Mixcloud website account. The header
shows a source path such as `Navidrome / Miles Davis / Kind of Blue / Tracks`.
This keeps the current provider and open location visible. Track rows show
right-aligned durations when the provider provides them.

For Mixcloud, selecting a Show, a creator Uploads/Favorites collection, or a
genre Latest/Popular view replaces the main playlist and closes the browser. An
empty result leaves the current playlist and browser unchanged.

For Podcasts, **Browse Categories** opens **Genre**, then **Show**. `Enter` on a
show replaces the main playlist with its episodes without starting playback.
Then `Enter` plays an episode and `a` toggles its play-next queue entry.

## Provider playlist list

The playlists pane appears when the focus is on a provider, such as Spotify,
Navidrome, Podcasts, or Local Playlists:

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps) |
| `Ctrl+U` `Ctrl+D` | Scroll by page |
| `Enter` | Load the selected playlist tracks into the queue |
| `/` | Filter the playlist list. In Podcasts, type a show name or publisher RSS URL, then `Enter` to search; typing alone sends no search requests. |
| `f` | In Podcasts, subscribe or unsubscribe from the selected show |
| `Ctrl+F` | Run the provider online or server search (Spotify, Navidrome, NetEase, and others). |
| `Ctrl+R` | Refresh the provider: reload the currently open playlist or starting wave in place (e.g. a fresh Yandex "Моя волна" batch), or return to the playlist list. For Mixcloud, also clear the cached `/me/` identity. |
| `p` | Open the playlist manager (Local pane only; create, rename, delete, add dirs/tracks) |
| `S` `N` `P` `J` `E` `Y` `C` `X` `M` `Q` `L` `R` `O` | Switch to that provider |
| `Tab` | Leave the provider pane and focus Source, or the first visible playback control |
| `Shift+Tab` | Leave the provider pane and focus the last visible playback control (Speed, or Repeat with Settings closed) |
| `Esc` `b` | Back to the playlist pane; in Podcasts, clear show search first |

Playlist rows show `Name · N tracks · 1h 23m` when the provider returns track
counts and total duration. The header shows `Provider / Playlists`. The loaded
playlist has a `▶` prefix. Spotify groups playlists under section headers (`── library ──`, `── your playlists ──`, `── followed playlists ──`). For configured accounts, Mixcloud shows Your Mixcloud first: Stream, then Favorites. It then
shows Browse shortcuts, public collections, Discover charts, and a
Latest/Popular pair for each locally favorited genre under Music Styles. Leaving
a provider-pane Browse shortcut returns to the provider pane.

Podcasts lists subscriptions and Apple's top shows. `/` searches up to 100 shows;
`Enter` on a show replaces the playlist without playing. `Ctrl+R` reloads a show
opened from this list or the category browser; reopen `Ctrl+F` shows to fetch
those feeds again. See [podcasts.md](podcasts.md) for startup and country settings.

## Search results overlays

Use these keys when `Ctrl+F` opens provider search or YouTube/SoundCloud network
search and the results list is open:

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` / `Ctrl+N` `Ctrl+P` | Move cursor (single item) |
| `Ctrl+U` `Ctrl+D` | Scroll results by page |
| `Enter` | Play the selected track now |
| `a` | Append the selected track to the playlist |
| `q` | Queue the selected track to play next |
| `f` | Subscribe or unsubscribe from the selected podcast show |
| `p` | (Spotify only) Save the selected track to a Spotify playlist |
| `Esc` `Backspace` | Back to the search input |

Podcasts returns show collections (up to 20), not episodes. `Enter` appends the
feed's episodes and starts the first; `a` appends them and starts the first if
the playlist was empty or nothing is playing. `q` appends and queues the
episodes in feed order after any already queued tracks, starting queued
playback if nothing is playing.

## Fuzzy search

Local search boxes use fuzzy matching. Query characters must appear in order but
do not need to be next to each other. Results are ranked by relevance, with the
best match first. For example, `skr` and `saku` both find a track named "Sakura".

This applies to:

- `/` playlist search
- `/` file browser filter
- `Ctrl+F` when the active provider is Local (your saved playlists)

Other `Ctrl+F` providers, including Spotify, Qobuz, Tidal, Navidrome, Lyrion,
Jellyfin, Emby, Plex, Audiobookshelf, Mixcloud, NetEase, Podcasts, and YouTube, send the
query to their search API. Their services control matching rules.

## General

| Key | Action |
|---|---|
| `?` / `Ctrl+K` | Show keymap |
| `q` | Quit |
