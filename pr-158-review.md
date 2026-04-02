# PR #158 Review — Pending Fixes

## Part 1: Simplify (code reuse, quality, efficiency)

These were identified by three parallel review passes (reuse, quality, efficiency) and previously applied, then lost to force push.

### 1.1 Duplicate `audioExtensions` / `isAudioFile` in cmd/playlist.go

**File:** `cmd/playlist.go:16-30`

`player.SupportedExts` already exists at `player/decode.go:24-36` with 11 entries. The PR's `audioExtensions` is a 7-entry subset missing `.aac`, `.m4b`, `.alac`, `.webm`. Users creating SSH playlists with those formats would silently skip files the player can actually play.

**Fix:** Delete `audioExtensions` and `isAudioFile` from `cmd/playlist.go`. Use `player.SupportedExts` in `sshFindAudio`. For local walking, use `resolve.CollectAudioFiles` (see 1.2).

---

### 1.2 Duplicate `walkDir` in cmd/playlist.go

**File:** `cmd/playlist.go:386-399`

`resolve.go:190-222` already has `collectAudioFiles()` which does the same thing but better: uses `filepath.WalkDir` (avoids extra Stat per entry), uses the canonical `player.SupportedExts`, and sorts results.

**Fix:** Export `collectAudioFiles` as `resolve.CollectAudioFiles`. Replace `walkDir` + the stat/isDir/walk loop in both `PlaylistCreate` (lines 77-91) and `PlaylistAdd` (lines 134-148) with calls to `resolve.CollectAudioFiles`.

---

### 1.3 Duplicate `trackFromFilename` in cmd/playlist.go

**File:** `cmd/playlist.go:442-456`

`playlist/tags.go:41-49` has an identical `trackFromFilename` (unexported). The `playlist` version also applies `sanitizeTag()`.

**Fix:** Export as `playlist.TrackFromFilename`. Use it in `cmd/playlist.go` for SSH track creation.

---

### 1.4 N+1 file I/O in AddTrack loop

**Files:** `cmd/playlist.go:99-111, 155-160` calling `external/local/provider.go:100-122`

`PlaylistCreate` and `PlaylistAdd` call `prov.AddTrack()` in a loop. Each call opens the TOML file, stats it, writes one track, and closes it. For a 500-track playlist, that's 500 open/stat/write/close cycles.

**Fix:** Add `AddTracks(name string, tracks []playlist.Track) error` to the local provider that opens the file once, writes all tracks, and closes once. Use it in `PlaylistCreate` and `PlaylistAdd`.

---

### 1.5 `playlistExists` loads all TOML files

**Files:** `cmd/playlist.go:458-470`, also inline at `PlaylistCreate:55-64`

Both `playlistExists()` and `PlaylistCreate` call `prov.Playlists()` which reads and fully parses every TOML file in the directory, just to check if one name exists.

**Fix:** Add `Exists(name string) bool` to the local provider that does `os.Stat` on the specific `.toml` file — a single syscall instead of reading N files.

---

### 1.6 TOCTOU race in ipc/client.go

**File:** `ipc/client.go:27-29`

```go
if _, err := os.Stat(sockPath); os.IsNotExist(err) {
    return Response{}, fmt.Errorf("cliamp is not running (no socket at %s)", sockPath)
}
conn, err := net.DialTimeout("unix", sockPath, 3*time.Second)
```

The `os.Stat` check is redundant — `DialTimeout` returns a clear error if the socket doesn't exist. The stat adds a race window (socket could appear/disappear between stat and dial).

**Fix:** Remove the `os.Stat` check. Wrap `DialTimeout` errors with the user-friendly message when they indicate `ENOENT` or `ECONNREFUSED`.

---

### 1.7 Fragile `--index` parsing in main.go

**File:** `main.go:588-592` (also duplicated at 619-622 for `playlist-favorite`)

```go
for i, arg := range positional[1:] {
    if arg == "--index" && i+2 < len(positional) {
        fmt.Sscanf(positional[i+2], "%d", &idx)
    }
}
```

`i` is 0-based within the sub-slice `positional[1:]`, so `positional[i+2]` works by coincidence (sub-slice offset +1 and skip-flag offset +1 = +2). `fmt.Sscanf` silently swallows parse errors — `--index abc` produces a generic "usage" message with no indication of the bad value.

**Fix:** Use `strconv.Atoi` for explicit error reporting (consistent with `volume`/`seek` parsing). Iterate with explicit index math.

---

### 1.8 Over-engineered Dispatcher interface

**Files:** `ipc/protocol.go:5-6, 34-38` and `ipc/server.go:18-22`

The `Dispatcher` interface + `DispatcherFunc` adapter + compile-time check is three moving parts. Only `DispatcherFunc` is ever used (at `main.go:316`). No code references the `Dispatcher` interface as a type constraint.

**Fix:** Replace with a plain `SendFunc func(msg interface{})` type. Change `Server.disp Dispatcher` to `Server.send SendFunc`.

---

### 1.9 64KB scanner buffer per IPC connection

**File:** `ipc/server.go:110-111`

```go
scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
```

Allocates 64KB per connection for commands that are ~50 bytes. The default `bufio.Scanner` is 4KB initial / 64KB max, which is already sufficient.

**Fix:** Remove the `scanner.Buffer` call. Use the default scanner.

---

### 1.10 Duplicate directory-walking code in PlaylistCreate/PlaylistAdd

**Files:** `cmd/playlist.go:77-91` and `cmd/playlist.go:134-148`

