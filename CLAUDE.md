# CLAUDE.md — cliamp

> A retro terminal music player (Go + Bubbletea). This file tells AI agents where things live, what conventions the codebase uses, and which skills to lean on.

## Extended context

**More narrative detail, design notes, and roadmap context for cliamp lives in `~/Documents/bjarne/projects/cliamp/`.** Read files in that directory when you need background that isn't captured in code or in `docs/`. Treat it as the project's long-form knowledge base (goals, decisions, TODOs). If a question feels strategic rather than tactical, check there first.

`AGENTS.md` is a symlink to this file. Edit `CLAUDE.md` only.

---

## What cliamp is

A TUI music player inspired by Winamp. It plays local files, `ssh://` paths, HTTP streams, HLS, podcasts and internet radio. It also plays from these providers: Spotify, Qobuz, Tidal, YouTube, YouTube Music, SoundCloud, Mixcloud, NetEase Cloud Music, Yandex Music, Navidrome, Lyrion, Plex, Jellyfin, Emby and Audiobookshelf. yt-dlp adds Bandcamp and Bilibili URLs. `resolve/` plays Xiaoyuzhou episode URLs. cliamp has built-in visualizers, a 10-band parametric EQ, synced lyrics, Lua plugins, V2 IPC remote control and a detached mode (`--daemon`, `cliamp attach`). Media controls use MPRIS on Linux, NowPlaying on macOS and media-key hotkeys on Windows.

- Site: https://cliamp.stream
- Install: `curl -fsSL https://cliamp.stream/install.sh | sh`
- Module: `github.com/bjarneo/cliamp`. Package `main` sits at the repo root.
- Entry point: `main.go` → `run(...)`. The TUI and detached mode both start there.
- CLI: `urfave/cli/v3` in `commands.go`.

---

## Architecture map

Package `main` at the repo root:

| File | Responsibility |
|------|----------------|
| `main.go` | `run(...)` for the TUI and detached mode: config, player, playlist, Model, Bubbletea program, IPC server and exit save. It also holds the V2 dispatcher `newV2Dispatcher`, the operation set `v2Operations` and the plugin jobs |
| `providers.go` | `buildProviders` and the `providerKeys` table that `--provider` reads. The player hooks `registerPlayerHooks` and `isBufferedProviderURL`. The sign-in URL observers, the Jellyfin and Emby resume context and the yt-dlp install prompt |
| `lua_wiring.go` | The Lua state, control and UI providers. `newLuaSender` queues the plugin messages for `prog.Send`, so a plugin never waits on the event loop |
| `commands.go` | The root flags and every subcommand |
| `ipc_client.go` | The V2 client of the subcommands, `cliamp open` and `cliamp remote`. Each call waits a bounded time |
| `open.go` | `cliamp open` for `cliamp://` links. It maps each parsed link to a fixed set of operations |
| `main_darwin.go` | Locks the main goroutine to the main OS thread on macOS with cgo |

Each directory below is a Go package:

