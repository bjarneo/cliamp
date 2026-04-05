A retro terminal music player inspired by Winamp. Play local files, streams, podcasts, YouTube, YouTube Music, SoundCloud, Bilibili, Spotify, Xiaoyuzhou (小宇宙), Navidrome, Plex, and Jellyfin with a spectrum visualizer, parametric EQ, and playlist management.

**[cliamp.stream](https://cliamp.stream)**

Built with [Bubbletea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), [Beep](https://github.com/gopxl/beep), and [go-librespot](https://github.com/devgianlu/go-librespot).


https://github.com/user-attachments/assets/fbc33d20-e3ac-4a62-a991-8a2f0243c8ea


## Radio

Tune in to our radio channel:

```sh
cliamp https://radio.cliamp.stream/lofi/stream.pls
```

Press `R` in the player to browse and search 30,000+ online radio stations from the [Radio Browser](https://www.radio-browser.info/) directory.

Add your own stations to `~/.config/cliamp/radios.toml`. See [docs/configuration.md](docs/configuration.md#custom-radio-stations).

Want to host your own radio? Check out [cliamp-server](https://github.com/bjarneo/cliamp-server).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/bjarneo/cliamp/HEAD/install.sh | sh
```

**Homebrew**

```sh
brew install bjarneo/cliamp/cliamp
```

**Arch Linux (AUR)**

```sh
yay -S cliamp
```

**Pre-built binaries**

Download from [GitHub Releases](https://github.com/bjarneo/cliamp/releases/latest).

**Build from source**

```sh
git clone https://github.com/bjarneo/cliamp.git && cd cliamp && go build -o cliamp .
```

## Quick Start

```sh
cliamp ~/Music                     # play a directory
cliamp *.mp3 *.flac               # play files
cliamp https://example.com/stream  # play a URL
```

Press `Ctrl+K` to see all keybindings.

## Building from source

**Prerequisites:**

- [Go](https://go.dev/dl/) 1.25.5 or later
- ALSA development headers (Linux only — required by the audio backend)

**Linux (Debian/Ubuntu):**

```sh
sudo apt install libasound2-dev
```

**Linux (Fedora):**

```sh
sudo dnf install alsa-lib-devel
```

**Linux (Arch):**

```sh
sudo pacman -S alsa-lib
```

**macOS:** No extra dependencies — CoreAudio is used.

**Clone and build:**

```sh
git clone https://github.com/bjarneo/cliamp.git
cd cliamp
make && make install
```

Or without Make: `go build -o cliamp .`

`make install` places the binary in `~/.local/bin/`.

**Optional runtime dependencies:**

- [ffmpeg](https://ffmpeg.org/) — for AAC, ALAC, Opus, and WMA playback
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — for YouTube, SoundCloud, Bandcamp, and Bilibili

## Docs

- [Configuration](docs/configuration.md)
- [Keybindings](docs/keybindings.md)
- [CLI Flags](docs/cli.md)
- [Streaming](docs/streaming.md)
- [Playlists](docs/playlists.md)
- [YouTube, SoundCloud, Bandcamp and Bilibili](docs/yt-dlp.md)
- [YouTube Music](docs/youtube-music.md)
- [Lyrics](docs/lyrics.md)
- [Spotify](docs/spotify.md)
- [Navidrome](docs/navidrome.md)
- [Plex](docs/plex.md)
- [Jellyfin](docs/jellyfin.md)
- [Themes](docs/themes.md)
- [SSH Streaming](docs/ssh-streaming.md)
- [Remote Control (IPC)](docs/remote-control.md)
- [Audio Quality](docs/audio-quality.md)
- [MPRIS](docs/mpris.md)
- [Lua Plugins](docs/plugins.md)
  - [Community Plugins](docs/community-plugins.md)
  - [Soap Bubbles Visualizer](https://github.com/bjarneo/cliamp-plugin-soap-bubbles)

## Troubleshooting

**No audio output (silence with no errors)**

On Linux systems using PipeWire or PulseAudio, cliamp's ALSA backend needs a bridge package to route audio through your sound server:

- **PipeWire:** `pipewire-alsa`
- **PulseAudio:** `pulseaudio-alsa`

Install the appropriate package for your system:

```sh
# PipeWire (Arch)
sudo pacman -S pipewire-alsa

# PulseAudio (Arch)
sudo pacman -S pulseaudio-alsa

# Debian/Ubuntu (PipeWire)
sudo apt install pipewire-alsa
```

## Omarchy

Add this keybind to launch cliamp with `Super+Shift+M`:

```
bindd = SUPER SHIFT, M, Music, exec, omarchy-launch-tui cliamp
```

## Author

[x.com/iamdothash](https://x.com/iamdothash)

## Disclaimer

Use this software at your own risk. We are not responsible for any damages or issues that may arise from using this software.
