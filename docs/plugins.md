# Lua Plugins

cliamp uses Lua 5.1 plugins. Plugins can handle playback events, such as scrobbling, notifications, and status-bar output. They can also add visualizers. Each plugin runs in an isolated VM. A plugin crash does not affect other plugins or the player.

Store plugins in `~/.config/cliamp/plugins/`. Create the directory:

```
mkdir -p ~/.config/cliamp/plugins
```

cliamp runs a plugin only after you approve its exact contents. Existing and manually copied plugins start as untrusted. Approve one with `cliamp plugins trust <name>`.

## Plugin manager

```sh
cliamp plugins                          # show help
cliamp plugins list                     # list installed plugins
cliamp plugins install <source>         # install a plugin
cliamp plugins trust <name>             # approve installed plugin contents
cliamp plugins remove <name>            # remove a plugin and its approval
```

The install and trust commands show the source, SHA-256, declared permissions, and implicit file-system and network access before the prompt. In a non-interactive environment, use `--yes` only after you review the same content independently. cliamp stores approvals in `plugins/.trust.json`. Editing a plugin changes its hash and disables it until you approve it again. Removing a plugin also removes its approval.

cliamp approves only the content and the permissions that the prompt showed. If the file changes while the prompt waits, `cliamp plugins trust` approves nothing and fails. If the `plugin.register()` call of a plugin declares another permission at runtime, the plugin does not load. An approval from an older cliamp has no recorded permissions. For such an approval, cliamp approves the permissions that the check below finds in the file.

If `plugins/.trust.json` does not parse, cliamp treats every plugin as untrusted and logs the error to `plugins.log`. `cliamp plugins list` then shows each plugin as untrusted and prints the error. `cliamp plugins install` stops before it downloads the plugin, `cliamp plugins trust` stops before it asks, and `cliamp plugins remove` stops before it deletes the plugin. To recover, delete the file and approve each plugin again. Then run the `install` or `remove` command again.

The install and trust commands check the `plugin.register()` call the same way the player does. They reject a plugin whose `plugin.register()` call the player rejects, such as a call with an unknown permission name, or without `type = "hook"` or `type = "visualizer"`. The check runs the plugin file with a stand-in `cliamp` table that does nothing, and `print` does nothing too. Thus the plugin cannot do any work or write to the terminal before you approve it. `cliamp plugins list` drops control characters, such as escape sequences, from the name, version, and description of each plugin.

### Install sources

| Format | Example |
|--------|---------|
| GitHub | `user/repo` |
| GitHub with tag | `user/repo@v1.0` |
| GitLab | `gitlab:user/repo` |
| GitLab with tag | `gitlab:user/repo@v1.0` |
| Codeberg | `codeberg:user/repo` |
| Codeberg with tag | `codeberg:user/repo@v1.0` |
| Direct URL | `https://example.com/plugin.lua` |

### Naming convention

Name plugin repositories `cliamp-plugin-<name>`. Put the `<name>.lua` entry point in the repository root. cliamp removes the `cliamp-plugin-` prefix during installation. For example, `cliamp-plugin-soap-bubbles`, which contains `soap-bubbles.lua`, installs as `soap-bubbles`.

```sh
cliamp plugins install bjarneo/cliamp-plugin-lastfm
cliamp plugins install bjarneo/cliamp-plugin-lastfm@v1.0
cliamp plugins install gitlab:user/my-visualizer
cliamp plugins install codeberg:user/my-plugin
cliamp plugins install https://example.com/my-plugin.lua
cliamp plugins remove lastfm
```

## Quick start

### Now-playing file for Waybar, Polybar, and similar bars

```lua
-- ~/.config/cliamp/plugins/now-playing.lua
local p = plugin.register({
    name = "now-playing",
    type = "hook",
    description = "Write now-playing to /tmp for status bars",
})

p:on("track.change", function(track)
    cliamp.fs.write("/tmp/cliamp-now-playing", track.artist .. " - " .. track.title)
end)

p:on("playback.state", function(ev)
    if ev.status == "paused" then
        cliamp.fs.write("/tmp/cliamp-now-playing", "paused")
    end
end)

p:on("app.quit", function()
    cliamp.fs.remove("/tmp/cliamp-now-playing")
end)
```

### Desktop notification on track change

```lua
-- ~/.config/cliamp/plugins/notify.lua
local p = plugin.register({
    name = "notify",
    type = "hook",
})

p:on("track.change", function(track)
    local title = track.artist .. " - " .. track.title
    os.execute('notify-send "cliamp" "' .. title .. '"')
end)
```

