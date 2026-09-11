# CLAUDE.md — cliamp

> A retro terminal music player (Go + Bubbletea). This file tells AI agents where things live, what conventions the codebase uses, and which skills to lean on.

## Extended context

**More narrative detail, design notes, and roadmap context for cliamp lives in `~/Documents/bjarne/projects/cliamp/`.** Read files in that directory when you need background that isn't captured in code or in `docs/`. Treat it as the project's long-form knowledge base (goals, decisions, TODOs). If a question feels strategic rather than tactical, check there first.

---

## What cliamp is

A TUI music player inspired by Winamp. Plays local files, HTTP streams, podcasts, and content from many providers: YouTube / YouTube Music, SoundCloud, Bilibili, Spotify, Xiaoyuzhou, Navidrome, Plex, Jellyfin, and a curated radio directory. Ships with a spectrum visualizer, 10-band parametric EQ, Lua plugin system, IPC remote control, and MPRIS/MediaRemote integration.

- Site: https://cliamp.stream
- Install: `curl -fsSL https://cliamp.stream/install.sh | sh`
- Entry point: `main.go` → `run(...)` wires providers, player, playlist, Lua plugin manager, IPC server, and the Bubbletea program.
- CLI built with `urfave/cli/v3` in `commands.go`.

---

## Architecture map

Top-level layout (each subdirectory below is a Go package):

| Path | Responsibility |
|------|----------------|
| `main.go`, `commands.go` | Entry point, CLI definition, subcommands (IPC clients: play/pause/next/seek/…) |
| `config/` | TOML config load/save, CLI overrides, provider-specific config blocks |
| `player/` | Audio engine. Decoding (FFmpeg, yt-dlp), DSP pipeline, 10-band EQ, ICY, gapless, platform audio devices (`audio_device_*.go`) |
| `playlist/` | Playlist model, shuffle/repeat, M3U/PLS encoding, tag reading, queue navigation |
| `provider/` | `interfaces.go` + `types.go`: the `Provider` contract every source implements |
| `external/<name>/` | One Go package per provider: `local`, `radio`, `navidrome`, `plex`, `jellyfin`, `spotify`, `ytmusic` |
| `resolve/` | Expand user arguments (files, dirs, M3U/PLS, URLs, search queries) into playable tracks |
| `ui/` | Bubbletea view layer: visualizers (`vis_*.go`), global styles, tick loop |
| `ui/model/` | The Bubbletea `Model`: state, update, keymap, overlays, file browser, search, EQ, seek, notifications, providers. This is the biggest directory — always start here for UI behavior |
| `luaplugin/` | Gopher-Lua VM wrapper + sandbox + plugin APIs (`api_player.go`, `api_fs.go`, `api_http.go`, `api_message.go`, `api_crypto.go`, …). Plugin visualizers live here too |
| `plugins/` | First-party bundled Lua plugins (`now-playing.lua`, `auto-eq.lua`, …) |
| `pluginmgr/` | `cliamp plugins install/remove/list` CLI: resolves GitHub/GitLab/Codeberg sources and a direct URL |
| `ipc/` | Unix-socket IPC: `server.go` listens; `client.go` + `protocol.go` drive subcommands like `cliamp pause` from outside the TUI. `attach.go` hands a connection over to the session host |
| `session/` | Detached mode (`--daemon`): `host.go` is the virtual terminal the TUI renders into with no client attached, `client.go` is `cliamp attach`, `protocol.go` the frame stream between them |
| `mediactl/` | MPRIS (Linux, dbus) + NowPlaying (macOS) integration. `service_linux.go`, `service_darwin.go`, `service_stub.go` for other OSes |
| `lyrics/` | LRC parsing / fetching |
| `theme/` | Theme loading, default theme, `themes/` subfolder |
| `internal/` | Shared helpers not meant for import by plugins: `appdir`, `appmeta` (version), `browser`, `control`, `fileutil`, `httpclient`, `playback` (message types like `NextMsg`, `PrevMsg`), `resume`, `sshurl`, `tomlutil` |
| `cmd/` | Subcommand implementations that the CLI wires up (e.g. playlist subcommands) |
| `upgrade/` | Self-update logic for `cliamp upgrade` |
| `site/` | Static website for cliamp.stream — **keep synced with `docs/` on user-facing changes** |
| `docs/` | User-facing docs. Each feature has its own `.md`. Source of truth for keybindings, providers, plugin API |

### Runtime flow (read this before touching `main.go`)

1. `main()` builds the CLI (`buildApp()` in `commands.go`) and dispatches — most subcommands are thin IPC clients.
2. `run(overrides, positional)` is the TUI entry path:
   - Load config → apply CLI overrides.
   - Instantiate providers conditionally (radio + local always; navidrome/plex/jellyfin/spotify/ytmusic when configured).
   - Resolve positional args into tracks via `resolve/`.
   - Construct the `player.Player` (platform-specific audio device picked at compile time via build tags).
   - Build the Bubbletea `model.Model` with the player, playlist, providers, themes, and the Lua plugin manager.
   - Wire Lua state/control/UI providers (so plugins can read state and post `prog.Send(...)` messages).
   - Start the IPC server on a Unix socket.
   - Hand off to the media-control service (dbus / NowPlaying) which owns the event loop.

### Provider contract

All providers implement the interfaces in `provider/interfaces.go` (Browse, Tracks, Search where relevant). When adding a provider, add a package under `external/<name>/`, then register it in `main.go` behind a config check. Follow existing providers (Navidrome / Plex / Jellyfin) as templates — they share a lot of shape.

### Plugin surface

