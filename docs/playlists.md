# Playlists

Cliamp supports two kinds of playlists: **M3U files** loaded from the command line and **local TOML playlists** managed from within the app.

## M3U Playlists

Load any `.m3u` or `.m3u8` file, local or remote:

```sh
cliamp ~/radio-stations.m3u
cliamp http://radio.example.com/streams.m3u
cliamp ~/music.m3u https://example.com/live.m3u   # mix local + remote
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

Create and manage your own playlists stored as `.toml` files in `~/.config/cliamp/playlists/`.

### File Format

Each playlist is a separate `.toml` file. The filename (minus extension) becomes the playlist name.

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

HTTP/HTTPS paths are automatically treated as streams.

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
2. **Open**: press `Enter` or `→` to view tracks inside a playlist
3. **Add track**: press `a` to add the currently playing track
4. **Delete playlist**: press `d` then `y` to confirm deletion
5. **Remove track**: open a playlist, highlight a track, press `d` to remove it
6. **Play all**: press `Enter` on the track list to load all tracks into the player
7. **New playlist**: select "+ New Playlist...", type a name, and press Enter

The directory `~/.config/cliamp/playlists/` is created automatically on first use. Removing the last track from a playlist auto-deletes the file.

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
| `p` | Open/close playlist manager |
| `Up` `Down` / `j` `k` | Navigate |
| `Enter` / `→` | Open playlist / Play all tracks |
| `a` | Add currently playing track |
| `d` | Delete playlist (confirms) / Remove track |
| `Esc` / `←` | Close / Go back |