`os.execute` is not available in the sandbox. Use `cliamp.http` for public HTTP endpoints. cliamp blocks private, loopback, link-local, multicast, and unspecified addresses. For local automation, write to an allowed file for a watcher to read. You can also declare the permission-gated `exec` capability.

### Webhook

```lua
-- ~/.config/cliamp/plugins/webhook.lua
local p = plugin.register({
    name = "webhook",
    type = "hook",
})

local url = p:config("url")

p:on("track.change", function(track)
    if not url then return end
    cliamp.http.post(url, {
        json = { title = track.title, artist = track.artist, album = track.album }
    })
end)
```

```toml
# config.toml
[plugins.webhook]
url = "https://example.com/hook"
```

## Plugin structure

### Single file

```
~/.config/cliamp/plugins/myplugin.lua
```

### Directory with init.lua

```
~/.config/cliamp/plugins/myplugin/
    init.lua
    helpers.lua
```

The directory name is the plugin name. cliamp loads only `init.lua` automatically. If `myplugin.lua` and `myplugin/init.lua` both exist, cliamp and `cliamp plugins` use `myplugin.lua`.

## Registration

Each plugin must call `plugin.register()`. cliamp skips files that do not call it. The `type` field is required. A `plugin.register()` call without `type = "hook"` or `type = "visualizer"` is a load error. cliamp shows the error at startup and does not load the plugin. Call `plugin.register()` once. A second call raises an error, so it cannot change the permissions after you approve the plugin. The top-level code of the plugin file must finish in 5 seconds. A plugin that runs longer at load is a load error. The `cliamp.player` controls and the `cliamp.queue` edits do nothing in the top-level code, because the player is not ready yet. Call them from an event handler, such as `app.start`.

```lua
local p = plugin.register({
    name        = "myplugin",           -- unique; default: the installed name
    type        = "hook",               -- required: "hook" or "visualizer"
    version     = "1.0.0",             -- optional
    description = "What it does",       -- optional
})
```

Each loaded plugin must have its own `name`. cliamp loads plugins in the order of their installed names. If a plugin registers a name that an earlier plugin already uses, cliamp reports a load error and does not load it. If you omit `name`, cliamp uses the installed name: the file name without `.lua`, or the directory name. Commands, key binding descriptions, and visualizer modes use `name`. Config, trust, `cliamp.store`, event topics, and `plugins.log` use the installed name.

The returned `p` object provides these methods:

| Method | Description |
|--------|-------------|
| `p:on(event, callback)` | Subscribe to a playback event |
| `p:config(key)` | Read a config value from `[plugins.myplugin]` in config.toml |
| `p:publish(topic, payload, options)` | Publish a namespaced event to local IPC subscribers |

## Plugin event pub/sub

Plugins can publish JSON-compatible values to external programs that connect to
Cliamp's owner-only IPC socket. cliamp puts each topic below the installed plugin
name. For example, `myplugin.lua` that publishes `"playback"` uses the topic
`plugin.myplugin.playback`. The name provides one topic segment. cliamp replaces
each character other than a letter, digit, `_`, or `-` with `_`. Therefore,
`my.plugin.lua` uses the `plugin.my_plugin.*` prefix. Publishing `"playback"`
from that plugin uses `plugin.my_plugin.playback`. It cannot collide with topics
from a plugin named `my`.

This conversion loses information, so namespaces are unique for a session. If
`my.plugin.lua` and `my_plugin.lua` are both installed, the first loaded plugin
(plugins load in name order) owns the `plugin.my_plugin.*` prefix. In the other
plugin, `p:publish()` returns `nil, err` and names the owner. Rename one plugin
to publish from both. Subscribers must specify complete topics, not prefixes.

```lua
p:publish("playback", {
    status = cliamp.player.state(),
    title = cliamp.track.title(),
}, { retain = true })
```

With `retain = true`, Cliamp keeps the latest value in memory and sends it to
new subscribers immediately. Cliamp discards retained values when it exits. It
does not save event data to disk. Publishing does not block. cliamp disconnects
a subscriber that cannot keep up instead of blocking the player.

Open `cliamp.sock` and send one NDJSON request to subscribe:

```json
{"version":2,"id":"events","method":"subscribe","topics":["plugin.myplugin.playback"]}
```

After `{"version":2,"id":"events","ok":true}`, the connection becomes a
server-to-client event stream:

```json
{"event":"plugin.myplugin.playback","seq":42,"time":1786685741,"retained":true,"data":{"status":"playing","title":"Track"}}
```

