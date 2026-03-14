```
 ██████ ██      ██  █████  ███    ███ ██████
██      ██      ██ ██   ██ ████  ████ ██   ██
██      ██      ██ ███████ ██ ████ ██ ██████
██      ██      ██ ██   ██ ██  ██  ██ ██
 ██████ ███████ ██ ██   ██ ██      ██ ██
```

A retro terminal music player inspired by Winamp. Play local files, streams, podcasts, YouTube, SoundCloud, Bilibili, Spotify, Xiaoyuzhou (小宇宙), and Navidrome with a spectrum visualizer, parametric EQ, and playlist management.

Built with [Bubbletea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), [Beep](https://github.com/gopxl/beep), and [go-librespot](https://github.com/devgianlu/go-librespot).


https://github.com/user-attachments/assets/fbc33d20-e3ac-4a62-a991-8a2f0243c8ea


## Radio

Tune in to our radio channel:

```sh
cliamp https://radio.cliamp.stream/lofi/stream.pls
```

Add your own stations to `~/.config/cliamp/radios.toml`. See [docs/configuration.md](docs/configuration.md#custom-radio-stations).

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

## Docs

- [Configuration](docs/configuration.md)
- [Keybindings](docs/keybindings.md)
- [CLI Flags](docs/cli.md)
- [Streaming](docs/streaming.md)
- [Playlists](docs/playlists.md)
- [YouTube, SoundCloud, Bandcamp and Bilibili](docs/yt-dlp.md)
- [Lyrics](docs/lyrics.md)
- [Spotify](docs/spotify.md)
- [Navidrome](docs/navidrome.md)
- [Themes](docs/themes.md)
- [Audio Quality](docs/audio-quality.md)
- [MPRIS](docs/mpris.md)
- [Telemetry](docs/telemetry.md)

## Author

[x.com/iamdothash](https://x.com/iamdothash)

## Disclaimer

Use this software at your own risk. We are not responsible for any damages or issues that may arise from using this software.
