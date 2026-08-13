# cliamp GUI

Qt 6 desktop frontend for cliamp's existing Go audio daemon.

The GUI preserves the compact, retro player design from `~/Documents/Cliamp GUI.html` while using cliamp's Unix-socket IPC for playback, queue management, spectrum data, EQ, provider browsing, radio, and library tracks.

## Build

Requirements: Qt 6.8 or newer with Quick and Network, CMake 3.21 or newer, and a `cliamp` executable in `PATH`.

```sh
cmake -S gui -B gui/build -DCMAKE_BUILD_TYPE=Release
cmake --build gui/build --parallel
./gui/build/cliamp-gui
```

Set `CLIAMP_BIN=/path/to/cliamp` when the daemon executable is not in `PATH`.

## Runtime

The GUI attaches to `~/.config/cliamp/cliamp.sock` (honoring `CLIAMP_CONFIG_DIR` and `XDG_CONFIG_HOME`). Select `START` if no daemon is running. The GUI only terminates a daemon process it started itself.

Keyboard controls:

- `Space`: play or pause
- `Left` and `Right`: seek five seconds
- `E`: equalizer
- `L`: library
- `R`: radio

The current daemon API intentionally does not expose interactive provider setup/authentication, plugin hosting, or all local playlist maintenance actions. Configure providers through cliamp first; the GUI then discovers configured providers through IPC.
