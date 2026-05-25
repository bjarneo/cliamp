# Remote Control (IPC)

Control a running cliamp instance from another terminal, a shell script, or an AI coding assistant.

When cliamp starts, it listens on a local IPC socket at `~/.config/cliamp/cliamp.sock` (or `%APPDATA%\cliamp\cliamp.sock` on Windows when `HOME` is unset). CLI subcommands connect to this socket to send playback commands and receive status. On Windows 10/11, this uses the same local socket transport via Go's AF_UNIX support.

## Playback Commands

```sh
cliamp play                  # resume playback
cliamp pause                 # pause playback
cliamp toggle                # play/pause toggle
cliamp next                  # next track
cliamp prev                  # previous track
cliamp stop                  # stop playback
```

## Status

```sh
cliamp status                # human-readable current state
cliamp status --json         # machine-readable JSON
```

JSON output:

```json
{
  "ok": true,
  "state": "playing",
  "track": {
    "title": "Imperial March",
    "artist": "John Williams",
    "path": "/path/to/file.mp3"
  },
  "position": 42.5,
  "duration": 183.0,
  "volume": -3,
  "playlist": "Star Wars OT",
  "index": 12,
  "total": 59,
  "visualizer": "ClassicPeak",
  "theme": {
    "name": "Kanagawa Dragon",
    "accent": "#658594",
    "fg": "#c5c9c5",
    "green": "#8a9a7b",
    "yellow": "#c4b28a",
    "red": "#c4746e"
  }
}
```

The `theme` block carries the active cliamp theme's resolved hex colors. Empty hex fields mean the default ANSI fallback theme is active.

## Volume and Seek

```sh
cliamp volume -5             # adjust volume in dB
cliamp seek 30               # seek to position in seconds
```

## Playlist Loading

```sh
cliamp load "Playlist Name"  # load a playlist into the player
cliamp queue /path/to.mp3    # queue a single track
```

## Spectrum Streaming

```sh
cliamp visstream             # NDJSON spectrum frames at 30 fps (default)
cliamp visstream --fps 60    # up to 60 fps; clamped to [1, 60]
```

`visstream` holds a single IPC connection open and emits one JSON line per frame containing the 10-band spectrum and the active visualizer mode name:

```json
{"ok":true,"visualizer":"Bars","bands":[0.93,0.81,0.62,0.48,0.31,0.22,0.14,0.09,0.04,0.01]}
```

Band values are normalized to [0, 1], in the same shape cliamp uses internally for spectrum visualizers. This is what powers the [Quickshell now-playing widget](quickshell.md). Consumers can pipe stdout directly into another process (`cliamp visstream | jq`) or use it from a long-lived subprocess in a UI toolkit.

Under the hood it issues a `{"cmd":"bands"}` request per tick over the existing IPC socket; you can also issue this command directly from your own client if you want frame-pulled access:

```json
{"cmd": "bands"}
```

Response:

```json
{"ok": true, "visualizer": "Bars", "bands": [0.93, 0.81, ...]}
```

## Protocol

The IPC protocol is newline-delimited JSON over a local stream socket. Each request is a single JSON object followed by a newline. The server responds with a single JSON object followed by a newline.

Request format:

```json
{"cmd": "status"}
{"cmd": "next"}
{"cmd": "volume", "value": -5}
{"cmd": "load", "playlist": "Star Wars OT"}
{"cmd": "queue", "path": "/path/to/file.mp3"}
```

Response format:

```json
{"ok": true}
{"ok": true, "state": "playing", "track": {...}, ...}
{"ok": false, "error": "cliamp is not running"}
```

## Socket Details

- **Path**: `~/.config/cliamp/cliamp.sock` (or `%APPDATA%\cliamp\cliamp.sock` on Windows when `HOME` is unset; created on TUI start, removed on shutdown)
- **Permissions**: `0600` (owner only)
- **Stale detection**: A PID file (`cliamp.sock.pid`) tracks the owning process. If cliamp crashes, the next instance detects the stale socket and cleans it up.

## Scripting Examples

```sh
# Skip to next track and show what's playing
cliamp next && cliamp status --json | jq .track.title

# Pause from a tmux/cmux script
cliamp pause

# Load a playlist and start playing
cliamp load "Blade Runner" && cliamp play
```

## Headless Daemon Mode

Run cliamp without a TUI and drive it entirely over this IPC interface — useful for status bars, hotkey scripts, cron jobs, and embedded boxes. See [Headless Daemon Mode](headless.md) for setup, use cases, and example configs (Waybar, Hyprland, systemd, cron).

```sh
cliamp --daemon                              # no TUI, IPC only
cliamp --daemon --auto-play --playlist Lofi  # start playing on launch
```

## Error Handling

If cliamp is not running:

```
$ cliamp status
cliamp is not running (no socket at /Users/you/.config/cliamp/cliamp.sock)
```
