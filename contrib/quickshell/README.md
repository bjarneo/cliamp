# cliamp quickshell widget

A compact "now playing" card for [Quickshell](https://quickshell.org) (300 x 72), centered along the bottom of every screen and driven by cliamp's MPRIS service (`org.mpris.MediaPlayer2.cliamp`). Two-row layout: title + time on top, artist + transport on the second row, a 10-band Winamp 2-style spectrum below, and a thin click-to-seek line at the bottom. Colors are picked up from the active Omarchy theme (`~/.config/omarchy/current/theme/colors.toml`) and update live when the theme changes. Hides itself when cliamp is not running. The card accepts mouse input for playback controls and seeking, but never takes keyboard focus from the active application.

Linux only. Requires Quickshell 0.2+ and cliamp running with its default MPRIS service enabled (it is by default on Linux).

## Quick start

Run it directly without installing:

```sh
qs -p contrib/quickshell/shell.qml
```

Or install as a named Quickshell config:

```sh
mkdir -p ~/.config/quickshell
ln -s "$PWD/contrib/quickshell" ~/.config/quickshell/cliamp
qs -c cliamp
```

Then start cliamp in another terminal. The card appears on every screen, anchored to the bottom edge.

## Customization

Theme colors come from Omarchy's active theme file:

```
~/.config/omarchy/current/theme/colors.toml
```

Mapping into the widget:

| Omarchy key | Widget role |
| --- | --- |
| `background` | card background |
| `foreground` | primary text (title) |
| `accent` | progress fill, seek handle, play/pause icon |
| `color2` | visualizer bottom LED rows (green slot), play/pause hover |
| `color3` | visualizer mid LED rows + peak caps, prev/next hover (yellow slot) |
| `color1` | visualizer top LED rows (red slot) |
| `selection_background` | card border (falls back to `color8`) |
| `color8` | secondary text + prev/next icons, time readout (falls back to a darker `foreground`) |

The card border uses `selection_background` (a muted dark gray in most Omarchy themes), falling back to `color8` if that key is missing. Switching theme rewrites `colors.toml` in place; the widget watches it and re-applies colors live (no reload needed).

To reposition the card, edit `shell.qml`. The defaults anchor a transparent full-width strip to the bottom of every screen and center a 300 x 72 card inside it with a 16 px gap from the edge:

```qml
PanelWindow {
    anchors { bottom: true; left: true; right: true }
    margins { bottom: 16 }
    implicitHeight: 72
    color: "transparent"

    NowPlaying {
        width: 300
        height: parent.height
        anchors.horizontalCenter: parent.horizontalCenter
    }
}
```

Swap `bottom` for `top` to flip to the top edge, or replace the `horizontalCenter` anchor with `anchors.left` / `anchors.right` to push it into a corner.

## Files

| File | Purpose |
| --- | --- |
| `shell.qml` | Entry point. Wires up a `PanelWindow` per screen and finds cliamp on the MPRIS bus. |
| `NowPlaying.qml` | The bar widget itself. |
| `TransportButton.qml` | Reusable prev/play/next button (hover + enabled state). |
| `MediaIcon.qml` | Resolution-independent transport icons drawn with Canvas2D (prev / play / pause / next / stop). |
| `Visualizer.qml` | Canvas-based ClassicPeak spectrum (bars + falling peak markers). |
| `BandStream.qml` | Wraps `cliamp visstream` and exposes the live 10-band frames as reactive properties. |

## Notes

- The widget polls `MprisPlayer.position` on a 250 ms timer while playing, since Quickshell's MPRIS service does not emit reactive updates for position drift.
- Player detection uses `dbusName === "cliamp"` (cliamp registers the well-known name `org.mpris.MediaPlayer2.cliamp` in `mediactl/service_linux.go`).
- Clicking the progress bar issues an MPRIS `SetPosition`, which cliamp handles via `playback.SetPositionMsg`.
- Theme colors come from the active Omarchy theme via a `FileView` watching `~/.config/omarchy/current/theme/colors.toml`. The TOML is parsed in QML with a small regex (no external script). Theme swaps update the card without reloading Quickshell.
- Spectrum bands stream over the cliamp IPC socket via `cliamp visstream`. One long-lived subprocess per widget.
- The widget renders a Winamp 2-style spectrum analyzer: each band is a stack of LED segments with a tiny gap between them, with a falling peak cap. Three-tone gradient across the height — `color2` (green) for the bottom rows, `color3` (yellow) for the middle, `color1` (red) for the top, mirroring the classic Winamp gradient (and the cliamp TUI). The bar block spans the full card width, edge to edge.
- All UI elements use sharp 90-degree corners (no `radius`) to match a terminal aesthetic.
