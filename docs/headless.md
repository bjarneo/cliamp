# Headless Daemon Mode

Run cliamp without a TUI. Headless mode runs the same player as the TUI, but
it does not render a terminal UI. It listens on the same Unix socket as the
interactive player. Playback, library, and V2 remote commands work. Use this
mode to control playback through IPC from a status bar, script, hotkey daemon,
or cron job.

```sh
cliamp --daemon                              # no TUI, IPC only
cliamp -d                                    # short form
cliamp --daemon --auto-play --playlist Lofi  # start playing on launch
cliamp --daemon ~/Music --auto-play          # auto-play a directory
```

To stop the daemon, press `Ctrl+C` or send `SIGINT`, `SIGTERM` or `SIGHUP`. The shell sends `SIGHUP` to a background daemon when its terminal closes. cliamp saves the resume position, as the `q` key does in the TUI. A second signal stops a daemon that does not exit.

## What works

The daemon exposes the same runtime, library, job, and event IPC interface as the TUI. See [Remote Control](remote-control.md) for the list:

- Playback: `play`, `pause`, `toggle`, `stop`, `next`, `prev`
- Position: `seek`, `volume`, `speed`
- Playback modes: `shuffle`, `repeat`, `mono`
- Library: `load "Name"`, `queue /path/to.mp3`
- Audio: `eq <preset>`, `eq --band N <dB>`, `device <name|list>`
- Status: `status`, `status --json`
- Plugins: `plugins call`, and the hooks of the plugins in `~/.config/cliamp/plugins/`

## What doesn't

These UI-only commands return an error in headless mode:

- `cliamp theme <name>`: no UI is available for themes
- `cliamp vis <name|next>`: headless mode has no visualizer to select

The `cliamp theme list` and `cliamp vis list` commands still work.

