# CLIAMP

A retro terminal music player inspired by Winamp 2.x. Plays MP3, WAV, FLAC, OGG, AAC, ALAC, Opus, and WMA with a 10-band spectrum visualizer, 10-band parametric EQ, and playlist management.

Built with [Bubbletea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), and [Beep](https://github.com/gopxl/beep).

Listen to our radio channel:
```bash
cliamp https://radio.cliamp.stream/lofi/stream.pls
```


https://github.com/user-attachments/assets/fbc33d20-e3ac-4a62-a991-8a2f0243c8ea


## Install

```sh
curl -fsSL https://raw.githubusercontent.com/bjarneo/cliamp/HEAD/install.sh | sh
```

### Homebrew (macOS / Linux)

```sh
brew install bjarneo/cliamp/cliamp
```

### Arch Linux (AUR)

```sh
yay -S cliamp
```

## Build

```sh
go build -o cliamp .
```

## Usage

```sh
./cliamp *.mp3 *.flac *.wav *.ogg
./cliamp ~/Music                   # recursively finds all audio files
./cliamp ~/Music/jazz ~/Music/rock # multiple folders
./cliamp ~/Music song.mp3          # mix folders and files
```

## Run in dev

```sh
go run . track.mp3 song.flac
go run . ~/Music/album
```

## HTTP Streaming

Play audio directly from URLs or M3U playlists:

```sh
./cliamp https://example.com/song.mp3
./cliamp http://radio-station.com/stream.m3u
./cliamp local.mp3 https://example.com/remote.mp3   # mix local + remote
```

For non-seekable HTTP streams, the UI shows `● Streaming` with a static seek bar, and seek keys are silently ignored.

## M3U Playlists

Load local or remote `.m3u`/`.m3u8` files with full EXTINF metadata support:

```sh
./cliamp ~/radio-stations.m3u
./cliamp http://radio.example.com/streams.m3u
./cliamp ~/music.m3u local.mp3   # mix M3U with other files
```

Titles from `#EXTINF` lines are displayed in the playlist. Relative paths in local M3U files resolve against the file's directory.

## Local Playlists

Create your own playlists as `.toml` files in `~/.config/cliamp/playlists/`:

```toml
# ~/.config/cliamp/playlists/radio-stations.toml

[[track]]
path = "http://station-1.com/stream"
title = "Radio Station 1"

[[track]]
path = "/home/user/Music/song.mp3"
title = "My Song"
artist = "My Artist"
```

Press `p` to open the playlist manager where you can browse playlists, add the currently playing track, remove tracks, and delete playlists. Select "+ New Playlist..." to create one from scratch.

If you have local playlists or Navidrome configured, press `Esc`/`b` to open the provider browser and switch between playlists. Without any arguments or providers, cliamp connects to the built-in radio channel.

See [docs/playlists.md](docs/playlists.md) for the full guide.

## Podcasts

Play any podcast by passing its RSS feed URL:

```sh
./cliamp https://example.com/podcast/feed.xml
```

Episode titles and the podcast name are extracted from the feed and shown in the playlist.

## YouTube, SoundCloud & Bandcamp (yt-dlp)

Play from YouTube, SoundCloud, and Bandcamp URLs if [yt-dlp](https://github.com/yt-dlp/yt-dlp) is installed:

```sh
./cliamp https://www.youtube.com/watch?v=dQw4w9WgXcQ
./cliamp https://soundcloud.com/artist/track
./cliamp https://artist.bandcamp.com/album/name
```

Playlists and albums are supported. Press `S` to save a downloaded track to `~/Music/cliamp/`.

Search and play directly from the command line:

```sh
cliamp search "never gonna give you up"       # search YouTube
cliamp search-sc "lofi beats"                  # search SoundCloud
```

You can also search interactively while playing by pressing `f` (YouTube) or `F` (SoundCloud).

**Use at your own risk.** Downloading or streaming copyrighted content may violate the terms of service of these platforms. You are responsible for how you use this feature.

## Create Your Own Radio Station

Run your own internet radio with [cliamp-server](https://github.com/bjarneo/cliamp-server) — point it at a directory of audio files and it starts broadcasting:

Supports multiple stations, live metadata, on-the-fly transcoding. See the [cliamp-server README](https://github.com/bjarneo/cliamp-server) for the full setup guide.

## Navidrome

Connect to a [Navidrome](https://www.navidrome.org/) ([GitHub](https://github.com/navidrome/navidrome)) server via environment variables:

```sh
export NAVIDROME_URL="https://your-server.com"
export NAVIDROME_USER="your-username"
export NAVIDROME_PASS="your-password"
./cliamp
```

The app starts in provider mode, letting you browse and play your Navidrome playlists.

### ffmpeg (optional)

AAC, ALAC (`.m4a`), Opus, and WMA playback requires [ffmpeg](https://ffmpeg.org/) installed:

```sh
# Arch
sudo pacman -S ffmpeg
# Debian/Ubuntu
sudo apt install ffmpeg
# macOS
brew install ffmpeg
```

MP3, WAV, FLAC, and OGG work without ffmpeg.

## Configuration

Copy the example config to get started:

```sh
mkdir -p ~/.config/cliamp
cp config.toml.example ~/.config/cliamp/config.toml
```

```toml
# Default volume in dB (range: -30 to 6)
volume = 0

# Repeat mode: "off", "all", or "one"
repeat = "off"

# Start with shuffle enabled
shuffle = false

# Start with mono output (L+R downmix)
mono = false

# Shift+Left/Right seek jump in seconds
seek_large_step_sec = 30

# EQ preset: "Flat", "Rock", "Pop", "Jazz", "Classical",
#             "Bass Boost", "Treble Boost", "Vocal", "Electronic", "Acoustic"
# Leave empty or "Custom" to use manual eq values below
eq_preset = "Flat"

# 10-band EQ gains in dB (range: -12 to 12)
# Bands: 70Hz, 180Hz, 320Hz, 600Hz, 1kHz, 3kHz, 6kHz, 12kHz, 14kHz, 16kHz
# Only used when eq_preset is "Custom" or empty
eq = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0]

# Visualizer mode (leave empty for default Bars)
# Options: Bars, Bricks, Columns, Wave, Scatter, Flame, Retro, None
visualizer = "Bars"

# UI theme name (see available themes in ~/.config/cliamp/themes/)
theme = "Tokyo Night"
```

## CLI Flags

Override config options for a single session:

```sh
cliamp --shuffle --volume -5 track.mp3
cliamp track.mp3 --repeat all --mono
cliamp --auto-play --theme "Amber CRT" ~/Music
cliamp --visualizer Flame ~/Music
cliamp --sample-rate 48000 --buffer-ms 200 track.mp3
cliamp --bit-depth 32 --sample-rate 96000 track.m4a
cliamp --eq-preset "Bass Boost" --visualizer Wave track.mp3
```

Visualizer modes: `Bars`, `Bricks`, `Columns`, `Wave`, `Scatter`, `Flame`, `Retro`, or `None` (hidden).

Flags can appear before, after, or between file arguments. See [docs/cli.md](docs/cli.md) for the full reference.

## Keys

| Key | Action |
|---|---|
| `Space` | Play / Pause |
| `s` | Stop |
| `>` `.` | Next track |
| `<` `,` | Previous track |
| `Left` `Right` | Seek -/+5s |
| `Shift+Left` `Shift+Right` | Seek -/+30s (configurable) |
| `+` `-` | Volume up/down |
| `m` | Toggle mono |
| `Tab` | Toggle focus (Playlist / EQ) |
| `j` `k` / `Up` `Down` | Playlist scroll / EQ band adjust |
| `h` `l` | EQ cursor left/right |
| `Enter` | Play selected track |
| `e` | Cycle EQ preset |
| `t` | Choose theme |
| `v` | Cycle visualizer |
| `V` | Full-screen visualizer |
| `S` | Save track to ~/Music |
| `/` | Search playlist |
| `f` | Find on YouTube (queue play next) |
| `F` | Find on SoundCloud (queue play next) |
| `J` | Jump to time |
| `x` | Expand/collapse playlist |
| `o` | Open file browser |
| `N` | Navidrome browser |
| `a` | Toggle queue (play next) |
| `A` | Queue manager |
| `p` | Playlist manager |
| `r` | Cycle repeat (Off / All / One) |
| `z` | Toggle shuffle |
| `Ctrl+K` | Show keymap |
| `b` `Esc` | Back to provider |
| `q` | Quit |

## Author

[x.com/iamdothash](https://x.com/iamdothash)
