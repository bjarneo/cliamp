# Keybindings

Press `?` or `Ctrl+K` in the player to see all keybindings.

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
| `Tab` | Toggle focus (Playlist / EQ) |
| `j` `k` / `Up` `Down` | Playlist scroll / EQ band adjust (wraps around) |
| `PageUp` `PageDown` / `Ctrl+U` `Ctrl+D` | Scroll playlist/file browser by page |
| `Home` `End` / `g` `G` | Go to top/end of playlist/file browser |
| `Shift+Up` `Shift+Down` | Move track up/down in playlist/queue |
| `h` `l` | EQ cursor left/right |
| `Enter` | Play selected track |
| `/` | Search playlist |
| `Ctrl+X` | Expand/collapse playlist |
| `o` | Open file browser |
| `b` `Esc` | Back to provider |


## EQ and Appearance

| Key | Action |
|---|---|
| `e` | Cycle EQ preset |
| `t` | Choose theme |
| `v` | Cycle visualizer |
| `V` | Full screen visualizer |
| `Ctrl+H` | Toggle album headers |

## Features

| Key | Action |
|---|---|
| `f` | Toggle bookmark ★ on selected track (or favorite radio station in radio browser) |
| `Ctrl+F` | Search — active provider's native search (Spotify, Navidrome, Jellyfin, Emby, Plex, NetEase, Local) or YouTube fallback. Available from playlist and provider-browser views. |
| `u` | Load URL (stream/playlist) |
| `y` | Show lyrics |
| `Ctrl+S` | Save track to ~/Music |
| `N` | Navidrome browser |
| `L` | Browse local playlists (with cliamp radio) |
| `R` | Open radio provider |
| `S` | Open Spotify provider |
| `P` | Open Plex provider |
| `J` | Open Jellyfin provider |
| `E` | Open Emby provider |
| `Y` | Open YouTube provider |
| `C` | Open SoundCloud provider |
| `M` | Open NetEase provider |

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
| `P` | Tracks screen: play all from the top |
| `a` | Add the now-playing track (footer shows which track) |
| `d` | List: delete playlist (confirms) · Tracks: remove the highlighted track |
| `←` `Backspace` `h` | Tracks screen: go back to the list |
| `p` `Esc` | Close the playlist manager |

## Provider browser (`N` key)

When you press `N` to drill into a provider (Navidrome, Plex, Jellyfin, Emby, Spotify, YouTube Music), the album/artist/track screens use:

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps top↔bottom) |
| `←` `→` / `h` `l` | Back / drill in |
| `/` | Filter the visible list (search bar appears under the title) |
| `Enter` | Open (artists/albums) · play the highlighted track and queue the rest of the visible list |
| `R` | Replace the queue with all visible tracks (start from the top) |
| `a` | Append all visible tracks to the queue |
| `q` | Queue the highlighted track to play next |
| `s` | Cycle album sort (album list only) |
| `S` `N` `P` `J` `E` `Y` `C` `M` `L` `R` | Quick-switch to that provider without going back through the main pane |
| `Esc` `b` | Walk back one level / close the browser |

The track screen shows a `N tracks · 47:22` subtitle and right-aligned per-track durations when the provider returns them.

## Provider playlist list

The playlists pane (visible when focus is on a provider — Spotify, Navidrome, Local Playlists, etc.):

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor (wraps) |
| `Enter` | Load the highlighted playlist's tracks into the queue |
| `/` | Filter the playlist list |
| `Ctrl+F` | Online/server search (Spotify/Navidrome/NetEase/etc.'s own search) |
| `Ctrl+R` | Refresh — re-pull the playlist list from the provider |
| `S` `N` `P` `J` `E` `Y` `C` `M` `L` `R` | Switch to that provider |
| `Tab` | Switch focus to EQ |
| `Esc` `b` | Back to the playlist pane |

Playlist rows show `Name · N tracks · 1h 23m` when the provider returns track counts and total duration. The currently loaded playlist is marked with a `▶` prefix. Spotify groups its playlists under section headers (`── library ──`, `── your playlists ──`, `── followed playlists ──`).

## Search results overlays

When `Ctrl+F` opens provider search or YouTube/SoundCloud net search and you're viewing the results list:

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move cursor |
| `Enter` | Play the selected track now |
| `a` | Append the selected track to the playlist |
| `q` | Queue the selected track to play next |
| `p` | (Spotify only) Save the selected track to a Spotify playlist |
| `Esc` `Backspace` | Back to the search input |

## General

| Key | Action |
|---|---|
| `?` / `Ctrl+K` | Show keymap |
| `q` | Quit |
