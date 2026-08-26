# Remote Control (IPC)

Control cliamp locally from a terminal, script, status bar, or GUI.

cliamp listens on `~/.config/cliamp/cliamp.sock` with `0600` permissions. It
uses newline-delimited JSON over a local Unix socket. To use SSH, run the client
command on the host that owns the socket.

## Quick Start

```sh
cliamp status --json
cliamp next
cliamp remote state
cliamp remote events runtime.state runtime.job
```

IPC supports version 2 only. Clients must send a V2 envelope with each request.
See [Upgrading IPC Clients To V2](upgrading-ipc-v2.md) when you migrate a raw
socket integration.

## Version 2

V2 responses use the request `id` and always include `"version":2`.

```json
{"version":2,"id":"state","method":"state.get"}
{"version":2,"id":"play","method":"operation.submit","operation":"play","params":{}}
{"version":2,"id":"queue","method":"operation.submit","operation":"queue.enqueue","params":{"index":4,"if_revision":18}}
```

Use these methods:

| Method | Purpose |
| --- | --- |
| `capabilities` | List available operation names and parameter hints |
| `state.get` | Read the runtime snapshot |
| `spectrum.get` | Read current visualizer bands |
| `operation.submit` | Start a runtime or library operation |
| `job.get` | Read an operation job by `job_id` |
| `job.cancel` | Request cancellation of an active job |
| `subscribe` | Start a server-to-client event stream |

`operation.submit` returns a job immediately. A job can become `queued`,
`running`, `succeeded`, `failed`, or `canceled`. Its final record contains the
operation result and the snapshot from the committed operation.

```json
{
  "version": 2,
  "id": "play",
  "ok": true,
  "job": {
    "id": "8f0d...",
    "operation": "play",
    "state": "queued"
  }
}
```

Fast operations can finish before the client requests `job.get`. Slow work,
such as provider access, URL resolution, downloads, lyrics, and saved playlist
writes, remains asynchronous.

## Runtime Snapshot

`state.get` returns a snapshot with the active audio track, logical playlist
track, playback state, position, duration, seekability, modes, EQ, visualizer,
theme, stream error, and two revisions.

```json
{
  "version": 2,
  "id": "state",
  "ok": true,
  "snapshot": {
    "revision": 18,
    "playlist_revision": 7,
    "state": "playing",
    "track": {"title":"Song","path":"/music/song.flac"},
    "logical_track": {"title":"Song","path":"/music/song.flac"},
    "position": 42.5,
    "duration": 183,
    "seekable": true,
    "play_next_total": 2
  }
}
```

`revision` changes when meaningful runtime state changes. `playlist_revision`
changes when the live playlist or play-next list changes. Position-only playback
ticks do not create events. Send `if_revision` with destructive live-playlist or
play-next operations to reject stale GUI actions with the `conflict` error code.

`track` keeps `provider_meta`, embedded playback flags, bookmark state, and
directory-source state. A GUI can send a provider result through `track.play`,
`track.queue`, `playlist.add`, `playlist.add_many`, or `playlist.replace`
without losing provider identity.

## Operations

Run `cliamp remote capabilities` to get the current machine-readable list.

| Group | Operations |
| --- | --- |
| Playback | `play`, `pause`, `toggle`, `stop`, `next`, `prev`, `volume`, `volume.adjust`, `seek`, `seek.absolute`, `speed`, `speed.adjust`, `shuffle`, `repeat`, `mono`, `eq`, `device` |
| Appearance | `theme`, `vis` |
| Live playlist | `queue`, `queue.list`, `queue.play`, `queue.enqueue`, `queue.remove`, `queue.move`, `queue.clear`, `track.play`, `track.queue` |
| Play-next | `playnext.list`, `playnext.remove`, `playnext.move`, `playnext.clear` |
| Sources | `load`, `url.load`, `save`, `lyrics`, `history`, `history.clear` |
| Providers | `provider.list`, `provider.playlists`, `provider.tracks`, `provider.load`, `provider.search`, `provider.artists`, `provider.artist_albums`, `provider.albums`, `provider.album_tracks`, `provider.load_album`, `provider.favorite`, `provider.catalog` |
| Saved playlists | `playlist.create`, `playlist.rename`, `playlist.delete`, `playlist.add`, `playlist.add_many`, `playlist.replace`, `playlist.remove`, `playlist.bookmark` |
| Plugins | `plugin.call`, `plugin.commands` |

`queue.*` applies to the live playlist. `playnext.*` applies only to the
play-next list. They use separate zero-based indexes.

Request provider list responses with `offset` and `limit` when the provider
supports paging. Use `playlist.replace` to save a GUI-created order, sort, or
deduplication result as one operation when the provider supports playlist saving.

## Events

Subscribe with an exact topic list. The acknowledgement is V2. Later lines use
the shared event envelope, so plugin and runtime events use the same form.

```json
{"version":2,"id":"events","method":"subscribe","topics":["runtime.state","runtime.job"]}
```

Core retained topics are `runtime.state`, `runtime.playback`,
`runtime.playlist`, and `runtime.settings`. `runtime.job` is temporary and
contains final job records. Plugin topics keep their `plugin.*` names.

If a client cannot keep up, it receives `system.overflow` with
`{"resync_required":true}` before the stream closes. Reconnect and request
`state.get` before you accept more changes.

## Spectrum Stream

`cliamp visstream` uses V2 `spectrum.get` internally. It outputs one plain
NDJSON frame at 30 FPS by default for status-bar and visualizer integrations.
Use `--fps` to set a rate from 1 through 60. GUI clients can request one current
frame with `spectrum.get`.

## CLI V2 Client

```sh
cliamp remote state
cliamp remote capabilities
cliamp remote call queue.enqueue --params '{"index":4,"if_revision":18}' --wait
cliamp remote job JOB_ID
cliamp remote cancel JOB_ID
cliamp remote events runtime.state runtime.job
```

`remote call` prints the V2 response as JSON and can submit every listed
operation. Use it in scripts and to validate a GUI integration.

Named CLI commands such as `cliamp volume`, `cliamp seek`, `cliamp load`, and
`cliamp plugins call` use V2 jobs internally. `volume` sets an absolute dB
value. `seek` is relative to the current position. V2 subscriptions read
`plugin.*` topics and runtime events.

## Headless Mode

```sh
cliamp --daemon --auto-play --playlist Lofi
```

The daemon exposes the same playback, queue, provider, saved-playlist, job,
snapshot, and event APIs. It has no TUI theme or visualizer selection. It does
not load Lua plugins. Use `capabilities` instead of assuming that each
interactive-only operation is available.

## Errors And Limits

V2 errors use stable codes: `invalid_version`, `invalid_request`,
`invalid_params`, `unknown_operation`, `not_found`, `conflict`, `unavailable`,
`canceled`, and `internal_error`. Runtime failures can include a `detail` string
with a provider, device, or plugin diagnostic.

Frames are limited to 1 MiB. Use paging for large provider or playlist results.
Jobs are process-local, bounded, and retained for 15 minutes after completion.
cliamp cancels them during an orderly server shutdown. They are not available
after a restart.
