[![Docs on contextowl.co](https://contextowl.co/uploads/_brand/badge-docs.svg)](https://contextowl.co)

A retro terminal music player inspired by Winamp. Play local files, streams, podcasts, YouTube, YouTube Music, SoundCloud, Bilibili, Spotify, NetEase Cloud Music, Xiaoyuzhou (小宇宙), Navidrome, Plex, Jellyfin, and Audiobookshelf with a spectrum visualizer, parametric EQ, and playlist management.

**[cliamp.stream](https://cliamp.stream)** · **[docs](https://whiterose.org.contextowl.co/docs/cliamp)**

Built with [Bubbletea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), [Beep](https://github.com/gopxl/beep), and [go-librespot](https://github.com/devgianlu/go-librespot).


https://github.com/user-attachments/assets/fbc33d20-e3ac-4a62-a991-8a2f0243c8ea

<div align="center">
  <a href="https://contextowl.co"><img src="https://contextowl.co/uploads/_brand/sponsor-dark.svg" alt="Proudly sponsored by contextowl.co" width="400"></a>
</div>

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

**Go**

```sh
go install github.com/bjarneo/cliamp@latest
```

Linux builds need ALSA development headers installed first. See [Building from source](#building-from-source).

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
>
> **Windows:** download `cliamp-windows-amd64.exe` from Releases. If `HOME` is not
> set, cliamp stores its config under `%APPDATA%\cliamp`.

**Optional runtime dependencies** (all platforms, all install methods):

- [ffmpeg](https://ffmpeg.org/) — for AAC, ALAC, Opus, and WMA playback
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — for YouTube, YouTube Music, SoundCloud, Bandcamp, Bilibili, and NetEase Cloud Music

On macOS: `brew install ffmpeg yt-dlp`. On Linux, use your distribution's package manager.

On Windows, install `ffmpeg` and `yt-dlp` with your preferred package manager and keep both on `PATH`.

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

**Configure remote providers** (Navidrome, Plex, Jellyfin, Audiobookshelf, Spotify, YouTube Music, NetEase Cloud Music) with the interactive wizard:

```sh
cliamp setup
```

It walks you through each provider, validates the connection, and writes the right block to your config file (`~/.config/cliamp/config.toml`, or `%APPDATA%\cliamp\config.toml` on Windows when `HOME` is unset). See [docs/cli.md](docs/cli.md#setup-wizard) for details.

## Radio

Press `R` in the player to browse and search 30,000+ online radio stations from the [Radio Browser](https://www.radio-browser.info/) directory.

Add your own stations to `~/.config/cliamp/radios.toml` (or `%APPDATA%\cliamp\radios.toml` on Windows when `HOME` is unset). See [docs/configuration.md](docs/configuration.md#custom-radio-stations).

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
sudo dnf install alsa-lib-devel libvorbis-devel flac-devel
```

**Linux (Arch):**

```sh
sudo pacman -S alsa-lib
```

**macOS:** No extra dependencies — CoreAudio is used.

**Windows:** No extra SDKs required for the core player — it uses pure-Go audio decoding. `ffmpeg.exe` and `yt-dlp.exe` remain optional runtime dependencies for the same formats/providers as on other platforms.

Spotify support uses `go-librespot`, which needs CGO and a MinGW toolchain:

1. Install [MSYS2](https://www.msys2.org/).
2. Open the **MSYS2 MinGW64** terminal (not the plain MSYS2 terminal) and install the toolchain and codec libraries:
   ```sh
   pacman -S mingw-w64-x86_64-gcc mingw-w64-x86_64-pkg-config \
     mingw-w64-x86_64-libogg mingw-w64-x86_64-libvorbis \
     mingw-w64-x86_64-flac mingw-w64-x86_64-mpg123
   ```
3. From that same MinGW64 terminal (so `gcc`/`pkg-config` are on `PATH`), build with CGO enabled:
   ```sh
   CGO_ENABLED=1 go build -o cliamp.exe .
   ```
   Some MSYS2 `libogg` builds ship a `libogg-0.dll` whose export table is missing `ogg_stream_iovecin`, even though it's present in the static `libogg.a`. If the link fails with `undefined reference to 'ogg_stream_iovecin'`, force static linking of just that library:
   ```sh
   CGO_LDFLAGS="-Wl,-Bstatic -logg -Wl,-Bdynamic" CGO_ENABLED=1 go build -o cliamp.exe .
   ```
4. `cliamp.exe` dynamically links `libvorbis`, `libvorbisenc`, `libvorbisfile`, `libFLAC`, and `libmpg123` from MSYS2. Either keep `C:\msys64\mingw64\bin` on `PATH` at runtime, or copy `libvorbis-0.dll`, `libvorbisenc-2.dll`, `libvorbisfile-3.dll`, `libFLAC.dll`, and `libmpg123-0.dll` next to `cliamp.exe`.

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
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) — for YouTube, SoundCloud, Bandcamp, Bilibili, and NetEase Cloud Music

## Docs

Full documentation is hosted at **[whiterose.org.contextowl.co/docs/cliamp](https://whiterose.org.contextowl.co/docs/cliamp)**.

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
