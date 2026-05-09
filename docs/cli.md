# CLI Flags

Override any config option for a single session without editing `~/.config/cliamp/config.toml`. Flags can appear before or after file/URL arguments.

## Playback

```sh
cliamp --volume -5 track.mp3          # volume in dB [-30, +6]
cliamp --shuffle ~/Music              # enable shuffle
cliamp --repeat all ~/Music           # repeat mode: off, all, one
cliamp --mono track.mp3               # downmix to mono
cliamp --no-mono track.mp3            # force stereo
cliamp --auto-play ~/Music            # start playback immediately
cliamp --playlist "Blade Runner"      # load a local TOML playlist and start playing
```

## Audio engine

```sh
cliamp --sample-rate 48000 track.mp3      # output sample rate (22050, 44100, 48000, 96000, 192000)
cliamp --buffer-ms 200 track.mp3          # speaker buffer in ms (50–500)
cliamp --resample-quality 1 track.mp3     # resample quality factor (1–4)
cliamp --bit-depth 32 track.m4a           # PCM bit depth: 16 (default) or 32 (lossless)
```

## Appearance

```sh
cliamp --compact ~/Music                     # cap width at 80 columns
cliamp --eq-preset "Bass Boost" ~/Music
```

## Diagnostics

```sh
cliamp --log-level debug                     # raise log verbosity for one session
```

Logs are written to `~/.config/cliamp/cliamp.log`. Levels: `debug`, `info` (default), `warn`, `error`.

## Low-power mode

```sh
cliamp --low-power track.mp3                 # minimize CPU: visualizer off
```

Forces the visualizer to `none` so the TUI ticks at 5 FPS for the time/seek display only. Useful on battery, slow terminals, or SSH sessions. Press `v` in the player to cycle visualizers back on at any time.

## Headless daemon mode

```sh
cliamp --daemon                              # no TUI, IPC only
cliamp --daemon --auto-play --playlist Lofi  # start playing on launch
cliamp -d ~/Music --auto-play                # short flag form
```

Runs cliamp without rendering a UI, listening on the same Unix socket the TUI uses. All `cliamp <subcommand>` IPC clients keep working. UI-specific commands (`theme`, `vis`) return an error in this mode. See [Headless Daemon Mode](headless.md) for use cases and example configs (Waybar, Hyprland, systemd, cron).

## Search

Search and play a track directly from the command line (requires [yt-dlp](https://github.com/yt-dlp/yt-dlp)):

```sh
cliamp search "never gonna give you up"       # search YouTube
cliamp search-sc "lofi beats"                  # search SoundCloud
```

Press `Ctrl+F` in the player for context-aware search: it runs the active provider's native search when available or falls back to YouTube search.

## General

| Flag | Short | Description |
|------|-------|-------------|
| `--help` | `-h` | Show help and exit |
| `--version` | `-v` | Print version and exit |
| `--upgrade` | | Update to the latest release |

## Mixing flags and files

Flags can appear before, after, or between positional arguments:

```sh
cliamp --shuffle track.mp3 --volume -5
cliamp track.mp3 --repeat all --mono ~/Music
```

## Flag reference

| Flag | Type | Default | Range / Values |
|------|------|---------|----------------|
| `--volume` | float | 0 | -30 to +6 dB |
| `--shuffle` | bool | false | |
| `--repeat` | string | off | off, all, one |
| `--mono` / `--no-mono` | bool | false | |
| `--auto-play` | bool | false | |
| `--compact` | bool | false | |
| `--theme` | string | | theme name |
| `--eq-preset` | string | | preset name |
| `--sample-rate` | int | 44100 | 22050, 44100, 48000, 96000, 192000 |
| `--buffer-ms` | int | 100 | 50–500 |
| `--resample-quality` | int | 4 | 1–4 |
| `--bit-depth` | int | 16 | 16, 32 |
| `--playlist` | string | | local TOML playlist name |
| `--log-level` | string | info | debug, info, warn, error |
| `--low-power` | bool | false | disables visualizer; cuts redraw + FFT cost |
| `--daemon` / `-d` | bool | false | run headless; IPC only, no TUI |

CLI flags override config file values for the current session only. They are not persisted.

## Setup wizard

Configure remote providers (Navidrome, Plex, Jellyfin, Emby, Spotify, NetEase, YouTube Music) through a small TUI. Each provider page links to where to find the required credentials, validates the connection live, and writes the resulting `[provider]` block to `~/.config/cliamp/config.toml` without disturbing the rest of the file.

```sh
cliamp setup
```

Keys: `↑/↓` to navigate, `Enter` to confirm or submit, `Esc` to back out, `q` from the menu to quit. Passwords and tokens are masked. Running setup again for an already-configured provider replaces its section in place.

## Playlist Management

Manage local TOML playlists from the command line without opening the TUI.

```sh
cliamp playlist list                          # list playlists with track counts
cliamp playlist create "Name" file1 dir/ ...  # create from files/folders (recursive)
cliamp playlist create "Name" --ssh HOST dir/ # create from remote machine via SSH
cliamp playlist add "Name" file1 ...          # append tracks to existing playlist
cliamp playlist show "Name"                   # display tracks
cliamp playlist show "Name" --json            # machine-readable output
cliamp playlist remove "Name" --index 3       # remove track by index
cliamp playlist delete "Name"                 # delete entire playlist
```

See [playlists.md](playlists.md) for the TOML format and [ssh-streaming.md](ssh-streaming.md) for remote playback.

## Recently Played

```sh
cliamp history                                # show the 50 most recent plays
cliamp history --limit 200                    # change the cap
cliamp history --json                         # machine-readable output
cliamp history clear                          # wipe ~/.config/cliamp/history.toml
```

A play is recorded once you've listened to a track for at least 50% of its
duration. Inside the TUI, the same data appears as the virtual "Recently
Played" entry in the Local Playlists provider. See [history.md](history.md).

## Spotify

```sh
cliamp spotify reset                          # clear stored Spotify credentials
```

Use `spotify reset` if you see persistent `rate-limited on /v1/me` warnings or stale auth errors. After running it, relaunch cliamp and select Spotify to sign in again. See [spotify.md](spotify.md) for the full setup guide.

## Remote Control (IPC)

Control a running cliamp instance from another terminal:

```sh
cliamp play / pause / toggle / stop    # playback control
cliamp next / prev                     # track navigation
cliamp status                          # current state
cliamp status --json                   # machine-readable state
cliamp volume -5                       # adjust volume (dB)
cliamp seek 30                         # seek to position (seconds)
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
```

See [remote-control.md](remote-control.md) for the full protocol specification.
