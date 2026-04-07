# Media Controls

Cliamp integrates with the operating system's media control infrastructure so that desktop environments, hardware media keys, and command line tools can control playback, read track metadata, and adjust volume without touching the TUI.

## Platform Support

| Platform | Backend | Requirements |
|---|---|---|
| Linux | [MPRIS2](https://specifications.freedesktop.org/mpris-spec/latest/) over D-Bus | A running D-Bus session bus (provided by most desktop environments and Wayland compositors) |
| macOS | MPNowPlayingInfoCenter / MPRemoteCommandCenter | None (frameworks are built-in) |
| Other | No-op stub | — |

## Linux (MPRIS2)

### Bus Name

Cliamp registers itself as:

```
org.mpris.MediaPlayer2.cliamp
```

Only one instance can hold this name at a time. If a second Cliamp process tries to start, the MPRIS registration will fail silently and that instance will run without D-Bus integration.

### Playback Control

All standard transport commands are supported through the `org.mpris.MediaPlayer2.Player` interface:

| playerctl command | Effect |
|---|---|
| `playerctl play-pause` | Toggle play / pause |
| `playerctl play` | Resume playback |
| `playerctl pause` | Pause playback |
| `playerctl stop` | Stop playback |
| `playerctl next` | Skip to the next track |
| `playerctl previous` | Go to the previous track (or restart if more than 3 seconds in) |

### Seeking

Relative and absolute seeking are both supported:

```sh
playerctl position 30          # seek to 30 seconds
playerctl position 5+          # seek forward 5 seconds
playerctl position 5-          # seek backward 5 seconds
```

Desktop widgets that display a progress bar will receive `Seeked` signals and stay in sync.

### Volume

Volume is exposed as a linear value between 0.0 and 1.0. Internally Cliamp uses a decibel scale (from -30 dB to +6 dB), and the conversion happens automatically.

```sh
playerctl volume               # print current volume (0.0 to 1.0)
playerctl volume 0.5           # set volume to 50%
```

Setting volume through `playerctl` updates the player immediately. Changing volume with the `+` and `-` keys in the TUI is reflected back to D-Bus clients on the next tick.

### Metadata

Track metadata is published under the standard MPRIS keys:

| Key | Description |
|---|---|
| `mpris:trackid` | D-Bus object path identifying the current track |
| `xesam:title` | Track title |
| `xesam:artist` | Artist name (as a list with one entry) |
| `xesam:album` | Album name, when available |
| `xesam:url` | File path or stream URL |
| `mpris:length` | Duration in microseconds |

Query metadata with:

```sh
playerctl metadata              # all keys
playerctl metadata artist       # just the artist
playerctl metadata title        # just the title
```

For live radio streams that provide ICY metadata, the artist and title fields update dynamically as the station reports new track information.

### Status

```sh
playerctl status                # prints Playing, Paused, or Stopped
```

## macOS

On macOS, Cliamp publishes now-playing information to the system's MPNowPlayingInfoCenter. This enables:

- Control Centre and Lock Screen media controls
- Touch Bar playback buttons
- Hardware media keys (play/pause, next, previous)
- Bluetooth headphone buttons

The macOS implementation requires the media-control runtime to pin the main goroutine to thread 0 (via `runtime.LockOSThread`) so that the Cocoa run loop can pump events. Bubbletea runs on a background goroutine instead.

## Architecture

The app-owned playback command and notifier boundary lives in `internal/playback`. The `mediactl` package translates platform APIs to and from that boundary and owns the platform-specific interactive runtime helper.

Platform-specific `Service` implementations:

- `internal/playback/*` — app-level playback commands and outbound notifier state.
- `mediactl/service_linux.go` — connects to the session bus, claims the MPRIS bus name, translates D-Bus calls into playback commands, and publishes outbound state through MPRIS properties.
- `mediactl/service_darwin.go` — initialises NSApplication as an accessory process, registers MPRemoteCommandCenter handlers, translates them into playback commands, and publishes now-playing state on the main-thread run loop.
- `mediactl/service_stub.go` — no-op implementation for unsupported platforms.

The model publishes playback state through the playback notifier whenever state changes. On Linux, `mediactl` uses `SetMust` rather than `Set` to bypass the property library's writable checks and callback triggers, which are intended for external D-Bus writes. For writable properties like Volume, the D-Bus callback is translated into an app playback command and dispatched back into the Bubbletea event loop.

## Limitations

Shuffle and loop status are not exposed. The `z` and `r` keys in the TUI control shuffle and repeat locally, but these states are not visible to or controllable from external tools.

The `HasTrackList` property is set to false on Linux. Cliamp does not implement the optional `org.mpris.MediaPlayer2.TrackList` interface.
