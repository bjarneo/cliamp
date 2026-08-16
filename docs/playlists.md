# Playlists

Cliamp supports local **TOML playlists** managed from the TUI or CLI, plus **M3U/M3U8/PLS playlists** loaded from files or URLs.

## M3U and PLS Playlists

Load any `.m3u`, `.m3u8`, or `.pls` file, local or remote:

```sh
cliamp ~/radio-stations.m3u
cliamp http://radio.example.com/streams.m3u
cliamp ~/music.m3u https://example.com/live.m3u   # mix local + remote
cliamp ~/radio.pls
```

### EXTINF Metadata

The parser extracts titles and durations from `#EXTINF` lines:

```m3u
#EXTM3U
#EXTINF:180,Radio Station 1
http://station-1.com/stream
#EXTINF:-1,Radio Station 2
http://station-2.com/stream/hd
```

Entries without `#EXTINF` still work. The filename or URL is used as the title instead.

### Relative Paths

Paths in a local M3U file are resolved relative to the M3U file's directory:

```m3u
#EXTINF:240,My Song
../Music/song.mp3
#EXTINF:-1,Live Stream
http://example.com/live
```

If `radio.m3u` is in `~/playlists/`, then `../Music/song.mp3` resolves to `~/Music/song.mp3`.

### Edge Cases Handled

- UTF-8 BOM (common in Windows-created files)
- `\r\n` line endings
- Missing `#EXTM3U` header
- Mixed local and remote entries in the same file
- Other `#` directives (silently skipped)

---

## Local TOML Playlists

