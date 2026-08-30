# CLI Flags

Override a config option for one session without editing `~/.config/cliamp/config.toml`. Put flags before or after file and URL arguments.

## Playback

```sh
cliamp --vol -5 track.mp3             # volume in dB [-30, +6]
cliamp --shuffle ~/Music              # enable shuffle
cliamp --repeat all ~/Music           # repeat mode: off, all, one
cliamp --mono track.mp3               # downmix to mono
cliamp --no-mono track.mp3            # force stereo
cliamp --auto-play ~/Music            # start playback immediately
cliamp --playlist "Blade Runner"      # load a local TOML playlist (add --auto-play to start playback)
```

## Audio engine

```sh
cliamp --sample-rate 48000 track.mp3      # output sample rate (22050, 44100, 48000, 96000, 192000)
cliamp --buffer-ms 2000 track.mp3         # speaker buffer in ms (50-5000; useful for unstable radio)
cliamp --resample-quality 1 track.mp3     # resample quality factor (1–4)
cliamp --bit-depth 32 track.m4a           # PCM bit depth: 16 (default) or 32 (lossless)
```

## Appearance

```sh
cliamp --simplified ~/Music                  # no visualizer or playlist
cliamp --eq-preset "Bass Boost" ~/Music
cliamp --visualizer-60fps ~/Music            # smoother visualizer animation (higher CPU use)
```

`--visualizer-60fps` renders a visible visualizer at about 60 FPS during playback. `Wave`, `Scope`, and `Heartbeat` already use this rate because they draw from audio samples. The flag does not affect overlays or low-power mode.

## Diagnostics

```sh
cliamp --log-level debug                     # raise log verbosity for one session
```

cliamp writes logs to `~/.config/cliamp/cliamp.log`. Use `debug`, `info` (default), `warn`, or `error`.

## Low-power mode

```sh
cliamp --low-power track.mp3                 # reduce CPU load for this session
```

This reduces CPU load. It lowers the UI and render rate and sets the visualizer to `none`. Use it on battery power, slow terminals, or SSH sessions. Press `v` in the player to enable a visualizer again.

To make this persistent, set it in `~/.config/cliamp/config.toml`:

```toml
low_power = true
```

## Headless daemon mode

```sh
cliamp --daemon                              # no TUI, IPC only
cliamp --daemon --auto-play --playlist Lofi  # start playing on launch
cliamp -d ~/Music --auto-play                # short flag form
```

Run cliamp without a UI. It listens on the Unix socket that the TUI uses. All `cliamp <subcommand>` IPC clients work. UI-only commands (`theme`, `vis`) return an error. See [Headless Daemon Mode](headless.md) for use cases and configuration examples for Waybar, Hyprland, systemd, and cron.

## Search

