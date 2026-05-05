A retro terminal music player inspired by Winamp. Play local files, streams, podcasts, YouTube, YouTube Music, SoundCloud, Bilibili, Spotify, Xiaoyuzhou (小宇宙), Navidrome, Plex, and Jellyfin with a spectrum visualizer, parametric EQ, and playlist management.

**[cliamp.stream](https://cliamp.stream)**

Built with [Bubbletea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), [Beep](https://github.com/gopxl/beep), and [go-librespot](https://github.com/devgianlu/go-librespot).


https://github.com/user-attachments/assets/fbc33d20-e3ac-4a62-a991-8a2f0243c8ea


## Install

```sh
curl -fsSL https://raw.githubusercontent.com/bjarneo/cliamp/HEAD/install.sh | sh
```

**Homebrew**

```sh
brew install bjarneo/cliamp/cliamp
```

The formula pulls in all required runtime libraries automatically.

**Arch Linux (AUR)**

```sh
yay -S cliamp
```

**Pre-built binaries**

Download from [GitHub Releases](https://github.com/bjarneo/cliamp/releases/latest).

> **macOS:** the pre-built binaries dynamically link against FLAC, Vorbis, and Ogg
> from Homebrew. If you download directly from Releases (or use the `install.sh`
> script) you must install them first, otherwise you will see errors like
> `Library not loaded: /opt/homebrew/opt/libvorbis/lib/libvorbisenc.2.dylib`:
>
> ```sh
> brew install flac libvorbis libogg
> ```
>
> Installing via `brew install bjarneo/cliamp/cliamp` does this for you.
>
> **Linux:** the pre-built binaries statically link FLAC, Vorbis, and Ogg, so no
> extra codec packages are required. You may still need an ALSA bridge for your
> sound server — see [Troubleshooting](#troubleshooting).

**Optional runtime dependencies** (all platforms, all install methods):

- [ffmpeg](https://ffmpeg.org/) — for AAC, ALAC, Opus, and WMA playback
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — for YouTube, YouTube Music, SoundCloud, Bandcamp, and Bilibili

On macOS: `brew install ffmpeg yt-dlp`. On Linux, use your distribution's package manager.

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

**Configure remote providers** (Navidrome, Plex, Jellyfin, Spotify, YouTube Music) with the interactive wizard:

```sh
cliamp setup
```

It walks you through each provider, validates the connection, and writes the right block to `~/.config/cliamp/config.toml`. See [docs/cli.md](docs/cli.md#setup-wizard) for details.

## Radio

Press `R` in the player to browse and search 30,000+ online radio stations from the [Radio Browser](https://www.radio-browser.info/) directory.

Add your own stations to `~/.config/cliamp/radios.toml`. See [docs/configuration.md](docs/configuration.md#custom-radio-stations).

Want to host your own radio? Check out [cliamp-server](https://github.com/bjarneo/cliamp-server).

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
- [SoundCloud](docs/soundcloud.md)
- [Lyrics](docs/lyrics.md)
- [Spotify](docs/spotify.md)
- [Navidrome](docs/navidrome.md)
- [Plex](docs/plex.md)
- [Jellyfin](docs/jellyfin.md)
- [Themes](docs/themes.md)
- [SSH Streaming](docs/ssh-streaming.md)
- [Remote Control (IPC)](docs/remote-control.md)
- [Audio Quality](docs/audio-quality.md)
- [Media Controls](docs/mediactl.md)
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

## Author

[x.com/iamdothash](https://x.com/iamdothash)

## Disclaimer

Use this software at your own risk. We are not responsible for any damages or issues that may arise from using this software.
