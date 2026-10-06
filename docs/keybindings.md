# Keybindings

Press `Ctrl+K` in any mode, or `?` in the player, to view keybindings. The
keymap first shows commands for the active screen. It then shows player and
library commands.

To run a command, select it and press `Enter`. The command acts as if you
pressed its key on the screen that opened the keymap. Press `/` to filter the
list first. The keymap cannot run key pairs such as `Left` `Right`, the `N`
then `j` seek, or a player command while another screen is open. For these
entries, a status message tells you which key to press.

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
| `Shift+Up` `Shift+Down` | Move track up/down in playlist/queue. Turn off shuffle to move a playlist track. |
| `h` `l` | Adjust the focused setting (EQ: select band) |
| `Enter` | Play selected track |
| `/` | Filter the playlist (navigate results with `↑` `↓` / `Ctrl+N` `Ctrl+P`; `Ctrl+U` clears the query; `Tab` toggles the queue for the selected result) |
| `Ctrl+X` | Expand/collapse playlist |
| `Ctrl+Z` | Undo the last playlist or queue change |
| `o` | Open file browser |
| `b` `Esc` | Back to provider |

In full and compact playback layouts, the first `Tab` from the playlist focuses
Source (`SRC`); `Shift+Tab` starts at Speed when it is visible. Source is skipped
when only one provider is available. Closing Settings skips EQ and Speed. A
short sidebar can omit Shuffle and Repeat together, removing both Tab stops.
Metadata is read-only and never a separate Tab stop.

In the minimal (`40x10`) and simplified layouts, `Tab` and `Shift+Tab` keep
playback focus on the playlist, even though simplified mode hides the list.
`Esc` still opens the separate provider-list view. In the minimal layout, the
hint bar then shows the provider keys. Below `40x10`, only a resize message is
shown.

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

### Search and filter modes

`/` filters the list on the screen. `Ctrl+F` searches the active provider. In
Radio and Podcasts, `/` sends the query to the provider when you press
`Enter`.

While a search or filter input is open, its line starts with a badge that
names the mode and the source, such as `[Filter: Playlist]`, `[Filter: Files]`,
`[Search: Spotify]` or `[Search: Radio]`. The line ends with `Esc Exit`. Press
`Esc` to leave the input. On a narrow panel, the hint bar still shows `Esc`.

If the active provider has no `Ctrl+F` search, `Ctrl+F` searches YouTube. The
overlay shows `[Search: YouTube]` and names the provider that has no search.

After a Radio or Podcasts search, the provider header shows `Search results`
and the hint bar shows `Esc Clear search`. Press `Esc` to go back to the full
list.

## EQ and Appearance

| Key | Action |
|---|---|
| `e` | Cycle EQ preset, including the saved Custom curve |
| `t` | Choose theme |
| `v` | Cycle visualizer and save the choice in `config.toml` |
| `Ctrl+V` | Pick visualizer from a list (live preview) |
| `V` | Full screen visualizer. Inside it, `v` cycles modes and saves the choice, `<`/`>` change track, `+`/`-` change volume, and `t` hides the episode name, leaving only the bracketed source. `q` or `Esc` exits back to the player; `Ctrl+C` quits. |
| `Ctrl+H` | Toggle album headers |
| `Ctrl+G` | Toggle the key-binding hint bar (remembered in `hide_help_bar`) |
| `Ctrl+B` | Open/close the settings pane (remembered in `hide_settings_pane`) |

Theme and visualizer pickers support `/` filtering. While you browse, arrow
keys preview the selected option. `Enter` keeps it. `Esc` or `q` restores the option
active when the picker opened. While you type a filter, `Enter` completes it
and `Esc` clears it.

## Features