Near-identical stat/isDir/walkDir/isAudioFile loops. Resolved automatically by fix 1.2 (use `resolve.CollectAudioFiles`), but should be extracted into a `collectLocalAudio(paths) ([]string, error)` helper within `cmd/playlist.go`.

---

## Part 2: Architecture

These were identified by architectural review and are new issues not yet addressed.

### 2.1 Message type duplication between MPRIS and IPC (Medium severity)

**Files:** `mpris/mpris.go:19-29`, `ipc/protocol.go:42-61`, `ui/model/update.go:617-660` vs `672-756`

MPRIS and IPC define parallel, semantically identical message types — `NextMsg`, `PrevMsg`, `StopMsg`, `ToggleMsg`/`PlayPauseMsg` — producing ~40 lines of duplicated handler logic in `update.go`. The Lua plugin subsystem already proves cross-package reuse works (`main.go:305` sends `mpris.NextMsg{}` from a non-MPRIS context).

IPC-only types that should stay in `ipc/`: `PlayMsg`, `PauseMsg`, `VolumeMsg`, `SeekMsg`, `LoadMsg`, `QueueMsg`, `StatusRequestMsg`.

**Fix:** Create `control/msg.go` with shared `ToggleMsg`, `NextMsg`, `PrevMsg`, `StopMsg`. Use type aliases in mpris (`type NextMsg = control.NextMsg`) for backwards compat. Remove the 4 duplicate types from `ipc/protocol.go`. Remove the 4 duplicate handler cases from `update.go`. Update `ipc/server.go` to use `control.NextMsg{}` etc.

---

### 2.2 SSH + ffmpeg formats silently fail (Medium severity)

**File:** `player/pipeline.go:213`

SSH paths like `ssh://nas/file.m4a` have `isURL() == false` (no `http://`), so they fall into the local ffmpeg branch which passes the raw `ssh://` path to ffmpeg as a local file. FFmpeg can't read `ssh://` URLs. The docs acknowledge this as a "limitation" but the code doesn't guard against it.

**Fix:** Add an explicit check before the local ffmpeg branch in `buildPipelineAt`:

```go
if isSSH(path) && needsFFmpeg(ext) {
    rc.Close()
    return nil, fmt.Errorf("SSH streaming does not support %s format (requires ffmpeg)", ext)
}
```

---

### 2.3 Playlist flag parsing sprawled across main.go (Low severity)

**File:** `main.go:531-642`

The `--ssh`, `--json`, `--index` flags are parsed inline in `main.go` (~110 lines) rather than in `cmd/`. This mixes CLI parsing into the dispatch layer. The pattern is now duplicated for `playlist-remove` and `playlist-favorite` (both parse `--index` with identical code).

**Fix:** Change `cmd.PlaylistCreate` to accept raw positional args and extract `--ssh` internally. Same for `PlaylistShow` (extract `--json`), `PlaylistRemove` and `PlaylistFavorite` (extract `--index`). This reduces each main.go dispatch case to 3-5 lines, matching the IPC pattern.

---

### 2.4 SSH URL parsing doesn't handle ports (Low severity)

**Files:** `player/decode.go:88-97`, `cmd/playlist.go:318-325`, `cmd/playlist.go:367-372`

All three locations manually split `ssh://` URLs by finding the first `/`:

```go
rest := strings.TrimPrefix(path, "ssh://")
slashIdx := strings.IndexByte(rest, '/')
host := rest[:slashIdx]
```

`ssh://host:2222/path` produces `host = "host:2222"` which the SSH binary misinterprets (it needs `-p 2222` as a separate flag). The `PlaylistEnrich` function at line 318-325 duplicates this parsing a third time.

**Fix:** Use `net/url.Parse` to correctly separate host, port, and user. Pass `-p port` to SSH when port is specified. Extract a shared `parseSSHURL(raw string) (host, port, path string, err error)` helper (either in `player/` or a small `internal/sshurl/` package) and use it in all three locations.

---

### 2.5 Documentation/code mismatch on StrictHostKeyChecking (Low severity)

**File:** `docs/ssh-streaming.md:75`

Docs say: `StrictHostKeyChecking=accept-new`
Code uses: `StrictHostKeyChecking=yes` (at `player/decode.go:104` and `cmd/playlist.go:419`)

The code is the safer choice (rejects unknown hosts). The docs should match.

**Fix:** Change line 75 of `docs/ssh-streaming.md`:

```
| Unknown host key | Rejected — add the host to known_hosts first or configure in `~/.ssh/config` |
```

---

## Part 3: Additional observations from force-push changes

### 3.1 `PlaylistEnrich` duplicates SSH URL parsing a third time

**File:** `cmd/playlist.go:318-325`

Same manual `ssh://` parsing as decode.go and sshFindAudio. Will be resolved by fix 2.4 (shared `parseSSHURL` helper).

### 3.2 `PlaylistEnrich` shell injection surface in afinfo command

**File:** `cmd/playlist.go:371`

```go
fmt.Sprintf("afinfo %s 2>/dev/null | grep 'estimated duration' | awk '{print int($3)}'", shellQuote(remotePath))
```

The `shellQuote` function correctly handles the remote path, but the entire pipeline is passed as a single string to the remote shell. This is fine as-is since `shellQuote` properly escapes single quotes, but is worth noting as a security-sensitive pattern.

### 3.3 `playlist-remove` and `playlist-favorite` duplicate identical `--index` parsing

**File:** `main.go:588-592` and `main.go:619-622`

Byte-for-byte identical loops. Both have the same fragile arithmetic (see 1.7). Resolved by fix 2.3 (move parsing into `cmd/`).
