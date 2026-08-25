# Remote Control (IPC)

Control cliamp locally from a terminal, script, status bar, or GUI.

cliamp listens on `~/.config/cliamp/cliamp.sock` with `0600` permissions. The
transport is newline-delimited JSON over a local Unix socket. SSH access works
by running the client command on the host that owns the socket.

## Quick Start

```sh
cliamp status --json
cliamp next
cliamp remote state
cliamp remote events runtime.state runtime.job
```

IPC is version 2 only. Clients must send a V2 envelope for every request.
See [Upgrading IPC Clients To V2](upgrading-ipc-v2.md) when migrating a raw
socket integration.

## Version 2

V2 responses are correlated with the request `id` and always include
`"version":2`.

```json
{"version":2,"id":"state","method":"state.get"}
{"version":2,"id":"play","method":"operation.submit","operation":"play","params":{}}
{"version":2,"id":"queue","method":"operation.submit","operation":"queue.enqueue","params":{"index":4,"if_revision":18}}
```

Use these methods:

| Method | Purpose |
| --- | --- |
| `capabilities` | List available operation names and parameter hints |
| `state.get` | Read the complete runtime snapshot |
| `spectrum.get` | Read current visualizer bands |
| `operation.submit` | Start a runtime or library operation |
| `job.get` | Read an operation job by `job_id` |
| `job.cancel` | Cooperatively cancel an active job |
| `subscribe` | Start a server-to-client event stream |

`operation.submit` returns a job immediately. A job becomes `queued`,
`running`, `succeeded`, `failed`, or `canceled`. Its terminal record includes
the operation-specific result and the snapshot produced by the committed
operation.

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

Fast operations can complete before the client asks for `job.get`. Slow work,
including provider access, URL resolution, downloads, lyrics, and saved
playlist writes, remains asynchronous.

## Runtime Snapshot

`state.get` returns a snapshot containing the active audio track, logical
playlist track, current playback state, position, duration, seekability, modes,
EQ, visualizer, theme, stream error, and two revisions.

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

`revision` changes on meaningful runtime state changes. `playlist_revision`
changes on live-playlist and play-next mutations. Position-only playback ticks
do not create events. Send `if_revision` with destructive live-playlist or
play-next operations to reject stale GUI actions with the `conflict` error code.

`track` preserves `provider_meta`, embedded playback flags, bookmark state,
and directory-source state. A GUI can round-trip a provider result through
`track.play`, `track.queue`, `playlist.add`, `playlist.add_many`, or
`playlist.replace` without dropping provider identity.

## Operations

Use `cliamp remote capabilities` for the machine-readable current list.

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

`queue.*` addresses the live playlist. `playnext.*` addresses only the
play-next list. They intentionally use separate zero-based indexes.

Provider list responses should be requested with `offset` and `limit` where
the provider supports paging. Use `playlist.replace` to atomically persist a
GUI-created order, sort, or deduplication result for providers that support
playlist saving.

## Events

Subscribe with an exact topic list. The acknowledgement is V2; subsequent
lines use the shared event envelope so plugin and runtime events work the same.

```json
{"version":2,"id":"events","method":"subscribe","topics":["runtime.state","runtime.job"]}
```

Core retained topics are `runtime.state`, `runtime.playback`,
`runtime.playlist`, and `runtime.settings`. `runtime.job` is transient and
contains terminal job records. Plugin topics retain their `plugin.*` names.

If a client cannot keep up, it receives `system.overflow` with
`{"resync_required":true}` before the stream closes. Reconnect and issue
`state.get` before accepting more mutations.

## Spectrum Stream

`cliamp visstream` uses V2 `spectrum.get` internally and emits one plain NDJSON
frame at 30 FPS by default for status-bar and visualizer integrations. Use
`--fps` for a rate from 1 through 60. GUI clients can pull one current frame
with `spectrum.get`.

## CLI V2 Client

```sh
cliamp remote state
cliamp remote capabilities
cliamp remote call queue.enqueue --params '{"index":4,"if_revision":18}' --wait
cliamp remote job JOB_ID
cliamp remote cancel JOB_ID
cliamp remote events runtime.state runtime.job
```

`remote call` prints the full V2 response as JSON and can submit every listed
operation. It is intended for scripts and for validating a GUI integration.

Named CLI commands such as `cliamp volume`, `cliamp seek`, `cliamp load`, and
`cliamp plugins call` use V2 jobs internally. `volume` sets an absolute dB
value and `seek` is relative to the current position. V2 subscriptions consume
`plugin.*` topics alongside runtime events.

## Headless Mode

```sh
cliamp --daemon --auto-play --playlist Lofi
```

The daemon exposes the same playback, queue, provider, saved-playlist, job,
snapshot, and event APIs. It has no TUI theme or visualizer selection, and Lua
plugins are not loaded. Use `capabilities` instead of assuming every
interactive-only operation is available.

## Errors And Limits

V2 errors use stable codes: `invalid_version`, `invalid_request`,
`invalid_params`, `unknown_operation`, `not_found`, `conflict`, `unavailable`,
`canceled`, and `internal_error`. Runtime failures include an optional `detail`
string with the provider, device, or plugin diagnostic.

Frames are limited to 1 MiB. Use paging for large provider or playlist results.
Jobs are process-local, bounded, and retained for 15 minutes after completion.
They are canceled during orderly server shutdown and are not available after a
restart.