| Key | Action |
|---|---|
| `f` | Toggle the favorite ♥ on the selected track. Favorites appear in the "Favorites" virtual playlist, and cliamp also saves them on Spotify and Navidrome. For directory radio stations outside saved local playlists, toggle Radio Favorites from the browser or playback playlist, including country and genre results. In the country browser, pin the selected country or region. On a podcast show, subscribe or unsubscribe. |
| `Ctrl+F` | Search with the active provider (Podcasts, Spotify, Qobuz, Tidal, Navidrome, Lyrion, Jellyfin, Emby, Plex, Audiobookshelf, Mixcloud, NetEase, SoundCloud, Yandex Music, Local), or search YouTube. Available in playlist and provider-browser views. The search line names the source. See [Search and filter modes](#search-and-filter-modes). |
| `u` | Load URL (stream/playlist) |
| `d` | Open the audio device picker |
| `y` | Show or close lyrics |
| `r` | Retry lyrics lookup while lyrics are open |
| `[` / `]` | Adjust synced-lyrics timing offset (−/+250 ms) while lyrics show timestamped lines |
| `i` | From the playlist, open full info for the highlighted item, including Path (`Up`/`Down` or `j`/`k` scroll; `i`/`Esc`/`q` closes) |
| `Ctrl+I` | Toggle Metadata below Settings for the highlighted playlist item (remembered in `show_metadata`; requires a terminal that distinguishes Ctrl+I from Tab) |
| `Ctrl+S` | Save track to `[downloads].directory` (default `~/Music/cliamp`) |
| `w` | Write the highlighted track to a local playlist |
| `N` | Open the active provider browser. On a selected Mixcloud show, open that creator's Uploads/Favorites. In the radio pane, open the country browser. |
| `L` | Open Local provider |
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
| `c` | Song radio: replace the queue with the highlighted song and related songs, and play it from the top. Spotify and YouTube songs only; see [Song radio](#song-radio) |
| `c` | In the queue manager: clear the queue (press again to confirm when more than one track) |
| `d` | In the queue manager: remove the highlighted track |
| `F` | Subscribed shows overlay (any provider that keeps subscriptions) |
| `x` | Remove the highlighted track from the current playlist |
| `p` | Playlist |
| `r` | Cycle repeat mode (Off / All / One) |
| `z` | Toggle shuffle |

While shuffle is on, the playlist lists tracks in play order. The tracks that
played before the current track are above it. The tracks that play next are
below it. Each row keeps its original track number. When you turn off shuffle,
the playlist returns to the original order and the cursor stays on the same
track.

### Song radio

Press `c` on a Spotify or YouTube song, in the playlist or in `Ctrl+F` search
results, to start a song radio. cliamp looks up related songs (Spotify's
autoplay station, or the YouTube Mix through yt-dlp) while the status line
shows "Finding songs like …". When they arrive, the queue becomes the chosen
song followed by the related songs, and the chosen song plays from the start,
even if it was already playing.

- If the lookup fails or finds nothing, the queue and playback stay as they were.
- Pressing `c` again, or loading or opening another playlist, before the songs
  arrive cancels the radio. Moving to another song in the queue does not.
- While the chosen song is still playing and the queue is unchanged, `Ctrl+Z`
  brings the old queue back; the chosen song keeps playing, and the old
  current song plays after it. The restored queue is no longer tied to the
  playlist it came from.
- `song_radio_size` in `config.toml` sets how many related songs are added
  (default 30, 1–100).
- The key is only offered on songs that support it: not local files, other
  providers, or albums in search results.

### Inside the subscribed shows overlay

`F` lists the shows you subscribed to, without touching the network. Unlike
`Enter` in the provider list, every action here appends, so the playlist and
the queue survive.

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps) |
| `/` | Filter by show title or author; `Enter` applies, `Esc` clears |
| `Enter` | Append the show's episodes and play the first appended |
| `a` | Append the show's episodes, leaving playback alone |
| `q` | Append the show's episodes and queue them in feed order |
| `l` | Append only the newest episode and add it to the end of the queue |
| `L` | Append the newest episode of every subscribed show |
| `Esc` `F` | Close |

`L` fetches feeds concurrently and keeps subscription order. Shows whose feed
fails are counted in the overlay's error line rather than dropped silently.
### Inside the save-to-playlist picker

Reached with `w`. The list shows your saved playlists plus a **New playlist**
row at the end.

| Key | Action |
|---|---|
| `Enter` | Add the tracks to the end of the selected playlist, or create a new one |
| `p` | Add the tracks to the start of the selected playlist instead |
| `Esc` `q` | Cancel |

`Enter` skips tracks the playlist already holds. `p` moves them to the front
instead, since putting a track first is an ordering request rather than a
duplicate. Tracks the playlist only holds through a `[[dir]]` source cannot be
reordered, so `p` leaves them alone and reports them as skipped.

### Inside the playlist

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor |
| `/` | Filter (incremental); `Esc` clears |
| `Enter` / `→` | List screen: open the selected playlist. Tracks screen: play the **selected** track. |
| `p` | Tracks screen: play all from the top |
| `w` | List: save the current queue with the playlist picker. Tracks: copy marked or selected tracks to another playlist. |
| `Space` | Tracks: mark/unmark highlighted track (advances on mark, stays on unmark) |
| `[` `]` | Tracks: move highlighted track and save the playlist |
| `f` | Tracks: toggle the favorite ♥ on the highlighted track. In "Favorites", an unfavorite removes the row. |
| `s` | Tracks: sort and save, cycling `track`, `title`, `artist`, `album`, `artist+album`, `path` |
| `o` | Tracks: open file browser to add files to this playlist |
| `D` | List: open the file browser to add `[[dir]]` sources to the selected playlist. Tracks: open the directory-sources screen. |
| `a` | List: create a playlist. After naming it, the file browser opens at `~`. Use `Enter` to enter a directory, `Space` to select folders or files, `Enter` to confirm, or `Esc` to finish. Tracks: mark or unmark all visible tracks. |
| `r` | List: rename the playlist (`Recently Played` cannot be renamed) |
| `d` | List: delete playlist (confirms; `Recently Played` cannot be deleted). Tracks: remove marked tracks, or highlighted track when none are marked |
| `A` | List: append the selected playlist to the current one, keeping what is loaded. Tracks: append the marked tracks, or the highlighted one. |
| `u` | Undo the last manager edit |
| `←` `Backspace` `h` `q` | Tracks screen: go back to the list |
| `Esc` `q` | Close the playlist or go back |

Shift-letter keys switch providers. Playlist-manager track actions use lowercase
or punctuation keys. `D` is the exception. It opens the directory-sources
screen. While a delete prompt waits for an answer, `y` or `Y` confirms and any
other key cancels. The Shift-letter keys do not switch providers then.

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
| `Esc` `q` `o` | Close file browser |

When the browser adds to a playlist, selected folders become `[[dir]]` sources.
Selected audio files become explicit tracks. This mode starts when you open the
browser with `D` from the manager list, with `o` from the tracks screen, or
after you create a playlist with `a`. In this mode, `Esc` means "done". cliamp
commits pending selections before it closes the browser.

## Provider browser (`N` key)

Press `N` to open a provider. These providers share the browser keys below:
Navidrome, Lyrion, Plex, Jellyfin, Emby, Audiobookshelf, Spotify, Qobuz,
Tidal, Mixcloud, Podcasts, and YouTube Music. Artist and album screens exist
only where the provider implements them: Navidrome, Lyrion, Jellyfin, Emby,
Audiobookshelf, Qobuz, Tidal, and Mixcloud. Podcasts reuses those screens for
categories and shows. Plex, Spotify, and YouTube Music have no artist or album
screens; their playlists — and, for Plex and Spotify, saved albums — appear in
the provider pane.

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps from top to bottom) |
| `←` `→` / `h` `l` | Go back; open the selected item |
| `/` | Filter the visible list, including Radio's complete genre/tag index. In the Mixcloud Genres list, `Enter` searches the complete server-side genre/tag catalog. |
| `f` | On a track, toggle its favorite ♥. In the Mixcloud Genres list, favorite or unfavorite the selected genre locally. Update `[mixcloud].styles`. On a podcast show, subscribe or unsubscribe. |
| `l` | Provider list, on a podcast show row only: append its newest episode and add it to the end of the queue, without replacing the playlist. Elsewhere in the browser `l` opens the selected item. |
| `a` | Append all visible tracks to the queue. Provider list, on a podcast show row only: append every episode without replacing the playlist. |
| `Enter` | Open the selected artist or album. A Radio tag loads up to 200 matching stations; a selected track plays and queues the rest of the visible list. |
| `R` | Replace the queue with all visible tracks (start from the top, confirm when non-empty) |
| `q` | Queue the highlighted track to play next |
| `s` | Cycle album sort (album list only) |
| `S` `P` `J` `E` `B` `Y` `C` `X` `M` `Q` `T` `L` `R` `O` | Switch to that provider without opening the main pane. On the track screen, `R` replaces the queue instead. |
| `N` | In Jellyfin, reopen the browse-mode chooser. Elsewhere, switch to Navidrome. |
| `Esc` `b` | Go back one level; close the browser |

While you type in the `/` filter, the Shift+letter switches and `N` do not
work. These keys add their letter to the filter.

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
| `p` | Open the playlist as an overlay (the Local source shows it directly; create, rename, delete, add dirs/tracks) |
| `S` `P` `J` `E` `B` `Y` `C` `X` `M` `Q` `T` `L` `R` `O` | Switch to that provider |
| `N` | Open the browser of the provider on screen |
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
| `c` | Song radio from the selected song (Spotify and YouTube songs, not albums) |
| `f` | Toggle the favorite ♥ on the selected track. On a podcast show, subscribe or unsubscribe. |
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
Jellyfin, Emby, Plex, Audiobookshelf, Mixcloud, NetEase, SoundCloud, Yandex
Music, Podcasts, and YouTube, send the query to their search API. Their
services control matching rules.

## General

| Key | Action |
|---|---|
| `?` / `Ctrl+K` | Show keymap. `Enter` runs the selected command. `Esc` or `q` closes it. |
| `q` / `Ctrl+C` | Quit |

cliamp also quits as the `q` key does when it gets `SIGINT`, `SIGTERM` or `SIGHUP`. It then saves the resume position. The terminal sends `SIGHUP` when you close its window.
