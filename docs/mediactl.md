# Media Controls

cliamp integrates with operating system media controls. Desktop environments,
hardware media keys, and command line tools can control playback, read track
metadata, and adjust volume without using the TUI.

## Platform Support

| Platform | Backend | Requirements |
|---|---|---|
| Linux | [MPRIS2](https://specifications.freedesktop.org/mpris-spec/latest/) over D-Bus | Running D-Bus session bus (provided by most desktop environments and Wayland compositors) |
| macOS | MPNowPlayingInfoCenter / MPRemoteCommandCenter | None (built-in frameworks) |
| Windows | Global hardware media key hotkeys | None (built-in Win32 API) |
| Other | No-op stub | None |

## Linux (MPRIS2)

### Bus Name

cliamp registers this name:

```
org.mpris.MediaPlayer2.cliamp
```

Only one instance can hold this name. If a second cliamp process starts, its
MPRIS registration fails without an error message. That instance runs without
D-Bus integration.

### Playback Control

The `org.mpris.MediaPlayer2.Player` interface supports all standard transport commands:

| playerctl command | Effect |
|---|---|
| `playerctl play-pause` | Toggle play / pause |
| `playerctl play` | Resume playback |
| `playerctl pause` | Pause playback |
| `playerctl stop` | Stop playback |
| `playerctl next` | Skip to the next track |
| `playerctl previous` | Go to the previous track (or restart if more than 3 seconds in) |

### Seeking

You can seek by an absolute or relative value:

```sh
playerctl position 30          # seek to 30 seconds
playerctl position 5+          # seek forward 5 seconds
playerctl position 5-          # seek backward 5 seconds
```

Desktop widgets with a progress bar receive `Seeked` signals and stay in sync.

### Volume

Volume is available as a linear value from 0.0 to 1.0. Internally, cliamp uses
a decibel scale from -30 dB to +6 dB. It converts the values automatically.

```sh
playerctl volume               # print current volume (0.0 to 1.0)
playerctl volume 0.5           # set volume to 50%
```

Setting volume through `playerctl` updates the player immediately. When you
change volume with the TUI `+` and `-` keys, D-Bus clients receive the new value
on the next tick.

### Metadata

cliamp publishes track metadata with standard MPRIS keys:

| Key | Description |
|---|---|
| `mpris:trackid` | D-Bus object path for the current track |
| `xesam:title` | Track title |
| `xesam:artist` | Artist name as a list with one entry |
| `xesam:album` | Album name, when available |
| `xesam:url` | File path or stream URL |
| `mpris:artUrl` | Embedded album art from local files, when available |
| `mpris:length` | Duration in microseconds |

Query metadata:

```sh
playerctl metadata              # all keys
playerctl metadata artist       # just the artist
playerctl metadata title        # just the title
```

For live radio streams with ICY metadata, artist and title update when the
station sends new track information.

### Status

```sh
playerctl status                # prints Playing, Paused, or Stopped
```

### Hyprland bindings

Hyprland does not bind `XF86Audio*` keys by default. Add the following to the
Hyprland config, usually `~/.config/hypr/bindings.conf` or `hyprland.conf`, to
bind hardware media keys to cliamp through `playerctl`:

```conf
bindl = , XF86AudioPlay,  exec, playerctl --player=cliamp play-pause
bindl = , XF86AudioPause, exec, playerctl --player=cliamp play-pause
bindl = , XF86AudioNext,  exec, playerctl --player=cliamp next
bindl = , XF86AudioPrev,  exec, playerctl --player=cliamp previous
```

Notes:

- `bindl` runs even when the session is locked. Keys continue to work under `hyprlock`.
- `--player=cliamp` limits the command to cliamp. Remove the flag to control the most recently active MPRIS player. Use this when cliamp shares the session with browsers or Spotify.
- Run `hyprctl reload` after you edit the configuration.
- Install `playerctl`, for example with `pacman -S playerctl` or `apt install playerctl`.

## macOS

On macOS, cliamp publishes now-playing information to the system MPNowPlayingInfoCenter. This enables:

- Control Centre and Lock Screen media controls
- Touch Bar playback buttons
- Hardware media keys (play/pause, next, previous)
- Bluetooth headphone buttons

Local files with embedded cover art publish that art to Control Centre and Lock
Screen media controls. cliamp caches art by content in
`~/.local/share/cliamp/album-art/`. It removes cached art as needed to keep the
cache near 100 MB.

The macOS media-control runtime pins the main goroutine to thread 0 with
`runtime.LockOSThread`. This lets the Cocoa run loop process events. Bubbletea
runs on a background goroutine.

## Windows

On Windows, cliamp registers the hardware media keys (Play/Pause, Next,
Previous, Stop) as global hotkeys via the Win32 `RegisterHotKey` API. This
works regardless of which window has focus, without needing a visible window
of its own — `RegisterHotKey` ties the hotkeys to a message queue owned by a
dedicated OS thread rather than to an `HWND`.

cliamp does not publish now-playing metadata to the Windows shell (no
System Media Transport Controls integration), so there is no track info in
the volume flyout or lock screen on Windows. Only the transport keys work.

A hotkey registration can fail if another running application already holds
the same key; that key then does not control cliamp. Registration failures
are silent and do not affect the other keys or the rest of the app.

## Architecture

The application playback command and notifier boundary is in
`internal/playback`. The `mediactl` package translates platform APIs to and from
this boundary. It owns the platform-specific interactive runtime helper.

Platform-specific `Service` implementations:

- `internal/playback/*`: application playback commands and outbound notifier state.
- `mediactl/service_linux.go`: connects to the session bus, claims the MPRIS bus name, translates D-Bus calls to playback commands, and publishes state through MPRIS properties.
- `mediactl/service_darwin.go`: starts NSApplication as an accessory process, registers MPRemoteCommandCenter handlers, translates them to playback commands, and publishes now-playing state in the main-thread run loop.
- `mediactl/service_windows.go`: registers the media keys as global hotkeys via `user32.dll` and pumps a Win32 message loop on a dedicated OS thread, translating `WM_HOTKEY` to playback commands.
- `mediactl/service_stub.go`: no-op implementation for unsupported platforms.

The model sends playback state through the playback notifier when state changes.
On Linux, `mediactl` uses `SetMust`, not `Set`, to bypass property-library
writable checks and callback triggers. These checks and triggers apply to
external D-Bus writes. For writable properties such as Volume, `mediactl`
translates the D-Bus callback to an application playback command. It sends this
command to the Bubbletea event loop.

## Limitations

cliamp does not expose shuffle and loop status. The TUI `z` and `r` keys control
shuffle and repeat locally. External tools cannot view or control these states.

On Linux, `HasTrackList` is false. cliamp does not implement the optional
`org.mpris.MediaPlayer2.TrackList` interface.