Search for and play a track from the command line. This requires [yt-dlp](https://github.com/yt-dlp/yt-dlp):

```sh
cliamp search "never gonna give you up"       # search YouTube
cliamp search-sc "lofi beats"                  # search SoundCloud
```

Press `Ctrl+F` in the player for context-aware search. cliamp uses the active provider search when available. Otherwise, it searches YouTube.

## Updates

```sh
cliamp upgrade                # latest stable release
cliamp upgrade --prerelease   # latest beta or release candidate
```

Select prerelease updates for each upgrade. Normal installs, package-manager updates, and `cliamp upgrade` use stable releases only.

Maintainers publish a prerelease by tagging the release commit with a SemVer prerelease tag such as `v1.5.0-beta.1` or `v1.5.0-rc.1`. The release workflow builds binaries for all platforms and creates a GitHub prerelease. It does not update Homebrew, AUR, or the public changelog.

## General

| Flag | Short | Description |
|------|-------|-------------|
| `--help` | `-h` | Show help and exit |
| `--version` | `-v` | Print version and exit |

## Shell completion

Generate completion scripts for supported shells.

```sh
cliamp completion bash
cliamp completion zsh
cliamp completion fish
cliamp completion pwsh
```

Source the generated script directly or install it as your shell documentation describes.

## Mixing flags and files

Put flags before, after, or between positional arguments:

```sh
cliamp --shuffle track.mp3 --volume -5
cliamp track.mp3 --repeat all --mono ~/Music
```

## Flag reference

| Flag | Type | Default | Range / Values |
|------|------|---------|----------------|
| `--vol` | float | 0 | -30 to +6 dB |
| `--shuffle` | bool | false | |
| `--repeat` | string | off | off, all, one |
| `--mono` / `--no-mono` | bool | false | |
| `--auto-play` | bool | false | |
| `--simplified` | bool | false | artist/title and time strip; no visualizer or playlist |
| `--visualizer-60fps` | bool | false | render a visible visualizer at about 60 FPS |
| `--start-theme` | string | | theme name |
| `--eq-preset` | string | | preset name |
| `--sample-rate` | int | 44100 | 22050, 44100, 48000, 96000, 192000 |
| `--buffer-ms` | int | 250 | 50-5000 |
| `--resample-quality` | int | 4 | 1-4 |
| `--bit-depth` | int | 16 | 16, 32 |
| `--playlist` | string | | local TOML playlist name |
| `--log-level` | string | info | debug, info, warn, error |
| `--low-power` | bool | false | lower UI cadence; disable visualization |
| `--daemon` / `-d` | bool | false | run headless; IPC only, no TUI |

CLI flags override config file values for the current session only. cliamp does not save them.

## Setup wizard

Configure remote providers through a small TUI. Supported providers are Navidrome, Lyrion, Plex, Jellyfin, Emby, Spotify, Qobuz, Tidal, Mixcloud, NetEase, Audiobookshelf, and YouTube Music. Each provider page links to its required credentials. The wizard writes the `[provider]` block to `~/.config/cliamp/config.toml` and leaves the rest of the file unchanged. It validates supported server connections during setup. OAuth providers (Spotify, Qobuz, Tidal) authenticate later in the player. Mixcloud checks optional browser-session or OAuth credentials when you use them.

```sh
cliamp setup
```

Use `↑/↓` to navigate. Use `Enter` to confirm or submit. Use `Esc` to go back. Use `q` in the menu to quit. Passwords and tokens are masked. Running setup again for a configured provider replaces its section.

## Playlist Management

Manage local TOML playlists from the command line without opening the TUI.

```sh
cliamp playlist list                          # list playlists with track counts
cliamp playlist create "Name"                 # create an empty playlist
cliamp playlist create "Name" file1 dir/ ...  # create from files/folders (recursive, skips duplicate paths)
cliamp playlist create "Name" --ssh HOST dir/ # create from remote machine via SSH
cliamp playlist create "Name" --dir ~/Music   # reference a directory as a [[dir]] source (scanned at load)
cliamp playlist add "Name" file1 ...          # append tracks to existing playlist, skipping duplicates
cliamp playlist add "Name" --dir ~/Music      # add another directory source
cliamp playlist dirs "Name"                   # list directory sources
cliamp playlist rename "Old" "New"            # rename a playlist
cliamp playlist show "Name"                   # display tracks
cliamp playlist show "Name" --json            # machine-readable output
cliamp playlist remove "Name" --index 3       # remove track by index
cliamp playlist dedupe "Name"                 # remove duplicate paths, keeping the first
cliamp playlist sort "Name" --by album         # sort in place
cliamp playlist doctor [Name]                  # report missing local files
cliamp playlist doctor "Name" --fix            # prune missing local files
cliamp playlist export "Name" -o mix.m3u       # export as M3U
cliamp playlist export "Name" --format pls     # export as PLS to stdout
cliamp playlist import mix.m3u --name "Name"   # import local M3U/M3U8/PLS
cliamp playlist bookmark "Name" --index 3      # toggle bookmark flag
cliamp playlist bookmarks                       # list bookmarked tracks
cliamp playlist enrich "Name"                   # probe duration/album 
cliamp playlist enrich "Name" --source metadata   # probe duration/album (forces to use the file's metadata as source)
cliamp playlist delete "Name"                   # delete entire playlist
```

Sort keys: `track`, `title`, `artist`, `album`, `artist+album`, `path`.

See [playlists.md](playlists.md) for the TOML format. See [ssh-streaming.md](ssh-streaming.md) for remote playback.

## Recently Played

```sh
cliamp history                                # show the 50 most recent plays
cliamp history --limit 200                    # change the cap
cliamp history --json                         # machine-readable output
cliamp history clear                          # wipe ~/.config/cliamp/history.toml
```

cliamp records a play after you listen to at least 50% of a track. In the TUI,
the Local Playlists provider shows this data in the virtual "Recently Played"
entry. See [history.md](history.md).

## Spotify

```sh
cliamp spotify reset                          # clear stored Spotify credentials
```

Use `spotify reset` for persistent `rate-limited on /v1/me` warnings or stale authentication errors. Then restart cliamp and select Spotify to sign in again. See [spotify.md](spotify.md) for the setup guide.

## cliamp:// Links

Open a song from a browser, a script, or anything that can open a URL:

```sh
cliamp open 'cliamp://play?url=https://example.com/s.mp3'    # play a stream
cliamp open 'cliamp://play?provider=navidrome&album=a1b2c3'  # play an album
cliamp open 'cliamp://queue?provider=ytmusic&q=aphex+twin'   # queue a search hit
cliamp protocol status                                       # where it is registered
cliamp protocol register                                     # register the scheme
cliamp protocol unregister                                   # remove it
```

install.sh registers the scheme. See [url-scheme.md](url-scheme.md) for the
URI format and what links cannot do.

## Remote Control (IPC)

Control a running cliamp instance from another terminal:

```sh
cliamp play / pause / toggle / stop    # playback control
cliamp next / prev                     # track navigation
cliamp status                          # current state
cliamp status --json                   # machine-readable state
cliamp volume -5                       # adjust volume (dB)
cliamp seek 30                         # seek relative to current position (seconds)
cliamp load "Playlist Name"            # load a playlist
cliamp queue /path/to/file.mp3         # queue a track
cliamp shuffle [on|off|toggle]         # toggle or set shuffle
cliamp repeat [off|all|one|cycle]      # set or cycle repeat mode
cliamp mono [on|off|toggle]            # toggle or set mono output
cliamp speed 1.5                       # set playback speed (0.25–2.0)
cliamp eq Rock                         # set EQ preset by name
cliamp eq --band 0 6.0                 # set EQ band 0 to +6 dB
cliamp device list                     # list audio output devices
cliamp device "My DAC"                 # switch audio output device
cliamp remote state                     # v2 GUI-ready runtime snapshot
cliamp remote capabilities              # v2 operation list
cliamp remote events runtime.state      # v2 event stream
```

See [remote-control.md](remote-control.md) for the protocol specification.
