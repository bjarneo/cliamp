# Upgrading IPC Clients To V2

Update raw Unix-socket clients to V2. Named `cliamp` commands already use V2
and need no script changes.

## Quick Migration

Replace an unversioned command with a V2 envelope. Every V2 request includes
`version: 2` and a request `id`.

```json
{"version":2,"id":"next","method":"operation.submit","operation":"next","params":{}}
```

The response returns a job immediately. Read `job.id`, then wait for a terminal
`runtime.job` event or call `job.get`.

```json
{"version":2,"id":"job","method":"job.get","job_id":"JOB_ID"}
```

## Command Mapping

| Previous request | V2 request |
| --- | --- |
| `{"cmd":"status"}` | `{"version":2,"id":"state","method":"state.get"}` |
| `{"cmd":"bands"}` | `{"version":2,"id":"bands","method":"spectrum.get"}` |
| `{"cmd":"next"}` | `{"version":2,"id":"next","method":"operation.submit","operation":"next","params":{}}` |
| `{"cmd":"volume","value":-5}` | `{"version":2,"id":"volume","method":"operation.submit","operation":"volume","params":{"value":-5}}` |
| `{"cmd":"seek","value":30}` | `{"version":2,"id":"seek","method":"operation.submit","operation":"seek","params":{"value":30}}` |
| `{"cmd":"queue","path":"/music/song.flac"}` | `{"version":2,"id":"queue","method":"operation.submit","operation":"queue","params":{"path":"/music/song.flac"}}` |
| `{"cmd":"provider.search","provider":"local","query":"ambient"}` | `{"version":2,"id":"search","method":"operation.submit","operation":"provider.search","params":{"provider":"local","query":"ambient"}}` |

`volume` sets an absolute dB value. `seek` is relative to the current playback
position. Use `seek.absolute` for an absolute position.

## State And Results

`state.get` returns its data in the V2 `snapshot` field. It replaces the old
top-level status object and includes the active playback track, logical
playlist track, playback state, position, modes, EQ, theme, device, and
playlist revisions.

Mutation and provider operations return a job. A terminal job includes:

- `state`: `succeeded`, `failed`, or `canceled`
- `result`: operation-specific data such as tracks, devices, lyrics, or output
  paths
- `snapshot`: the runtime state after the accepted operation
- `error`: stable `code` and `message`, with an optional diagnostic `detail`

Use `if_revision` for destructive live-playlist and play-next mutations. A
stale revision fails with `conflict` instead of applying a stale GUI action.

## Events

Replace the old subscription request with a V2 subscription. The
acknowledgement is a V2 response; event lines keep the shared event envelope.

```json
{"version":2,"id":"events","method":"subscribe","topics":["runtime.state","runtime.job","plugin.example.playback"]}
```

Core retained topics are `runtime.state`, `runtime.playback`,
`runtime.playlist`, and `runtime.settings`. `runtime.job` is transient and
reports terminal jobs. Plugin topics remain `plugin.*`.

If a stream receives `system.overflow`, reconnect and call `state.get` before
submitting another mutation.

## Spectrum Clients

`cliamp visstream` keeps its existing one-frame-per-line NDJSON output. It now
uses V2 `spectrum.get` internally, so Quickshell and status-bar integrations do
not need parser changes.

Raw clients can issue `spectrum.get` at their preferred bounded frame rate. Use
one request and response at a time on a connection.

## Client Checklist

- Send only V2 envelopes. Unversioned requests fail with `invalid_version`.
- Correlate every response with its `id`.
- Use `state.get` for direct state reads.
- Use `operation.submit` plus jobs for control, library, and provider work.
- Subscribe to `runtime.job` instead of assuming an operation has finished.
- Query `capabilities` at startup, especially in daemon mode where theme,
  visualizer selection, and plugins are unavailable.

See [Remote Control](remote-control.md) for the complete V2 operation list and
error contract.