Lua plugins run in isolated `gopher-lua` VMs. Crashes are sandboxed. Hooks fire on playback events; plugins can register custom visualizers. When you add or change a plugin API, update **all three** of:
1. `luaplugin/api_*.go` (implementation + tests)
2. `docs/plugins.md` (user-facing reference)
3. `site/index.html` (the plugin API grid — see feedback memory)

---

## Build, test, and local workflow

```sh
make build        # go build -trimpath with version ldflags → ./cliamp
make test         # go test ./...
make vet          # go vet ./...
make lint         # vet + staticcheck (if installed)
make fmt          # gofmt -l -w .
make check        # fmt + vet + test
make install      # installs binary into ~/.local/bin
```

- Go version: **1.26** (see `go.mod`; toolchain pinned via `mise.toml`).
- Linux build needs `libasound2-dev` / `alsa-lib` at build time.
- For audio at runtime on PipeWire or PulseAudio, install `pipewire-alsa` or `pulseaudio-alsa` (see memory `project_alsa_audio_troubleshooting.md`).
- Optional runtime deps: `ffmpeg` (AAC/ALAC/Opus/WMA), `yt-dlp` (YT/SC/Bandcamp/Bilibili).

Tests are colocated with sources (`*_test.go`). Favor table-driven tests — the codebase already uses them heavily in `player/`, `playlist/`, `config/`, `ui/model/`, and `luaplugin/`.

Config lives at `~/.config/cliamp/config.toml` (example at `config.toml.example`); plugins at `~/.config/cliamp/plugins/`; custom radios at `~/.config/cliamp/radios.toml`; themes at `~/.config/cliamp/themes/`.

---

## Conventions to follow

- **Package naming:** lowercase, single-word, matches directory. No internal suffix gymnastics — use `internal/` for genuinely private helpers.
- **Error handling:** wrap with `fmt.Errorf("context: %w", err)`. Surface user-facing messages from `main.go` / `run(...)` only.
- **Build tags:** platform-specific audio and media-control files use `*_linux.go` / `*_darwin.go` / `*_stub.go` suffixes — follow the existing pattern, don't invent new conditional-compile styles.
- **Bubbletea messages:** put shared message types in `internal/playback/` so UI code and non-UI callers (Lua, IPC) can both send them via `prog.Send(...)`.
- **Keep `docs/` and `site/index.html` in sync** on any user-visible change (keybindings, plugin APIs, providers, config keys). This is recorded as user feedback — the automation depends on it.
- **Don't add emojis** to code or docs unless the user asks for them.
- **Minimal diffs:** prefer editing in place over rewriting files. No speculative abstractions.

---

## Skills to use

When working in this repo, prefer these skills over ad-hoc approaches:

- **`/golang`** — Best practices for production Go (error handling, concurrency, naming, testing patterns). Use for any Go code you write, review, or refactor here. Pair with `everything-claude-code:golang-patterns` and `everything-claude-code:golang-testing` for deeper pattern work.
- **`/simplify`** — Review changed code for reuse, quality, and efficiency, then fix what it finds. Run after non-trivial edits in `player/`, `ui/model/`, or `luaplugin/` — those packages accumulate complexity fastest.
- **Refactoring** — For dead-code cleanup and consolidation, dispatch the `everything-claude-code:refactor-cleaner` agent. For broader architectural restructuring, use `everything-claude-code:architect` first to plan, then execute with narrow edits. Always run `make check` after a refactor — gofmt, vet, and tests all need to pass before you stop.
- **`/go-review`** — For comprehensive idiomatic Go review (concurrency safety, error handling, security) before landing larger changes.
- **`/docs`** — When touching an external library (Bubbletea, Beep, go-librespot, urfave/cli, gopher-lua), look up current docs via Context7 rather than relying on training data.

Golden path for a non-trivial change:
1. Read relevant `docs/*.md` + skim the target package.
2. Plan (optionally via `everything-claude-code:plan`).
3. Implement the narrowest change that works. Add/extend table-driven tests.
4. Run `make check`.
5. Invoke `/simplify` on the diff.
6. If user-visible: update both `docs/` and `site/index.html`.

---

## Where to look first

| Question | Start here |
|----------|-----------|
| "How does the player decode X?" | `player/decode.go`, `player/ffmpeg.go`, `player/ytdl.go` |
| "How does the EQ work?" | `player/eq.go` + `player/eq_test.go` |
| "How is the UI laid out?" | `ui/model/model.go`, `ui/model/view.go`, `ui/styles.go` |
| "How do keybindings work?" | `ui/model/keymap.go`, `ui/model/keys*.go`, user-facing `docs/keybindings.md` |
| "How do I add a provider?" | `provider/interfaces.go` → copy `external/navidrome/` as a template |
| "How does IPC work?" | `ipc/protocol.go` (request/response types), `ipc/server.go`, `ipc/client.go` |
| "How do `--daemon` and `cliamp attach` work?" | `session/host.go`, `session/client.go`, the detached branch in `main.go` `run(...)`, `docs/headless.md` |
| "How are Lua plugins sandboxed?" | `luaplugin/sandbox.go`, `luaplugin/luaplugin.go` |
| "Where are bundled plugins?" | `plugins/` (first-party) |
| "What visualizers exist?" | `ui/vis_*.go` |
| "How is configuration resolved?" | `config/config.go` (load), `config/flags.go` (CLI overrides), `config/saver.go` (save) |
| "Why does my audio break silently on Linux?" | See memory `project_alsa_audio_troubleshooting.md` and README troubleshooting section |

---

## Things to know about the maintainer's preferences

- Keep responses and commits terse; no trailing "here's what I did" summaries unless asked.
- Bundled PRs for refactors in one area are preferred over many small ones (per feedback memory).
- User-facing changes must update `docs/` *and* `site/index.html` in the same change.
- Avoid adding new top-level dependencies casually — the dependency list in `go.mod` is intentional.
