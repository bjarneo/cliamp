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

## Search

Search and play a track directly from the command line (requires [yt-dlp](https://github.com/yt-dlp/yt-dlp)):

```sh
cliamp search "never gonna give you up"       # search YouTube
cliamp search-sc "lofi beats"                  # search SoundCloud
```

Press `f` in the player to search YouTube interactively, or `F` (Shift+F) to search SoundCloud.

## General

| Flag | Short | Description |
|------|-------|-------------|
| `--help` | `-h` | Show help and exit |
| `--version` | `-v` | Print version and exit |
| `--upgrade` | | Update to the latest release |

## Mixing flags and files

Flags can appear anywhere — before, after, or between positional arguments:

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

CLI flags override config file values for the current session only. They are not persisted.

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
```

See [remote-control.md](remote-control.md) for the full protocol specification.
