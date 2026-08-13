# Qt Desktop GUI

The optional Qt 6 GUI is a compact Winamp-style desktop client for cliamp's existing audio daemon. It renders playback, queue, spectrum, EQ, provider browsing, and radio while the Go daemon retains audio decoding and provider integration.

## Build

Install Qt 6.8 or newer with Quick and Network, plus CMake 3.21 or newer.

```sh
make gui-build
./gui/build/cliamp-gui
```

To build directly:

```sh
cmake -S gui -B gui/build -DCMAKE_BUILD_TYPE=Release
cmake --build gui/build --parallel
```

Set `CLIAMP_BIN=/path/to/cliamp` if `cliamp` is not available in `PATH`.

## Runtime

The GUI connects to `cliamp.sock` in the same config directory as cliamp. It honors `CLIAMP_CONFIG_DIR` and `XDG_CONFIG_HOME`.

- Select `START` to launch `cliamp --daemon` when no instance is running.
- The GUI only stops daemon processes it started itself.
- Existing daemon instances remain available to terminal commands and media controls.
- Provider setup and authentication remain in `cliamp setup` for now.

Keyboard controls: `Space` toggles playback, arrow keys seek, `E` opens equalizer, `L` opens library, and `R` opens radio.
