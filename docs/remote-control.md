# Remote Control (IPC)

Control a running cliamp instance from another terminal, a shell script, or an AI coding assistant.

When cliamp starts, it listens on a Unix domain socket at `~/.config/cliamp/cliamp.sock`. CLI subcommands connect to this socket to send playback commands and receive status.

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
  "total": 59
}
```

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

## Protocol

The IPC protocol is newline-delimited JSON over a Unix domain socket. Each request is a single JSON object followed by a newline. The server responds with a single JSON object followed by a newline.

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

- **Path**: `~/.config/cliamp/cliamp.sock` (created on TUI start, removed on shutdown)
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

## Error Handling

If cliamp is not running:

```
$ cliamp status
cliamp is not running (no socket at /Users/you/.config/cliamp/cliamp.sock)
```