| Path | Responsibility |
|------|----------------|
| `config/` | Loads `config.toml` with a hand-written parser in `config.go`. CLI overrides are in `flags.go`. `config` is the only config writer: `save` and `SaveSection` in `config.go`, and the typed savers in `saver.go` |
| `player/` | The audio engine: decoding, FFmpeg and yt-dlp pipelines, the buffered provider pipeline, gapless, the 10-band EQ, volume, speed, ICY and the visualizer tap. `audio_device_*.go` holds the platform audio devices |
| `playlist/` | The `Track` model, the queue with shuffle, repeat and play-next, M3U and PLS, the track TOML format, and the base `playlist.Provider` interface in `provider.go` |
| `provider/` | The optional capability interfaces in `interfaces.go`, `Entry`, the `ProviderMeta` keys in `types.go`, and `AddTracks` |
| `external/<name>/` | One package per provider: `audiobookshelf`, `emby`, `jellyfin`, `local`, `lyrion`, `mixcloud`, `navidrome`, `netease`, `plex`, `podcast`, `qobuz`, `radio`, `soundcloud`, `spotify`, `tidal`, `yandex`, `ytmusic`. `radio` also serves the cliamp radio channels. `radiometa` is not a provider. It reads the now-playing data of stations that send no ICY metadata |
| `favorites/`, `history/` | The ♥ favorites and Recently Played stores in the config directory. `buildProviders` makes one of each and shares them with the local provider and the Model |
| `resolve/` | Turns the user arguments into tracks: files, directories, globs, URLs, M3U, PLS, feeds, yt-dlp playlists and Xiaoyuzhou |
| `tracksave/` | Saves a downloaded track in the download directory |
| `ui/` | The visualizer engine: `visualizer.go`, the drivers in `vis_driver.go`, the mode table in `vis_registry.go`, one `vis_*.go` file per mode, and the Lua modes in `vis_lua.go`. Also the shared styles, the frame padding and the tick intervals |
| `ui/model/` | The Bubbletea `Model`. See [ui/model by concern](#uimodel-by-concern). This is the biggest package. Start here for UI behavior |
| `luaplugin/` | The Gopher-Lua VMs, the sandbox, the per-plugin event queues, the plugin visualizers and the plugin APIs in `api_*.go` |
| `plugins/` | First-party example plugins. `luaplugin/regression_bundled_test.go` loads each one |
| `pluginmgr/` | `cliamp plugins install`, `remove`, `list` and `trust`. Resolves GitHub, GitLab, Codeberg and direct URL sources |
| `ipc/` | The V2 protocol over a Unix socket: the envelope, the operation registry, jobs and the event broker. `attach.go` hands a connection over to the session host. See [IPC](#ipc) |
| `session/` | Detached mode: `host.go` is the virtual terminal the TUI renders into with no client attached, `client.go` is `cliamp attach`, `protocol.go` the frame stream between them |
| `mediactl/` | Media controls: MPRIS over D-Bus on Linux, NowPlaying on macOS with cgo, media-key hotkeys on Windows, and `service_stub.go` for the other builds |
| `lyrics/` | The lyrics lookup: embedded lyrics, the provider sources, LRCLIB and NetEase |
| `theme/` | The theme loader and the built-in themes in `themes/` |
| `cmd/` | The bodies of the `setup`, `playlist`, `history`, `radio` and `protocol` subcommands |
| `applog/` | The log file `cliamp.log` in the config directory |
| `upgrade/` | Self-update for `cliamp upgrade` |
| `internal/` | Private helpers: `appdir` for the config, data and plugin directories, `appmeta` for the version, `authurl` for sign-in URLs, `browser`, `credstore` for provider credential files, `deeplink` for `cliamp://` parsing, `embyapi` for the shared Emby and Jellyfin client, `fileutil`, `fuzzy`, `globe` and `worldmap` for `cliamp radio --globe`, `httpclient` for the stream client and `NewAPI`, `netdiag`, `playback` for the shared messages and `Notifier`, `plugintrust`, `resume`, `sshurl`, `tomlutil` for `[[section]]` files, and `ytdlcookies` |

Other directories: `docs/` holds the user docs, one `.md` per feature. `site/` is the static website for cliamp.stream. **Keep it synced with `docs/` on user-facing changes.** `nix/package.nix` and `flake.nix` build the Nix package. `.github/workflows/` holds CI, release, AUR, Nix and Pages.

### Runtime flow (read this before touching `main.go`)

1. `main()` sets the version and runs `buildApp()` from `commands.go`. Most subcommands are thin V2 clients in `ipc_client.go`. They talk to the running instance over the socket.
2. `run(overrides, positional, daemon, visualizer60FPS)` is the path of the TUI and of detached mode:
   1. Load `config.toml`, apply the CLI overrides and open `cliamp.log`. With `--daemon` `checkNotRunning` ends the run when another instance serves the socket.
   2. Call `buildProviders` in `providers.go`. cliamp radio, Radio, Local and Podcasts always register. The other providers register when they are configured. YouTube also needs credentials and yt-dlp.
   3. Resolve the positional arguments with `resolve.Args`. Feeds, M3U, PLS and yt-dlp pages go to `Pending` and resolve after start.
   4. Build the start playlist from `--playlist`, the cliamp radio channels or a saved Jellyfin or Emby context.
   5. Open the `player.Player` in `newPlayer`. `registerPlayerHooks` adds the stream factories, source resolvers and URL matchers of the providers.
   6. Create the IPC event broker and load the Lua plugins with `luaplugin.New`.
   7. Build the Model with `model.New` and `config.SaveFunc{}`. `configureModel` applies the settings. With `--daemon`, `run` creates a `session.Host` and calls `SetDetached(true)` and `SetSessionDetach(host.Detach)`.
   8. Create the Bubbletea program with `programOptions`, plus `sessionProgramOptions` with `--daemon`. In both modes `quitOnSignals` turns SIGINT, SIGTERM and SIGHUP into `playback.QuitMsg`, so the exit save runs.
   9. Attach the sign-in URL observers, the media controls in `wireMediaCtl`, and the Lua control and UI providers from `lua_wiring.go`.
   10. Start the IPC server in `startIPC`. With `--daemon` a failed bind ends the run, and the host takes the `attach` requests.
   11. `mediactl.Run` runs the program until it quits. `saveOnExit` then saves the theme and the resume position.

### Detached mode

`cliamp --daemon` or `cliamp -d` runs the TUI against a `session.Host`, a virtual terminal, instead of the terminal it started from. `cliamp attach` lends a real terminal to it over the socket; `q` detaches, and `cliamp quit` (or a signal) ends the session. While no client is attached, `SetDetachedMsg` makes `View` return nothing, hides the visualizer and drops the tick to `ui.TickDetached`. `spectrum.get` then analyzes on demand. It differs from the TUI in these ways:

- It never offers to install yt-dlp.
- With the cliamp provider and no arguments it starts with the live channel streams, as `--auto-play` does.
- The `q` and `Ctrl+C` keys detach instead of quitting. `playback.QuitMsg` from signals and media controls still shuts down.

`docs/headless.md` is the user guide.

### IPC

The socket is `cliamp.sock` in the config directory. `ipc.DefaultSocketPath` returns the path. The protocol is V2 only: one JSON object per line with `"version": 2`. The server answers any other request with `invalid_version`. The operations live in these places:

| Place | What it owns |
|-------|--------------|
| `ipc/v2.go`, `ipc/server.go` | The envelope, `capabilities`, `job.get`, `job.cancel`, `subscribe`, and the direct reads `state.get` and `spectrum.get` |
| `ipc/operations.go` | `DefaultOperationRegistry`: the name, the parameters and the parameter checks of each operation |
| `ipc/jobs.go`, `ipc/pubsub.go` | The job store and the event broker |
| `main.go` | `newV2Dispatcher` sends each job to the Model as `model.V2RequestMsg`, and runs `plugin.call` and `plugin.commands` against `luaplugin`. `v2Operations` picks the operations of the runtime |
| `ui/model/ipc_runtime.go` | Playback, queue, play-next, settings, the runtime snapshot and the runtime events |
| `ui/model/ipc_extended.go` | The provider, playlist, library, `url.load`, `save`, `lyrics` and `history` operations |
| `ipc_client.go`, `commands.go`, `open.go` | The CLI side |

To add an operation:

1. Add it to `DefaultOperationRegistry` in `ipc/operations.go`.
2. Put new parameter fields on `ipc.Request` in `ipc/protocol.go` when the existing fields do not fit.
3. Handle it in `handleV2Request` in `ui/model/ipc_runtime.go`, or in `ui/model/ipc_extended.go`. `handleV2Request` sends only `provider.*` and `playlist.*` names to `ipc_extended.go` by itself. For another name, add it to the case that calls `handleV2DeferredRequest`, and add a case to the switch in `handleV2DeferredRequest`. `handleV2Request` fails an unknown name with `unavailable`.
4. For a queue edit, add the name to `v2MutatesLivePlaylist` in `ui/model/ipc_runtime.go`. The revision check then covers it.
5. When the runtime cannot serve the operation, unregister it in `v2Operations` in `main.go`.
6. Add a CLI command in `commands.go` when users need one.
7. Add table-driven tests in `ui/model/`, and in `ipc/` for a protocol change.
8. Document it in `docs/remote-control.md`.

### ui/model by concern

| Concern | Files |
|---------|-------|
| State and setup | `model.go` holds the `Model` struct, the screens, the focus areas and the `ConfigSaver` seam. `state.go` groups the sub-structs. `init.go` holds `New`, the setters and `Init`. `detached.go` holds `SetDetached`, `SetSessionDetach` and `SetDetachedMsg` |
| Update loop | `update.go` is one type switch. After each message it lays out the frame, drops a stale preload, tells the media controls, emits the plugin events and publishes the IPC and plugin state. `update_load.go`, `update_nav.go`, `update_playback.go`, `update_provider.go` and `update_search.go` hold the message bodies. `tick.go` runs the frame tick. `commands.go` holds the `tea.Cmd` constructors and their messages |
| Actions | `actions.go` holds one verb for each user intent that more than one entry point starts: keys, media controls, Lua and IPC. `queue_ops.go` holds the one queue edit rule for keys, IPC and Lua. `playback.go`, `playback_state.go`, `preload.go`, `seek.go` and `audio.go` run playback, the gapless preload, seek, EQ and speed. `eq_presets.go` holds the built-in EQ presets. `ytdl_batch.go` loads a long YouTube playlist in batches |
| Reports | `notifications.go` updates the media controls. At track start it records history and sends the now-playing report. When a track that played past half its length is left, it sends the scrobble. `report_queue.go` keeps the provider reports in order. `favorites.go` copies a ♥ favorite to the service |
| Keys | `keys.go` holds `handleKey` and the global keys. `keys_*.go` hold the keys of each screen. `command_registry.go` lists every key with its mode. It feeds the help bar, the `Ctrl+K` keymap in `keymap.go` and `ReservedKeys` for plugins |
| Overlays | `overlays_table.go` holds `overlayStack`. The first open overlay in it renders, gets the keys and gets pasted text. `overlays.go`, `inline_overlays.go`, `inline_overlays_nav.go`, `filebrowser.go`, `pl_picker.go`, `plmgr_append.go`, `lyrics.go`, `metadata.go`, `jump.go` and the `*_subs.go` files implement the overlays. `filter_list.go` holds the cursor, the scroll and the `/` filter of a list overlay. `search.go` filters the playlist for the `/` search |
| View | `view.go`, `view_columns.go`, `view_helpers.go`, `view_nav.go`, `view_overlays.go`, `layout.go`, `scroll.go`, `playlist_rows.go`, `styles.go` and `title.go`. `textinput.go` edits the inline text inputs and draws their cursor |
| Providers | `providers.go` switches providers, loads the provider pane and maps the `Shift+letter` shortcuts. `keys_radio.go` handles the catalog, the favorites and the location consent. `nav_labels.go` names the browse levels |
| IPC | `ipc_runtime.go` and `ipc_extended.go`. See [IPC](#ipc) |
| Plugins | `plugin_events.go` emits the Lua events after each Update. `plugin_state.go` publishes the `PluginState` snapshot that Lua reads. `plugin_queue.go` applies `PluginQueueMsg` |

Overlays are rows in `overlayStack`, not a mode stack. `Update` dispatches with a type switch, not a message-handler interface.

### Provider contract

Every provider implements `playlist.Provider` in `playlist/provider.go`. The optional capabilities are interfaces in `provider/interfaces.go`. The UI finds them with type assertions. Register a provider in `buildProviders` and `providerKeys` in `providers.go`. `docs/provider-development.md` lists every file that a new provider touches. Use Navidrome, Plex or Jellyfin as a template.

### Plugin surface

Lua plugins run in isolated `gopher-lua` VMs. A crash stays in its plugin. Each plugin runs its events and key presses in order from its own queue, and each callback has a timeout. Plugins read the `PluginState` snapshot that the Model publishes. Their controls reach the Model as messages through `lua_wiring.go`. Plugins can register visualizers. When you add or change a plugin API, update **all three** of:
1. `luaplugin/api_*.go` and its tests.
2. `docs/plugins.md`, the user-facing reference.
3. `site/index.html`, the plugin API grid. `luaplugin/hooks_site_test.go` checks the event count on the site.

---

## Build, test, and local workflow

`mise.toml` pins Go 1.26.6 and shellcheck 0.10.0. Run the targets through mise to use the pins, for example `mise exec -- make ci`.

| Target | What it does |
|--------|--------------|
| `make build` | `go build -trimpath` with the version ldflags into `./cliamp` |
| `make test` | `go test ./...` |
| `make vet` | `go vet ./...` |
| `make lint` | `vet`, then `staticcheck` when it is installed |
| `make staticcheck` | `staticcheck ./...`. It fails when staticcheck is missing |
| `make tools` | Installs the pinned `staticcheck` and `govulncheck` |
| `make fmt` | `gofmt -l -w` on the Go files that git tracks or would track |
| `make fmt-check` | Fails when a Go file that git tracks or would track needs `gofmt` |
| `make tidy-check` | `go mod tidy -diff`. Fails when `go.mod` or `go.sum` is not tidy |
| `make coverage` | Tests with a coverage profile and a summary for each function |
| `make security` | `govulncheck ./...` |
| `make ci` | What CI runs: `fmt-check`, `tidy-check`, `vet`, `staticcheck`, `security`, race tests with coverage, `shellcheck site/install.sh` and `git diff --exit-code` |
| `make check` | `fmt`, `vet` and `test` |
| `make install` | Builds and installs the binary into `~/.local/bin` |
| `make clean` | Removes the binary |

Keep these versions in step:

- The `go` line in `go.mod`, the `go` pin in `mise.toml`, and `STATICCHECK_VERSION` in the `Makefile`. CI reads the Go version from `go.mod`.
- The `shellcheck` pin in `mise.toml` and `SHELLCHECK_VERSION` in `.github/workflows/ci.yml`.
- `GOVULNCHECK_VERSION` in the `Makefile` sets the govulncheck that `make tools` installs.

On Linux the build needs the ALSA, FLAC, Vorbis, Ogg and mpg123 headers: `libasound2-dev libflac-dev libvorbis-dev libogg-dev libmpg123-dev` on Debian and Ubuntu. For audio on PipeWire or PulseAudio, install `pipewire-alsa` or `pulseaudio-alsa`. The README troubleshooting section has the details. Optional runtime tools are `ffmpeg` for AAC, ALAC, Opus and WMA, and `yt-dlp` for YouTube, YouTube Music, SoundCloud, Mixcloud, Bandcamp, Bilibili and NetEase.

Tests are colocated with sources (`*_test.go`). Favor table-driven tests — the codebase already uses them heavily in `player/`, `playlist/`, `config/`, `ui/model/`, and `luaplugin/`. Tests read `site/index.html` to check the counts of visualizers, EQ presets, themes and plugin events. `config/config_golden_test.go` loads every setting of `config.toml.example`. Other tests check the lists and defaults that `docs/*.md` and `config.toml.example` name, and the anchor links of `docs/*.md` and `README.md`. A docs edit can therefore fail `make test`.

Config lives at `~/.config/cliamp/config.toml` (example at `config.toml.example`); plugins at `~/.config/cliamp/plugins/`; custom radios at `~/.config/cliamp/radios.toml`; themes at `~/.config/cliamp/themes/`. The IPC socket is `~/.config/cliamp/cliamp.sock`, and the log is `~/.config/cliamp/cliamp.log`. Set HOME and every `XDG_*` directory to a temp directory when you run a cliamp binary. Build the binary first in the normal environment, for example with `mise exec -- make build`. Do not run `mise exec` or `go` under the isolated HOME. mise then loses its installed tools, and go loses its build cache and module cache. `CLIAMP_CONFIG_DIR` and `XDG_CONFIG_HOME` move only the config directory, which holds the socket and the log. The data directory `~/.local/share/cliamp`, the `~/Music/cliamp` save directory and `~` in local music paths still follow HOME.

---

## Conventions to follow

- **Package naming:** lowercase, single-word, matches directory. No internal suffix gymnastics — use `internal/` for genuinely private helpers.
- **Error handling:** wrap with `fmt.Errorf("context: %w", err)`. Surface user-facing messages from `main.go` / `run(...)` only.
- **Build tags:** a platform file uses the GOOS suffix `_linux.go`, `_darwin.go` or `_windows.go`. `player/audio_device_macos.go` is the one exception, with `//go:build darwin && !ios`. The file for the other platforms uses `_stub.go`, `_other.go` or `_unix.go`, with a `//go:build` line such as `!windows`. Follow these patterns. Do not invent new conditional-compile styles.
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
| How does the player decode X? | `player/decode.go`, `player/ffmpeg.go`, `player/ytdl.go`, `player/pipeline.go` |
| How does the EQ work? | `player/eq.go` and `player/eq_test.go`. The presets are in `ui/model/eq_presets.go` |
| How is the UI laid out? | `ui/model/layout.go`, `ui/model/view.go`, `ui/model/view_columns.go`, `ui/styles.go` |
| How do keybindings work? | `ui/model/command_registry.go`, `ui/model/keys.go`, `ui/model/keys_*.go`, `ui/model/keymap.go`, and the user-facing `docs/keybindings.md` |
| Which overlay gets a key? | `ui/model/overlays_table.go` |
| Where does a key, IPC or Lua action land? | `ui/model/actions.go` and `ui/model/queue_ops.go` |
| How do I add a provider? | `docs/provider-development.md`, `provider/interfaces.go`, `providers.go` |
| How does IPC work? | `ipc/v2.go`, `ipc/operations.go`, `ipc/server.go`, `ui/model/ipc_runtime.go`, `docs/remote-control.md` |
| How do `--daemon` and `cliamp attach` work? | `session/host.go`, `session/client.go`, the `daemon` branch of `run` in `main.go`, `ui/model/detached.go`, and `docs/headless.md` |
| How do Lua plugins reach the Model? | `lua_wiring.go`, `ui/model/plugin_state.go`, `ui/model/plugin_events.go` |
| How are Lua plugins sandboxed? | `luaplugin/sandbox.go`, the write rules in `luaplugin/api_fs.go`, the queues and timeouts in `luaplugin/hooks.go`, and `internal/plugintrust/` |
| Where are bundled plugins? | `plugins/` |
| What visualizers exist? | `ui/vis_registry.go`, `ui/vis_*.go`, `ui/vis_driver.go` |
| How is configuration resolved and saved? | `config/config.go` for `Load`, `save` and `SaveSection`. `config/flags.go` for the CLI overrides. `config/saver.go` for the typed savers |
| Why does my audio break silently on Linux? | The README troubleshooting section |

---

## Things to know about the maintainer's preferences

- Keep responses and commits terse; no trailing "here's what I did" summaries unless asked.
- Bundled PRs for refactors in one area are preferred over many small ones (per feedback memory).
- User-facing changes must update `docs/` *and* `site/index.html` in the same change.
- Avoid adding new top-level dependencies casually — the dependency list in `go.mod` is intentional.

## Commits

Every commit must have a description body, not just a subject line.
Subject: `area: what changed` (50 chars or less, e.g. `fix(ui): ...`). Body: what was wrong,
what the fix does, and how it was verified (`make check`: fmt, vet, tests).
