# cliamp

Retro terminal music player. Fork of [bjarneo/cliamp](https://github.com/bjarneo/cliamp) adding CLI playlist management, Unix socket IPC, and SSH streaming.

## Stack
- Go 1.25.5 — Bubbletea TUI, Lip Gloss styling, Beep audio, go-librespot (Spotify)
- 170 Go files, module name `cliamp`
- Config: `~/.config/cliamp/config.toml`, playlists: `~/.config/cliamp/playlists/*.toml`

## Commands
- Build: `go build -o cliamp .`
- Test: `go test ./...`
- Vet: `go vet ./...`
- Install: `cp cliamp ~/.local/bin/cliamp`

## Structure
- `main.go` — entry point, provider wiring, CLI action dispatch (switch on `config.ParseFlags()`)
- `config/` — `config.go` (TOML config), `flags.go` (CLI arg parsing with subcommand routing)
- `player/` — audio pipeline: `decode.go` (source opening: local/HTTP/SSH), `pipeline.go`, `player.go`
- `playlist/` — `Track` struct, `Playlist` state machine, `Provider` interface, ID3 tag reading
- `provider/` — extended interfaces: `PlaylistWriter`, `PlaylistDeleter`, `Searcher`, `AlbumBrowser`
- `external/` — provider implementations: `local/` (TOML), `radio/`, `navidrome/`, `spotify/`, etc.
- `ui/model/` — Bubbletea model: `model.go`, `keys.go`, `view.go`, `providers.go`, `overlays.go`
- `cmd/` — CLI subcommand handlers (`playlist.go`)
- `ipc/` — Unix socket IPC: `server.go`, `client.go`, `protocol.go`
- `mpris/` — D-Bus media controls (Linux)
- `luaplugin/` — Lua plugin system
- `internal/appdir/` — `Dir()` returns `~/.config/cliamp`

## Key Patterns

### External control via prog.Send()
The TUI accepts commands from external sources by sending Bubbletea messages through `prog.Send(msg)`. Three systems use this pattern:
- **MPRIS** (`mpris/mpris.go`) — D-Bus on Linux
- **Lua plugins** (`main.go:288-303`) — `ControlProvider` struct with function fields
- **IPC server** (`ipc/server.go`) — Unix socket, dispatches via `DispatcherFunc`

### Provider system
Providers implement `playlist.Provider` (Name, Playlists, Tracks). Extended interfaces add write ops. The local provider (`external/local/provider.go`) handles TOML file CRUD. Providers appear as pill tabs in the TUI; local playlists appear in the playlist manager (`p` key).

### CLI subcommand dispatch
`config.ParseFlags()` returns an action string. Subcommands like `plugins`, `playlist`, IPC commands are detected before the flag loop. `main.go` switches on action to dispatch.

### Audio pipeline
`player/decode.go:openSourceAt()` opens audio from three source types: local files (`os.Open`), HTTP URLs, and SSH paths (`ssh host cat /path`). Returns `io.ReadCloser` for the decoder.

## Fork Additions (tdimino/cliamp)
- `cmd/playlist.go` — `cliamp playlist {list,create,add,show,remove,delete}` with `--ssh HOST` and `--json`
- `ipc/` — JSON-over-Unix-socket remote control (`cliamp play/pause/next/status/load/...`)
- `player/decode.go` — SSH streaming via `ssh://host/path` URL scheme
- `config/flags.go` — subcommand routing for playlist + IPC commands

## Conventions
- Zero new Go dependencies for fork features — stdlib only (net, encoding/json, os/exec)
- Errors to stderr, JSON to stdout. `--json` flag for machine-readable output
- Follow existing patterns: subcommand dispatch mirrors `plugins`, IPC mirrors MPRIS
- Socket at `~/.config/cliamp/cliamp.sock` with PID file for stale detection