Create and manage your own playlists stored as `.toml` files in `~/.config/cliamp/playlists/`. (The folder follows cliamp's config-dir resolution: `CLIAMP_CONFIG_DIR`, then `XDG_CONFIG_HOME/cliamp`, then `HOME/.config/cliamp`.)

### File Format

Each playlist is a separate `.toml` file, and every file in the `playlists/` folder with a `.toml` extension (case-insensitive) is picked up automatically — whether created by the CLI or written by hand. Files that fail to parse are skipped. The filename (minus extension) becomes the playlist name:

```text
~/.config/cliamp/playlists/
├── radio-stations.toml   → playlist "radio-stations"
├── music.toml            → playlist "music"
└── gym.toml              → playlist "gym"
```

Keep one file per collection; mix hand-written and CLI-created files freely. Empty playlists are kept on disk so they remain visible in the TUI and CLI.

```toml
# ~/.config/cliamp/playlists/radio-stations.toml

[[track]]
path = "http://station-1.com/stream"
title = "Radio Station 1"

[[track]]
path = "http://station-2.com/stream/hd"
title = "Radio Station 2"
artist = "Radio Network"

[[track]]
path = "/home/user/Music/song.mp3"
title = "My Song"
artist = "My Artist"
```

Each `[[track]]` section supports:

| Key | Required | Description |
|-----|----------|-------------|
| `path` | Yes | File path or HTTP URL |
| `title` | Yes | Display title |
| `artist` | No | Artist name |
| `album` | No | Album name |
| `genre` | No | Genre name |
| `year` | No | Release year |
| `track_number` | No | Track number |
| `duration_secs` | No | Duration in seconds |
| `embedded_lyrics` | No | Lyrics copied from local file tags |
| `album_art_url` | No | Cached file URL for embedded album art |
| `bookmark` | No | Bookmark flag |

HTTP/HTTPS paths are automatically treated as streams.

### Directory Sources (`[[dir]]`)

Instead of listing every file, a playlist can reference a directory that is
scanned for audio files every time the playlist loads. The directory is read
at load time, so new files show up automatically and removed files disappear:

```toml
# ~/.config/cliamp/playlists/music.toml

[[dir]]
path = "~/Music"

[[track]]
path = "https://radio.example.com/stream"
title = "My Radio"
```

Each `[[dir]]` section supports:

| Key | Required | Description |
|-----|----------|-------------|
| `path` | Yes | Directory path; `~` and environment variables are expanded |
| `recursive` | No | Scan subdirectories too (default `true`) |

Directory-sourced tracks are returned in document order, sorted by path within
each directory. An explicit `[[track]]` with the same path always wins over a
directory scan, so you can pin a bookmark or custom metadata onto a specific
file. Bookmarking a directory-sourced track (TUI `f` or
`cliamp playlist bookmark`) materializes it as an explicit `[[track]]` entry so
the bookmark survives. Unreadable or missing directories contribute no tracks.

Use `--dir` to create or extend such playlists from the CLI:

```sh
cliamp playlist create "Music" --dir ~/Music
cliamp playlist add "Music" --dir ~/Downloads/live
cliamp playlist dirs "Music"               # list referenced directories
```

`--dir` cannot be combined with `--ssh`. Removing a directory-sourced track
(CLI or TUI) is refused, since the file is owned by the directory source;
delete the file from the directory or edit the `[[dir]]` section instead.

### Adding Files and Directories

A playlist file can hold any number of `[[track]]` and `[[dir]]` sections,
mixed freely. This single `mix.toml` combines two directory sources, two
absolute file paths, and a stream URL:

```toml
# ~/.config/cliamp/playlists/mix.toml

[[dir]]
path = "~/Music"

[[track]]
path = "/home/user/Downloads/live-set.mp3"
title = "Live Set"

[[dir]]
path = "~/Downloads/podcasts"
recursive = false

[[track]]
path = "https://radio.example.com/stream"
title = "My Radio"
```

Or spread the tracks and directories across many files — every `.toml` file in
the `playlists/` folder becomes its own playlist:

```toml
# ~/.config/cliamp/playlists/gym.toml

[[dir]]
path = "~/Music/gym"

[[track]]
path = "/home/user/Documents/warmup.mp3"
title = "Warmup"
```

```toml
# ~/.config/cliamp/playlists/chill.toml

[[track]]
path = "/home/user/Downloads/chill-lofi.mp3"
title = "Lofi"
```

The same playlists can be built from the command line without editing files.
Positional paths are expanded into `[[track]]` entries; `--dir` keeps a live
directory reference that re-scans on every load:

```sh
cliamp playlist create "mix" ~/Downloads/live-set.mp3 --dir ~/Music --dir ~/Downloads/podcasts
cliamp playlist create "gym" ~/Documents/warmup.mp3 --dir ~/Music/gym
cliamp playlist create "chill" ~/Downloads/chill-lofi.mp3
```

Append more files and directories to an existing playlist at any time:

```sh
cliamp playlist add "mix" another-track.mp3 --dir ~/Downloads/live
```

Note that `recursive = false` has no CLI flag — set it by editing the
playlist file's `[[dir]]` section.

### Podcast / RSS Feed Playlists

You can save podcast RSS feed URLs in a playlist. Add `feed = true` to mark a track as a feed. When played, the feed is resolved into individual episodes instead of being streamed directly.

```toml
# ~/.config/cliamp/playlists/podcasts.toml

[[track]]
path = "https://feeds.simplecast.com/54nAGcIl"
title = "The Daily"
feed = true

[[track]]
path = "https://lexfridman.com/feed/podcast/"
title = "Lex Fridman Podcast"
feed = true
```

Each `[[track]]` with `feed = true` supports:

| Key | Required | Description |
|-----|----------|-------------|
| `path` | Yes | RSS/Atom feed URL |
| `title` | Yes | Display name for the feed |
| `feed` | Yes | Must be `true` to enable feed resolution |

When you select a feed entry, cliamp fetches the RSS feed, extracts all episodes with audio enclosures, and loads them into the playlist. Episode titles and durations (from `<itunes:duration>`) are preserved.

URLs with `.xml`, `.rss`, or `.atom` extensions are also auto-detected as feeds without needing `feed = true`.

### Browsing and Loading Playlists

Running `cliamp` without arguments connects to the built-in radio channel. If Navidrome is configured, it opens the provider browser instead.

To browse your local playlists, press `Esc` or `b` during playback to open the provider browser. Navigate with `Up`/`Down` (or `j`/`k`) and press `Enter` to load a playlist. Tracks replace the current playlist and playback starts immediately. Press `Tab` to jump back to the now-playing playlist without reloading.

If Navidrome is also configured, both sources appear in the same list with provider labels (e.g., `[Navidrome] Jazz`, `[Local Playlists] favorites`).

You can start with CLI files and browse playlists later:

```sh
cliamp song.mp3                    # starts playing, Esc opens browser
```

### Managing Playlists

Press `p` from any view to open the playlist manager:

1. **Browse**: see all playlists with track counts
2. **Filter**: press `/` to incrementally filter the list (works on both the playlists screen and the track screen). `Esc` clears the filter.
3. **Open**: press `Enter` or `→` to view tracks inside a playlist
4. **Add now-playing**: press `a` to add the currently playing track (the footer shows the track name so you know what gets added)
5. **Delete playlist**: press `d` then `y` to confirm deletion
6. **Mark tracks**: open a playlist, press `Space` to mark a track and advance, or `a` to mark or unmark all visible tracks
7. **Move tracks**: press `[` or `]`; the saved playlist is updated immediately
8. **Sort tracks**: press `s` to cycle `track`, `title`, `artist`, `album`, `artist+album`, and `path` sorting
9. **Remove tracks**: press `d` to remove the marked tracks, or the highlighted track when nothing is marked
10. **Undo manager edits**: press `u` after delete, remove, move, or sort
11. **Write tracks elsewhere**: press `w` to copy the marked or highlighted tracks to another playlist; duplicate paths are skipped
12. **Add files**: press `o` from inside a playlist to browse files and add them to that playlist
13. **Play this**: press `Enter` on the track list to start playback at the highlighted track. The rest of the playlist follows.
14. **Play all**: press `p` to start from the top, regardless of cursor position
15. **New playlist**: select "+ New Playlist...", type a name, and press Enter. If you create a playlist while a `/` filter is active, the filter text is pre-filled as the new playlist name.

Tracks with an `album` field are grouped by album with visual separator headers in the playlist manager (album grouping is hidden while a filter is active) and the main player view.

The directory `~/.config/cliamp/playlists/` is created automatically on first use. Removing the last track leaves an empty playlist file; use `d` on the playlist list or `cliamp playlist delete` to delete the playlist itself.

### Writing to Playlists

Press `w` on a track in the main playlist to open the local playlist picker. Pick an existing playlist or choose `+ New Playlist...`. Exact duplicate paths are skipped and reported.

In the file browser, select files with `Space`, select all visible audio files with `a`, then press `w` to write the selection to a playlist instead of loading it into the current queue.

### Command Line Management

Manage local TOML playlists without opening the TUI:

```sh
cliamp playlist list
cliamp playlist create "Name"                    # create an empty playlist
cliamp playlist create "Name" file1 dir/ ...     # create from files/folders
cliamp playlist create "Name" --dir ~/Music      # reference a directory dynamically
cliamp playlist add "Name" file1 dir/ ...        # append, skipping duplicate paths
cliamp playlist add "Name" --dir ~/Music         # add another directory source
cliamp playlist dirs "Name"                      # list directory sources
cliamp playlist rename "Old" "New"
cliamp playlist dedupe "Name"
cliamp playlist sort "Name" --by artist+album
cliamp playlist doctor                           # report missing local files in all playlists
cliamp playlist doctor "Name" --fix              # prune missing local files
cliamp playlist export "Name" --format m3u -o mix.m3u
cliamp playlist import mix.pls --name "Imported"
cliamp playlist show "Name" --json
cliamp playlist remove "Name" --index 3
cliamp playlist bookmark "Name" --index 3       # toggle bookmark flag
cliamp playlist bookmarks                        # list all bookmarked tracks
cliamp playlist enrich "Name"                    # backfill duration/album metadata
cliamp playlist delete "Name"
```

Sort keys are `track`, `title`, `artist`, `album`, `artist+album`, and `path`.

New playlist names reject path separators and non-portable filename characters. Existing playlist files with older Unix-only names remain readable and writable.

### Creating Playlists Manually

Create the directory and add a `.toml` file:

```sh
mkdir -p ~/.config/cliamp/playlists
```

```toml
# ~/.config/cliamp/playlists/favorites.toml

[[track]]
path = "/home/user/Music/song.mp3"
title = "Great Song"
artist = "Good Artist"

[[track]]
path = "https://radio.example.com/stream"
title = "My Radio"
```

### Controls

**Playlist browser (provider view):**

| Key | Action |
|-----|--------|
| `Up` `Down` / `j` `k` | Navigate playlists |
| `Enter` | Load selected playlist |
| `Tab` | Switch to now-playing playlist |
| `Esc` `b` | Open browser (from playlist view) |

**Playlist manager (`p` key):**

| Key | Action |
|-----|--------|
| `p` / `Esc` | Open/close playlist manager (Esc on tracks screen goes back) |
| `Up` `Down` / `j` `k` | Navigate |
| `/` | Filter playlists or tracks; `Esc` clears |
| `Enter` / `→` | Open playlist (list screen) / Play **highlighted** track (tracks screen) |
| `p` | Play all tracks from the top (tracks screen) |
| `a` | List: add currently playing track. Tracks: mark/unmark all visible tracks |
| `Space` | Mark/unmark track and advance (tracks screen) |
| `s` | Sort tracks, cycling supported sort keys (tracks screen) |
| `w` | Write marked/highlighted tracks, or the current queue from the list screen, to another playlist |
| `o` | Add files to the open playlist (tracks screen) |
| `[` `]` | Move track up/down and save (tracks screen) |
| `d` | Delete playlist (confirms) / Remove marked tracks, or highlighted track if none are marked |
| `u` | Undo the last playlist-manager edit |
| `←` / `Backspace` | Go back from tracks screen to list |