Subscriptions use exact topic matches and accept at most 32 topics. They are
for streaming only. Use another IPC connection for normal commands. Sending
more bytes on a subscription closes it. Payloads can be at most 64 KiB. cliamp
converts them with the nesting and cycle rules of
[`cliamp.json`](#cliampjson). Topic segments can contain letters, digits, `.`,
`_`, and `-`. `p:publish()` needs no permission. IPC is local to the same user,
and the `status` command already exposes playback metadata.

## Events

Use `p:on(event, callback)` to subscribe to events. Each plugin gets its events and key presses one at a time, in the order cliamp sent them. Different plugins run in parallel. Each callback times out after 5 seconds.

Up to 256 events and key presses can wait for one plugin. If a plugin falls further behind, cliamp drops its new events and key presses until it catches up, and logs one warning to `plugins.log`. At shutdown, cliamp first stops each command that still runs. Then it runs the events that wait for up to 2 seconds and drops the rest. Then it runs the `app.quit` handlers one at a time, with the same 5 second limit. After that, cliamp stops each timer callback and exec callback that still runs.

### Available events

| Event | Callback argument | When |
|-------|-------------------|------|
| `track.change` | `{title, artist, album, genre, year, path, duration, stream}` | New track starts successfully |
| `track.scrobble` | Same + `{played_secs}` | You left a track that played at least 50% of its known duration: it ended, or you skipped, stopped, removed it from the queue, rewound with previous, or started another track. Each start of a track fires this at most once |
| `playback.state` | `{status, title, artist, album, path, duration, stream, position}` | Once for each playback state change (play, pause, stop, seek, volume, track transition, new stream title), and once per second while a track plays. A message that changes nothing does not fire it |
| `player.seek` | `{position, duration}` (seconds) | A seek completes |
| `player.volume` | `{db}` | Volume changes |
| `player.eq` | `{bands, preset}` | An EQ band or preset changes |
| `player.mode` | `{shuffle, repeat}` | Shuffle toggled or repeat mode cycled |
| `queue.change` | `{count, index, queued}` | Playlist or play-next queue changes |
| `queue.end` | Same as `track.change`, for the finished track | Advancing past the last track stopped playback, whether the track ended or the user skipped. A manual stop does not fire this |
| `playback.stop` | `{}` | The user stopped playback with a key, IPC, or media controls. The queue running out does not fire this |
| `app.start` | `{}` | After all plugins loaded |
| `app.quit` | `{}` | Before shutdown |

In `playback.state`, `status` is `"playing"`, `"paused"`, or `"stopped"`. In `player.mode`, `repeat` is `"Off"`, `"All"`, or `"One"`, matching `cliamp.player.repeat_mode()`. In `player.eq`, `bands` is an array of 10 dB values.

In `track.change`, `track.scrobble`, `playback.state` and `queue.end`, `duration` is the length in whole seconds. A track with no length in its metadata, such as a local file, gets the decoded length. `duration` is 0 when the length is unknown, as for a live stream.

`track.change` fires after playback starts successfully for all sources, including YouTube and SoundCloud. A stream that is still buffering, fails to start, or is superseded before it starts does not emit this event. Gapless transitions also emit `track.change`.

`queue.end` reports the last track that emitted `track.change`. It fires when the track drains, at a gapless boundary with nothing queued, on next from the last track, and on next after the playlist was emptied while the track played, or replaced by a list with no playable track. A stream still buffering when the queue runs out is not reported, and a failed start emits nothing. After a failed start, `queue.end` does not fire until another track starts, even if the previous track is still playing.

cliamp sends `player.*` and `queue.change` events by comparing state after each UI update. They cover every source, including a keypress, IPC, MPRIS, or another plugin.

An event hook sees the state that includes the change that the event reports. For example, `cliamp.track.title()` in a `track.change` hook returns the title of the track that started.

## Plugin object methods

The object from `plugin.register(...)` provides these methods in addition to `:on()` and `:config()`:

### `p:bind(key, [description,] callback)` - keyboard binding (requires `permissions = {"keymap"}`)

```lua
local p = plugin.register({
    name = "my-plugin",
    type = "hook",
    permissions = {"keymap"},
})

-- Listed in the Ctrl+K overlay under "— plugins —":
p:bind("ctrl+n", "Extract chapters", function(key) ... end)

-- Not listed (hidden binding):
p:bind("ctrl+t", function(key) ... end)
```

Returns `true` on success. Returns `false, reason` when cliamp's core UI owns the key or the plugin lacks the `keymap` permission. Pass a description as the middle argument to show the binding in the `Ctrl+K` keymap overlay. Omit it for an internal-only binding.

Use Bubbletea's `msg.String()` form for key strings: lowercase letters and the `ctrl+`, `shift+`, or `alt+` prefixes. For example: `"n"`, `"ctrl+y"`, and `"shift+f1"`. Key strings are case-insensitive.

Plugin keys work only in the main view. Overlays such as the file browser, theme picker, and keymap capture their own input. The core reserves the keys of its own commands, in the main view and in each overlay. It also reserves the text-editor keys `Ctrl+A`, `Ctrl+E`, `Ctrl+W`, `Ctrl+U`, `Home`, `End`, `Delete`, and `Backspace`. The cursor aliases `Ctrl+N` and `Ctrl+P` stay free. A bind of a reserved key logs a warning to `plugins.log` and returns `false, reason`.

Use `p:unbind(key)` to release a binding.

### `p:command(name, callback)` - shell-invokable command

```lua
p:command("run", function(args)
    -- args is an array of strings passed after the command name
    return "done: " .. args[1]
end)
```

The callback can return a string. The CLI client prints it. Invoke commands from the shell with `cliamp plugins call <plugin-name> <command> [args...]`. cliamp sends the command to the running player over IPC. Commands need no separate permission because the user starts them.

List registered commands with `cliamp plugins commands`. A command can run for up to 5 minutes before it times out. cliamp also stops a command when its IPC job is canceled and when cliamp quits.

## Lua API

All APIs are in the global `cliamp` table.

### Errors and permission denials

Each function reports a failure in one fixed way:

- A function that returns a value returns `nil` and an error message. Examples are `cliamp.fs.read`, `cliamp.http.get`, `cliamp.json.decode`, `cliamp.store.set`, `cliamp.exec.run`, `cliamp.queue.add(track)`, and `p:publish`.
- `p:bind` returns `false` and a reason.
- The controls in `cliamp.player`, and `cliamp.queue.add(path)`, `jump`, `remove`, and `move`, return nothing.
- A bad argument raises a Lua error. `cliamp.fs.write`, `append`, `remove`, and `mkdir` also raise a Lua error for a path outside the allowed write directories. Use `pcall` to catch it. `cliamp.exec.run` returns `nil, "cwd not in write allowlist"` for such a `cwd`.

If a plugin calls a function that needs a permission it did not declare, the function does nothing and returns its usual failure result. cliamp logs one warning to `plugins.log` for each missing permission.

```lua
local ok, err = pcall(cliamp.fs.write, "/etc/motd", "text")
if not ok then cliamp.log.warn(err) end
```

### cliamp.player (read-only)

```lua
cliamp.player.state()         --> "playing" | "paused" | "stopped"
cliamp.player.position()      --> number (seconds)
cliamp.player.duration()      --> number (seconds)
cliamp.player.volume()        --> number (dB, volume_min to +6)
cliamp.player.speed()         --> number (ratio, 1.0 = normal)
cliamp.player.mono()          --> boolean
cliamp.player.repeat_mode()   --> "Off" | "All" | "One"
cliamp.player.shuffle()       --> boolean
cliamp.player.eq_bands()      --> table of 10 dB values
```

### cliamp.track (read-only)

```lua
cliamp.track.title()          --> string
cliamp.track.artist()         --> string
cliamp.track.album()          --> string
cliamp.track.genre()          --> string
cliamp.track.year()           --> number
cliamp.track.track_number()   --> number
cliamp.track.path()           --> string
cliamp.track.is_stream()      --> boolean
cliamp.track.is_live()        --> boolean (no track boundary: radio, or a stream that is live now)
cliamp.track.duration_secs()  --> number
```

`cliamp.track` reports the track that plays, as `playback.state` does. When you load a playlist during playback, the old track plays on, and `cliamp.track` reports it until the next track starts. For a radio stream, `title` and `artist` come from the stream title when the station sends one. When nothing plays, `cliamp.track` reports the current track of the queue.

cliamp updates the values of `cliamp.player`, `cliamp.track`, and `cliamp.queue` after it handles each key, message, or tick. `cliamp.player.position()` and `cliamp.player.duration()` read the audio engine directly.

### cliamp.queue

You can read the playlist without permission. To change it, declare `permissions = {"control"}`. All indices are 0-based, as in `cliamp.queue.current()`.

```lua
-- read (no permission)
cliamp.queue.list()        --> array of {title, artist, album, genre, year, path, duration, stream, index, queued}
cliamp.queue.count()       --> number of tracks
cliamp.queue.current()     --> 0-based index of the current track
cliamp.queue.has_next()    --> true when a playable track follows in play order (play-next queue, repeat, shuffle)

-- mutate (requires "control")
cliamp.queue.add(path)         -- resolve a file/dir/URL and append
cliamp.queue.add(track)        --> true | nil, err  -- append a track table as given
cliamp.queue.jump(index)       -- make index current and play it
cliamp.queue.remove(index)     -- remove the track at index
cliamp.queue.move(from, to)    -- swap the tracks at from and to
```

`add`, `remove`, and `move` follow the rules of the `Shift+Up`, `Shift+Down`,
and `x` keys and of IPC:

- While shuffle is on, `move` changes nothing.
- When the queue mirrors a saved local playlist, `move` saves the new order to
  that playlist. `remove` removes the track from it too. When that save fails,
  the edit changes nothing. Favorites is not a playlist file, so an edit of a
  loaded Favorites list changes only the queue.
- `add`, `move`, and `remove` record no undo. After one of them, `Ctrl+Z` does
  not undo the last TUI edit, because that undo would drop the new change.
- While the queue mirrors a saved local playlist, `remove` does not remove a
  track that a directory source of that playlist supplies. A removal of the
  playing track stops playback.
- `add` appends. The queue then mirrors no saved playlist.
- When an edit changes the next track, cliamp re-arms the gapless preload.

`add` accepts every input that the CLI accepts: a local file or directory, an
HTTP stream, an M3U/PLS URL, or a YouTube/yt-dlp URL. cliamp resolves it off the
UI thread. A slow URL does not block playback.

`add` also takes a track table with the same keys as the track tables in
events: `{path, title, artist, album, genre, year, duration, stream}`. Only
`path` is required, and other keys are ignored, so a track from an event or from
`queue.list()` can be passed back as it is. cliamp appends the track exactly as
described, without resolving the path. Use it for tracks a path alone can't
describe, such as a provider track from another service:

```lua
local ok, err = cliamp.queue.add({
    path = "spotify:track:69kOkLUCkxIZYexIgSG8rq",
    title = "Get Lucky", artist = "Daft Punk", duration = 369,
})
if not ok then cliamp.log.warn(err) end
```

It returns `true`, or `nil` and an error message when the table is invalid (for
example, a missing path or a title that is not a string) or when the plugin
lacks the `control` permission. An HTTP URL is always marked as a stream.

### cliamp.http

```lua
-- GET
local body, status = cliamp.http.get("https://api.example.com/data", {
    headers = { Authorization = "Bearer token" }
})

-- POST with JSON
local body, status = cliamp.http.post("https://api.example.com/scrobble", {
    json = { artist = "Radiohead", track = "Everything In Its Right Place" }
})

-- POST with form body
local body, status = cliamp.http.post(url, {
    headers = { ["Content-Type"] = "application/x-www-form-urlencoded" },
    body = "key=value&foo=bar"
})
```

The timeout is 5 seconds. A request also ends when the time limit of the running callback ends, such as the 50 ms limit of a visualizer `render`. The response body limit is 1 MB.

### cliamp.fs

```lua
cliamp.fs.write(path, content)    -- overwrite file
cliamp.fs.append(path, content)   -- append to file
cliamp.fs.read(path)              --> string (max 1 MB)
cliamp.fs.remove(path)            -- delete file
cliamp.fs.exists(path)            --> boolean
cliamp.fs.mkdir(path)             -- create directory (recursive)
cliamp.fs.listdir(path)           --> {names}, err
```

You can write only to the system temp directory (`/tmp/` on Unix), `~/.config/cliamp/`, `~/.local/share/cliamp/`, and `~/Music/cliamp/`. In `~/.config/cliamp/`, you cannot write to the `plugins/` directory, `config.toml`, `radios.toml`, `cliamp.sock`, `cliamp.sock.pid`, or `plugins.log`. You can read from any path. `cliamp.fs.read` reads only regular files. For a FIFO, a device, or a directory, it returns `nil` and an error, because a read of such a file can block without a limit. On Windows, when `HOME` is unset, the config directory resolves to `%APPDATA%\cliamp`.

### cliamp.json

```lua
local tbl = cliamp.json.decode('{"key": "value"}')
local str = cliamp.json.encode({ key = "value" })
```

cliamp encodes tables to a depth of 64 levels. A deeper table or a cyclic
reference becomes `null`; it does not fail. `p:publish()` and `cliamp.store`
use the same conversion.

### cliamp.store

This is a persistent key/value store for each plugin. Strings, numbers,
booleans, and tables survive restarts. No permission is required. cliamp keys
each store by the installed name, and `cliamp.store` reaches only the store of
the calling plugin. The store is not secret. Another plugin can read or change
the store file with `cliamp.fs`. Do not keep secrets in it.

```lua
cliamp.store.set(key, value)   -- value: string|number|boolean|table
cliamp.store.get(key)          --> value or nil
cliamp.store.delete(key)
cliamp.store.keys()            --> sorted array of keys
cliamp.store.clear()
```

`set` returns `nil` and an error for a value that JSON cannot hold, such as
`0/0` or `1/0`, also inside a table. The store keeps the old value of the key.

cliamp stores this data in `~/.local/share/cliamp/plugins/<name>/store.json`
with owner-only mode (0600). Use it for play counts, offline scrobble queues,
resume positions, and saved settings. Do not use it for large data.

```lua
local counts = cliamp.store.get("counts") or {}
counts[cliamp.track.path()] = (counts[cliamp.track.path()] or 0) + 1
cliamp.store.set("counts", counts)
```

### cliamp.crypto

```lua
cliamp.crypto.md5("hello")                  --> hex string
cliamp.crypto.sha256("hello")               --> hex string
cliamp.crypto.hmac_sha256("secret", "msg")  --> hex string
```

### cliamp.log

```lua
cliamp.log.info("loaded successfully")
cliamp.log.warn("missing config key")
cliamp.log.error("request failed: " .. err)
cliamp.log.debug("response: " .. body)
```

cliamp writes logs to `~/.config/cliamp/plugins.log`. Each line has a timestamp and the installed name of the plugin as the prefix, for example `[now-playing]`. `print(...)` also writes to this file, at the `info` level, with its arguments joined by tabs. It never writes to the terminal.

cliamp also logs the Lua errors of event hooks, key bindings, commands, timers, exec callbacks, and visualizer callbacks to this file. A callback that fails again with the same error logs it once. cliamp logs it again after the callback succeeds or fails with a different error. A visualizer `render` runs on each frame, so cliamp logs only its first error and its first timeout until cliamp restarts. The log entry for a timeout names the time limit. cliamp does not write plugin errors to the terminal.

### cliamp.player control (requires permissions)

Plugins that declare `permissions = {"control"}` can control the player:

```lua
local p = plugin.register({
    name = "my-controller",
    type = "hook",
    permissions = {"control"},
})

cliamp.player.next()              -- skip to next track
cliamp.player.prev()              -- go to previous track
cliamp.player.play_pause()        -- toggle play/pause
cliamp.player.stop()              -- stop playback
cliamp.player.set_volume(-5)      -- set volume in dB (volume_min to +6)
cliamp.player.set_speed(1.25)     -- set playback speed (0.25 to 2.0)
cliamp.player.seek(30)            -- seek forward 30 seconds (a negative value seeks back)
cliamp.player.toggle_mono()       -- toggle mono output
cliamp.player.set_eq_preset("Rock") -- switch to built-in preset (sets bands + UI label)
cliamp.player.set_eq_preset("Metal", {6,4,1,-1,-2,2,4,6,6,5}) -- custom preset with bands
cliamp.player.set_eq_band(1, 6)   -- set EQ band 1 to +6 dB (bands 1-10, -12 to +12)
```

If a plugin does not declare `permissions = {"control"}`, these functions log a warning and do nothing.

Each control returns at once. cliamp applies the controls in the order that the plugin calls them, on the same loop that handles the keys. Thus a read such as `cliamp.player.volume()` right after `set_volume` can still return the old value. cliamp saves a speed change to `config.toml` after one second, as the speed keys do. A volume change updates the volume of the media controls at once.

### cliamp.notify

```lua
cliamp.notify("Song Title")                -- notification with title only
cliamp.notify("Song Title", "Artist Name") -- notification with title and body
```

This sends a desktop notification through `notify-send`. It works with mako, dunst, and other notification daemons. cliamp stops `notify-send` after 2 seconds, or earlier when the time limit of the running callback ends.

### cliamp.exec (requires permissions)

Plugins that declare `permissions = {"exec"}` can start subprocesses from a configurable binary allowlist. The default allowlist is `yt-dlp`, `ffmpeg`. Add binaries in `config.toml`:

```toml
[plugins]
allowed_binaries = "ffprobe, curl"   # merged with defaults
```

```lua
local p = plugin.register({
    name = "my-downloader",
    type = "hook",
    permissions = {"exec"},
})

local handle, err = cliamp.exec.run("yt-dlp", {"--dump-json", url}, {
    on_stdout = function(line) ... end,   -- optional, called per line
    on_stderr = function(line) ... end,   -- optional
    on_exit   = function(code) ... end,   -- optional, fires exactly once
    cwd       = "/tmp/work",              -- optional; must be in write allowlist
    timeout   = 300,                       -- optional seconds, hard cap 1800
})

handle:cancel()                           -- terminate the process
handle:alive()                            -- --> boolean
```

**Safety rules:**

- The binary must be in the allowlist. Argv is argv. No shell or expansion is used.
- The read-only paths of `cliamp.fs` apply only to `cliamp.fs` and to `cwd`. With the exec permission, `yt-dlp` or `ffmpeg` can write to any path that you can write. Examples are the `--exec` option of `yt-dlp` and an output path of `ffmpeg`.
- `args` must be a flat array of strings. cliamp rejects nested tables and non-strings.
- The subprocess environment contains only `PATH`, `HOME`, and `LANG`. cliamp does not pass the other variables of its environment. This does not hide secrets from the plugin, which can read each variable with `os.getenv()`.
- Output is limited to 4 MiB per process, for stdout and stderr together. cliamp silently drops later lines.
- A line is limited to 1 MiB. After a longer line, cliamp silently drops the rest of that stream. The process continues to run.
- Each plugin can run up to 4 processes at one time.
- cliamp kills every plugin-owned process when the plugin unloads and when cliamp exits.
- On Linux and macOS, a cancel or a timeout kills the process and the child processes that it started, such as the `ffmpeg` of `yt-dlp`. cliamp closes the output pipes 0.5 seconds later, so a child that left the process group cannot keep `on_exit` from running.
- Negative `on_exit` codes indicate cancellation or timeout (`-1`), or a start failure (`-2`).
- Each call of `on_stdout`, `on_stderr`, or `on_exit` times out after 5 seconds.

Without `permissions = {"exec"}`, `cliamp.exec.run` returns `nil, "exec permission required"`.

### cliamp.message

```lua
cliamp.message("Scrobble Sent")        -- show for default duration
cliamp.message("Syncing Library", 5)   -- show for 5 seconds
```

This shows a temporary message in the status bar at the bottom of the UI. The
duration is optional and uses seconds. Omit it to use the default TTL. cliamp
limits durations above 60 seconds. The call returns at once, and cliamp shows
the messages and applies the controls of a plugin in the order of the calls.

### cliamp.sleep

```lua
cliamp.sleep(2.5)  -- block for 2.5 seconds (max 10)
```

This blocks the plugin's Lua VM. Other hooks for the same plugin wait until the sleep ends. Use `cliamp.timer.after()` for a non-blocking delay. The sleep ends early when the time limit of the running callback or of the plugin load ends.

### cliamp.timer

```lua
-- Run once after 5 seconds
local id = cliamp.timer.after(5.0, function()
    cliamp.log.info("timer fired")
end)

-- Run every 30 seconds
local id = cliamp.timer.every(30.0, function()
    -- periodic task
end)

-- Cancel
cliamp.timer.cancel(id)
```

Each timer callback times out after 5 seconds.

## Configuration

Put plugin-specific configuration in `config.toml` under `[plugins.<name>]`:

```toml
[plugins.lastfm]
api_key = "abc123"
api_secret = "secret"
session_key = "sk-xxx"

[plugins.webhook]
url = "https://example.com/hook"
```

Read it in Lua:

```lua
local api_key = p:config("api_key")   --> "abc123" or nil
```

### Disabling plugins

Disable one plugin:

```toml
[plugins.webhook]
enabled = false
```

To disable several plugins:

```toml
[plugins]
disabled = webhook, discord-rpc
```

A list in square brackets also works, such as `disabled = ["webhook", "discord-rpc"]`. `allowed_binaries` accepts the same forms. `enabled` accepts every bool form in [Value syntax](configuration.md#value-syntax), such as `False`.

## Visualizer plugins

Plugins with `type = "visualizer"` add visualizer modes to the `v` key cycle with the built-in modes. A name selects the built-in mode when a visualizer plugin has the same name, for example `Bars`, in `cliamp vis <name>` and in the `visualizer` config key. Use the `v` key or the picker to select that plugin, or give it another name.

```lua
-- ~/.config/cliamp/plugins/simple-bars.lua
local p = plugin.register({
    name = "simple-bars",
    type = "visualizer",
})

-- Called every frame (~20 FPS during playback).
-- bands: table of 10 numbers (0.0-1.0), indices 1-10, eased between
--        analyses as in the built-in spectrum modes
-- frame: monotonic counter
-- rows: available terminal rows (changes in fullscreen mode)
-- cols: available terminal columns
-- Must return a multi-line string.
function p:render(bands, frame, rows, cols)
    local lines = {}
    local chars = { " ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█" }

    for row = 5, 1, -1 do
        local line = ""
        for i = 1, 10 do
            local level = bands[i]
            local threshold = (row - 1) / 5
            if level > threshold then
                line = line .. "██████ "
            else
                line = line .. "       "
            end
        end
        table.insert(lines, line)
    end

    return table.concat(lines, "\n")
end
```

### Visualizer callbacks

| Callback | Signature | Required |
|----------|-----------|----------|
| `p:render(bands, frame, rows, cols)` | Returns string | Yes |
| `p:init(rows, cols)` | Setup when selected | No |
| `p:destroy()` | Cleanup when deselected | No |

cliamp calls `init` before the first frame after you select the visualizer, with the size of that frame. It calls `destroy` when you select another visualizer after `init` ran. It calls neither when you leave the visualizer before it draws a frame, and it does not call `destroy` when cliamp quits. Use the `app.quit` event for cleanup at exit.

`render` has a 50 ms limit for each frame. If it runs longer or fails, cliamp shows the previous frame. cliamp also shows the previous frame while another callback of the same plugin runs, so a slow hook does not delay the UI. cliamp runs `init` and `destroy` in order with the events of the plugin, and `render` shows the previous frame until `init` has run.

## Sandbox

For security, plugins have restricted access. The sandbox removes unsafe standard-library functions and limits file-system access.

### Removed functions

| Removed | Replacement |
|---------|-------------|
| `os.execute`, `os.remove`, `os.rename`, `os.exit`, `os.setlocale`, `os.tmpname` | Use `cliamp.fs`, `cliamp.http`, or permission-gated `cliamp.exec` |
| `io` module (all of it) | Use `cliamp.fs` |
| `dofile`, `loadfile`, `load`, `loadstring`, `require`, `module`, `package`, `debug` | Not available |

### Kept functions

You can use `os.time()`, `os.date()`, `os.clock()`, and `os.getenv()`. `os.getenv()` reads each variable of the cliamp environment, including tokens and passwords.

### File system restrictions

**Reads:** You can read any regular file, up to 1 MB for each read.

**Writes/removes/mkdir** work only in these directories:

- `/tmp/` (and the system temp directory)
- `~/.config/cliamp/`
- `~/.local/share/cliamp/`
- `~/Music/cliamp/`

These paths in `~/.config/cliamp/` stay read-only for `cliamp.fs` and for the `cwd` of `cliamp.exec`:

- `plugins/`, which contains the plugin files and `plugins/.trust.json`
- `config.toml`
- `radios.toml`
- `cliamp.sock`
- `cliamp.sock.pid`
- `plugins.log`

Thus a plugin cannot use `cliamp.fs` to approve plugins, change the exec allowlist, stop the next start of cliamp, or hide its log. A plugin with the exec permission can still write to any path that you can write through `yt-dlp` or `ffmpeg`. Approve a plugin that declares the exec permission only when you trust it.

Writing outside these directories, or to a read-only path, raises a Lua error. cliamp resolves symlinks and blocks directory traversal (`..`) before it checks the path. The `cwd` of `cliamp.exec` follows the same rules.

### Isolation

- Each plugin has its own Lua VM. A plugin cannot reach the Lua variables of another plugin.
- The sandbox does not keep secrets from a plugin. `cliamp.fs` reads each file that you can read. Examples are `config.toml`, with the `[plugins.<name>]` keys of every plugin, and the `cliamp.store` file of another plugin. `os.getenv()` reads the environment. Install only plugins that you trust with these values.
- A plugin crash does not affect other plugins or the player.
- Use `cliamp.http` for public network access. Raw socket access is not available. cliamp blocks private, loopback, link-local, multicast, and unspecified destinations after DNS resolution and through redirects.
- `os.execute` is not available. Permission-gated `cliamp.exec` can start only configured allowed binaries.

## Debugging

Check `~/.config/cliamp/plugins.log` for plugin output and errors:

```
2025-03-29 14:30:01 [now-playing] info: Now playing: Everything In Its Right Place
2025-03-29 14:30:01 [webhook] error: track.change handler error: connection refused
```

Use `cliamp.log.debug()` during development.
