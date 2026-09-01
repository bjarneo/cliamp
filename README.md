[![Docs on contextowl.co](https://contextowl.co/uploads/_brand/badge-docs.svg)](https://contextowl.co)

A retro terminal music player inspired by Winamp. Play local files, streams, podcasts, YouTube, YouTube Music, SoundCloud, Mixcloud, Bilibili, Spotify, NetEase Cloud Music, Xiaoyuzhou (小宇宙), Navidrome, Lyrion, Plex, Jellyfin, and Audiobookshelf. Use the spectrum visualizer, parametric EQ, and playlist manager.

**[cliamp.stream](https://cliamp.stream)** | **[docs](https://whiterose.org.contextowl.co/docs/cliamp)**

cliamp uses [Bubbletea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss), [Beep](https://github.com/gopxl/beep), and [go-librespot](https://github.com/devgianlu/go-librespot).


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

The formula installs all required runtime libraries.

**Arch Linux (AUR)**

```sh
yay -S cliamp
```

**Nix**

```sh
nix run github:bjarneo/cliamp
```

For a declarative NixOS configuration, add `github:bjarneo/cliamp` as a flake
input. Install its default package:

```nix
inputs.cliamp.url = "github:bjarneo/cliamp";

environment.systemPackages = [
  inputs.cliamp.packages.${pkgs.stdenv.hostPlatform.system}.default
];
```

**Go**

```sh
go install github.com/bjarneo/cliamp@latest
```

Install ALSA development headers before you build on Linux. See [Building from source](#building-from-source).

**Pre-built binaries**

Download from [GitHub Releases](https://github.com/bjarneo/cliamp/releases/latest).

> **macOS:** Pre-built binaries link dynamically to FLAC, Vorbis, Ogg, and mpg123
> from Homebrew. If you download from Releases or use `install.sh`, install these
> libraries first. Otherwise, you can see errors such as
> `Library not loaded: /opt/homebrew/opt/libvorbis/lib/libvorbisenc.2.dylib`:
>
> ```sh
> brew install flac libvorbis libogg mpg123
> ```
>
> `brew install bjarneo/cliamp/cliamp` installs these libraries.
>
> **Linux:** Pre-built binaries link FLAC, Vorbis, Ogg, and mpg123 statically. No
> extra codec packages are required. You can still need an ALSA bridge for your
> sound server. See [Troubleshooting](#troubleshooting).
>
> **Windows:** Download and extract `cliamp-windows-amd64.zip` from Releases. It
> includes the codec DLLs that Spotify requires. If `HOME` is not set, cliamp stores
> its config under `%APPDATA%\cliamp`.

**Optional runtime dependencies** for all platforms and install methods:

- [ffmpeg](https://ffmpeg.org/) for AAC, ALAC, Opus, and WMA playback
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) for YouTube, YouTube Music, SoundCloud, Mixcloud, Bandcamp, Bilibili, and NetEase Cloud Music

On macOS, run `brew install ffmpeg yt-dlp`. On Linux, use your distribution package manager.

On Windows, install `ffmpeg` and `yt-dlp` with your package manager. Keep both on `PATH`.

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

**Configure remote providers** such as Navidrome, Lyrion, Plex, Jellyfin, Audiobookshelf, Spotify, Mixcloud, YouTube Music, and NetEase Cloud Music with the interactive wizard:

```sh
cliamp setup
```

The wizard guides you through each provider. It writes the required block to your config file (`~/.config/cliamp/config.toml`, or `%APPDATA%\cliamp\config.toml` on Windows when `HOME` is unset). It validates supported server connections during setup. It checks optional Mixcloud browser-session or OAuth credentials when you use them. See [docs/cli.md](docs/cli.md#setup-wizard) for details.

See the [Mixcloud provider guide](docs/mixcloud.md) for discovery, account,
creator/show, genre search, local genre favorites, authentication, signed-in
playback, resume, seeking, and limitations.

## Radio

Press `R` in the player to browse about 58,000 online radio stations in the [Radio Browser](https://www.radio-browser.info/) directory.

Cut that down by location: `N` browses by country, `f` pins the countries you listen to, and `Enter` on a country loads its stations as a playlist. cliamp does not work out where you are unless you pick "Use my location" and accept; it then reads your country from the system timezone, never from a geo-IP service. See [docs/radio.md](docs/radio.md).

Add your own stations to `~/.config/cliamp/radios.toml` (or `%APPDATA%\cliamp\radios.toml` on Windows when `HOME` is unset). See [docs/configuration.md](docs/configuration.md#custom-radio-stations).

To host a radio station, use [cliamp-server](https://github.com/bjarneo/cliamp-server).

## Building from source

**Prerequisites:**

- [Go](https://go.dev/dl/) 1.25.5 or later
- ALSA development headers (Linux only, required by the audio backend)

**Linux (Debian/Ubuntu):**

```sh
sudo apt install libasound2-dev libflac-dev libvorbis-dev libogg-dev libmpg123-dev
```

**Linux (Fedora):**

```sh
sudo dnf install alsa-lib-devel flac-devel libvorbis-devel libogg-devel mpg123-devel
```

**Linux (Arch):**

```sh
sudo pacman -S alsa-lib flac libvorbis libogg mpg123
```

**macOS:** `brew install flac libvorbis libogg mpg123 pkg-config`

**Windows:** The core player needs no extra SDKs. It uses pure-Go audio decoding. `ffmpeg.exe` and `yt-dlp.exe` remain optional runtime dependencies for the same formats and providers as other platforms.

Spotify support uses `go-librespot`. It needs CGO and a MinGW toolchain:

1. Install [MSYS2](https://www.msys2.org/).
2. Open the **MSYS2 MinGW64** terminal, not the standard MSYS2 terminal. Install the toolchain and codec libraries:
   ```sh
   pacman -S mingw-w64-x86_64-gcc mingw-w64-x86_64-pkg-config \
     mingw-w64-x86_64-libogg mingw-w64-x86_64-libvorbis \
     mingw-w64-x86_64-flac mingw-w64-x86_64-mpg123
   ```
3. In that MinGW64 terminal, build with CGO enabled. This keeps `gcc` and `pkg-config` on `PATH`:
   ```sh
   CGO_ENABLED=1 go build -o cliamp.exe .
   ```
   Some MSYS2 `libogg` builds provide `libogg-0.dll` without `ogg_stream_iovecin` in its export table. The static `libogg.a` has this symbol. If linking fails with `undefined reference to 'ogg_stream_iovecin'`, use static linking for this library only:
   ```sh
   CGO_LDFLAGS="-Wl,-Bstatic -logg -Wl,-Bdynamic" CGO_ENABLED=1 go build -o cliamp.exe .
   ```
4. `cliamp.exe` links dynamically to codec and MinGW runtime DLLs. Keep `C:\msys64\mingw64\bin` on `PATH` at runtime, or copy each `/mingw64/bin/*.dll` that `ldd cliamp.exe` shows next to `cliamp.exe`.

**Clone and build:**

```sh
git clone https://github.com/bjarneo/cliamp.git
cd cliamp
make && make install
```

Or without Make: `go build -o cliamp .`

`make install` places the binary in `~/.local/bin/`.

**Optional runtime dependencies:**

- [ffmpeg](https://ffmpeg.org/) for AAC, ALAC, Opus, and WMA playback
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) for YouTube, SoundCloud, Mixcloud, Bandcamp, Bilibili, and NetEase Cloud Music

## Docs

Full documentation is hosted at **[whiterose.org.contextowl.co/docs/cliamp](https://whiterose.org.contextowl.co/docs/cliamp)**.

## Troubleshooting

**No audio output (silence with no errors)**

On Linux systems that use PipeWire or PulseAudio, the cliamp ALSA backend needs a bridge package to route audio through the sound server:

- **PipeWire:** `pipewire-alsa`
- **PulseAudio:** `pulseaudio-alsa`

Install the package for your system:

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

Use this software at your own risk. The authors are not responsible for damage or issues that result from its use.