The daemon enables MPRIS on Linux, NowPlaying on macOS, and hardware media key hotkeys on Windows when the platform service is available. You can also bind media keys directly to `cliamp` subcommands. See [Hotkeys](#hotkeys-window-manager--sxhkd--hyprland).

## Same behavior as the TUI

Headless mode runs the same player as the TUI. These features work the same
way in both modes:

- Lua plugins load from `~/.config/cliamp/plugins/`. Their hooks see playback events.
- Navidrome, Jellyfin, Emby, Audiobookshelf, and Yandex Music get now-playing and scrobble reports. Plex gets none.
- A track enters Recently Played when it starts. See [Recently Played](history.md).
- The IPC `save` operation, for example `cliamp remote call save --wait`, writes to the `[downloads]` directory. See [configuration.md](configuration.md#download-directory).
- The next track preloads, so playback is gapless.
- cliamp saves shuffle, repeat, speed, EQ, and output device changes to `config.toml`. A device switch saves `audio_device`.

The view settings in `config.toml`, such as `visualizer`, `simplified`, and
`expanded`, do not apply. `spectrum.get` and `cliamp visstream` always use the
default `Bars` analysis.

## Use cases

### Background music daemon

Start cliamp once at login, for example with `~/.config/systemd/user/cliamp.service` or desktop-environment autostart. Keep it running. Control it from any terminal:

```sh
cliamp toggle      # play/pause from anywhere
cliamp next
cliamp volume -3   # set the volume to -3 dB
```

Use this minimal systemd user unit:

```ini
[Unit]
Description=cliamp headless music player

[Service]
ExecStart=%h/.local/bin/cliamp --daemon --auto-play --playlist "Lofi"
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user enable --now cliamp.service
```

Headless mode never offers to install yt-dlp. If you configured YouTube, install yt-dlp before you start the service.

### Waybar / Polybar / i3blocks status modules

Poll `cliamp status --json` at an interval. Render the fields that you need.

**Waybar** (`~/.config/waybar/config`):

```jsonc
"custom/cliamp": {
  "exec": "cliamp status --json | jq -r 'if .state == \"playing\" then \"  \" + (.track.title // \"\") else \"\" end'",
  "interval": 2,
  "on-click": "cliamp toggle",
  "on-click-right": "cliamp next",
  "on-scroll-up": "cliamp remote call volume.adjust --params '{\"value\":3}'",
  "on-scroll-down": "cliamp remote call volume.adjust --params '{\"value\":-3}'"
}
```

The scroll actions submit `volume.adjust`, which changes the volume by the given number of dB. Do not use `cliamp volume +3` for a step. It sets the volume to +3 dB.

**Polybar**:

```ini
[module/cliamp]
type = custom/script
exec = cliamp status --json | jq -r '.track.title // ""'
interval = 2
click-left = cliamp toggle
click-right = cliamp next
```

#### Radio stream metadata

A radio station playlist entry only has the station name. During playback,
`.track` reports the station now-playing metadata. The station sends this data
inline as SHOUTcast/Icecast ICY metadata:

| Field | Description |
|-------|-------------|
| `title` | Current song from the ICY tag. A tag without the ` - ` separator is the whole title. Before a tag arrives, and while the tag has an empty artist or title part, this is the station name. |
| `artist` | Current artist when the ICY tag uses `"Artist - Title"` and both parts are set. cliamp trims both parts. |
| `station` | Station name. Present only after a song replaces `title`. |
| `stream_title` | Raw, unsplit ICY value |

A status bar can show the station and song together:

```sh
cliamp status --json | jq -r '(.track // {}) | if .station then (if .artist then "\(.station): \(.artist) - \(.title)" else "\(.station): \(.title)" end) else (.title // "") end'
```

### Hotkeys (window manager / sxhkd / Hyprland)

Bind media keys directly to IPC subcommands.

**Hyprland** (`~/.config/hypr/hyprland.conf`):

```ini
bind = , XF86AudioPlay,  exec, cliamp toggle
bind = , XF86AudioNext,  exec, cliamp next
bind = , XF86AudioPrev,  exec, cliamp prev
bind = , XF86AudioRaiseVolume, exec, cliamp remote call volume.adjust --params '{"value":3}'
bind = , XF86AudioLowerVolume, exec, cliamp remote call volume.adjust --params '{"value":-3}'
```

**sxhkd**:

```
XF86AudioPlay
    cliamp toggle

XF86AudioNext
    cliamp next
```

### Sleep / wake timers via cron

```cron
# Start lofi playback at 8am on weekdays
0 8 * * 1-5  /home/me/.local/bin/cliamp --daemon --auto-play --playlist Lofi >/dev/null 2>&1 &

# Stop at 6pm
0 18 * * *   pkill -TERM -f 'cliamp --daemon'
```

### Scripted playlists

Build a queue from a script, then start it with `cliamp play`. `--auto-play`
starts only a queue that holds tracks at startup, so it does not help here.

```sh
cliamp --daemon &
sleep 1                                  # let the socket bind
for f in $(find ~/Music/Albums/Daft\ Punk -name '*.flac' | sort); do
  cliamp queue "$f"
done
cliamp play                              # start the first track
```

### Remote control over SSH

The socket is at `~/.config/cliamp/cliamp.sock`, and the CLI accesses it locally. Get a shell on the host with SSH or tmux session attach to control playback:

```sh
ssh kitchen-pi cliamp toggle
ssh kitchen-pi cliamp status --json
```

### Embedded / kiosk audio

Run this mode on a Pi or small Linux computer without a display. The daemon needs no terminal allocation. It needs working ALSA, PipeWire, or PulseAudio output.

```sh
cliamp --daemon --auto-play http://radio.cliamp.stream/lofi/stream
```

## Notes

- The daemon and TUI share one Unix socket. Only one cliamp instance can run for a user. A second daemon exits with the error `cliamp is already running`. It exits before it opens the audio device or loads the plugins, so it does not change the running instance. A second TUI runs without the socket.
- cliamp resolves feed, M3U, PLS, and yt-dlp arguments in the background after start. If one of these URLs fails, cliamp adds none of them. The daemon keeps running with the local files and the direct stream URLs. Check `cliamp status`, and look in `~/.config/cliamp/cliamp.log` for the error.
