# Detached Mode

Run cliamp without a terminal of its own. `--daemon` starts the whole player --
the same playback, providers, plugins, keybindings, and visualizer as the
interactive TUI -- and renders it into a virtual terminal instead of yours.
Playback survives every terminal, IPC serves scripts and status bars, and
`cliamp attach` lends a real terminal to the running player whenever you want
to browse or search.

```sh
cliamp --daemon                              # detached, IPC only until you attach
cliamp -d                                    # short form
cliamp --daemon --auto-play --playlist Lofi  # start playing on launch
cliamp --daemon ~/Music --auto-play          # auto-play a directory

cliamp attach                                # borrow this terminal to the session
```

Press `q` in an attached terminal to hand it back: the session keeps playing.
`ctrl+\` also detaches, without asking the player, so it works even if the
session stops responding.

No key ends a session. Its lifetime belongs to whatever started it -- a service
manager, an autostart entry, a shell job -- so a keystroke in a borrowed
terminal must not take it down. Stop it explicitly:

```sh
cliamp quit                      # from any terminal
systemctl --user stop cliamp     # or through the service manager
kill -TERM $(pgrep -f 'cliamp --daemon')
```

All three are the same graceful shutdown: cliamp flushes pending settings and
saves the resume position. `SIGINT` and `SIGHUP` do the same; the shell sends
`SIGHUP` to a background session when its terminal closes, so start one with
`setsid` or a service manager if it must outlive that terminal. A second signal
stops a session that does not exit.

## What works

Everything the interactive player does, because it is the interactive player.
Detached, that includes playback, gapless preload, the playlist and provider
browsers, saved playlists, Lua plugins, MPRIS on Linux, NowPlaying on macOS,
hardware media keys on Windows, and the full runtime, library, job, and event
IPC interface. See [Remote Control](remote-control.md) for the command list:

- Playback: `play`, `pause`, `toggle`, `stop`, `next`, `prev`
- Lifetime: `quit`
- Position: `seek`, `volume`, `speed`
- Playback modes: `shuffle`, `repeat`, `mono`
- Library: `load "Name"`, `queue /path/to.mp3`
- Audio: `eq <preset>`, `eq --band N <dB>`, `device <name|list>`
- Appearance: `theme <name>`, `vis <mode>`
- Status: `status`, `status --json`, `vis --stream`
- Plugins: `plugins call`, and the hooks of the plugins in `~/.config/cliamp/plugins/`

`cliamp vis --stream` analyzes the spectrum on demand while detached, so a
status bar gets live bands without a terminal attached.

You can also bind media keys directly to `cliamp` subcommands. See
[Hotkeys](#hotkeys-window-manager--sxhkd--hyprland).

## Attaching

`cliamp attach` needs a terminal on stdin and stdout, and takes the size from
it -- resize the window and the session follows.

- **One terminal at a time.** One player has one renderer, so attaching from a
  second terminal takes the session over and the first is told why.
- **Color depth is fixed for the session.** A session outlives its clients, so
  it renders truecolor and each client downsamples for its own terminal.
- **The quit key detaches.** In a session `q` hands the terminal back rather
  than stopping the music, and the keymap says so. Everywhere `q` already meant
  something else -- queueing a track in the provider browser, typing in a
  search field -- it still does.
- **Your terminal comes back as you left it.** Attaching and detaching go
  through the player's own terminal setup and teardown, so the alternate
  screen, cursor, window title, and the colors a theme sets are restored on
  detach exactly as they are when cliamp exits. The client repeats the
  restore on its way out, so a session that dies without a teardown does not
  leave your terminal in a strange state either.
- **Attach only works against `--daemon`.** A cliamp that owns a real terminal
  has no virtual one to lend and reports that.

Over SSH, attach the session running on the other host:

```sh
ssh -t kitchen-pi cliamp attach
```

## Same behavior as the TUI

A session is the same player as the TUI, so these work the same way whether
or not a terminal is attached:

- Lua plugins load from `~/.config/cliamp/plugins/`. Their hooks see playback events.
- Navidrome, Jellyfin, Emby, Audiobookshelf, and Yandex Music get now-playing and scrobble reports. Plex gets none.
- A track enters Recently Played when it starts. See [Recently Played](history.md).
- The IPC `save` operation, for example `cliamp remote call save --wait`, writes to the `[downloads]` directory. See [configuration.md](configuration.md#download-directory).
- The next track preloads, so playback is gapless.
- cliamp saves shuffle, repeat, speed, EQ, and output device changes to `config.toml`. A device switch saves `audio_device`.

## Use cases

### Background music session

Start cliamp once at login, for example with `~/.config/systemd/user/cliamp.service` or desktop-environment autostart. Keep it running. Control it from any terminal, or attach to it:

```sh
cliamp toggle      # play/pause from anywhere
cliamp next
cliamp volume -3
cliamp attach      # full UI in this terminal, q to leave it playing
cliamp quit        # stop the session for good
```

Use this minimal systemd user unit:

```ini
[Unit]
Description=cliamp music player session

[Service]
ExecStart=%h/.local/bin/cliamp --daemon --auto-play --playlist "Lofi"
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user enable --now cliamp.service
```

`Restart=on-failure` and not `Restart=always`: a graceful stop the session asks
for itself -- `cliamp quit`, an MPRIS client's Quit, a SIGTERM you send by hand
-- exits 0, and `always` reads that as a reason to start it again. The music
would come back moments after you asked it to stop, leaving `systemctl --user
stop` (which systemd does not restart after) as the only way to end it.
`on-failure` restarts the session when it actually crashed, which is what you
wanted the line for.

A detached session never offers to install yt-dlp. If you configured YouTube, install yt-dlp before you start the service.

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

The socket is at `~/.config/cliamp/cliamp.sock`, and the CLI accesses it locally. Get a shell on the host with SSH to control playback, or attach to its UI:

```sh
ssh kitchen-pi cliamp toggle
ssh kitchen-pi cliamp status --json
ssh -t kitchen-pi cliamp attach
```

### Embedded / kiosk audio

Run this mode on a Pi or small Linux computer without a display. A detached session needs no terminal allocation. It needs working ALSA, PipeWire, or PulseAudio output.

```sh
cliamp --daemon --auto-play http://radio.cliamp.stream/lofi/stream
```

## Notes

- One cliamp instance runs per user: it owns the Unix socket. A second
  `--daemon` exits with the error `cliamp is already running` before it opens
  the audio device or loads the plugins, so it does not change the running
  instance. Attach to the running session instead. A second TUI runs without
  the socket.
- A detached session renders no frame and runs no visualizer until a client
  attaches. It ticks only as often as playback bookkeeping needs, so an
  idle-but-playing session costs about what the old headless mode did.
- cliamp resolves feed, M3U, PLS, and yt-dlp arguments in the background after start. If one of these URLs fails, cliamp adds none of them. The session keeps running with the local files and the direct stream URLs. Check `cliamp status`, and look in `~/.config/cliamp/cliamp.log` for the error.
