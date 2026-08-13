pragma Singleton
pragma ComponentBehavior: Bound

import QtQuick

QtObject {
    property string paletteName: "phosphor"

    readonly property var palettes: ({
        "phosphor": { "bg": "#0b0e0c", "panel": "#0f1310", "line": "#1d2a1b", "fg": "#cfe8c8", "dim": "#5c7355", "accent": "#6ee787", "accent2": "#ffb454" },
        "amber": { "bg": "#0d0a06", "panel": "#141009", "line": "#2c2113", "fg": "#f0d8a8", "dim": "#7a6237", "accent": "#ffb454", "accent2": "#ff8a5c" },
        "ice": { "bg": "#080b0e", "panel": "#0e1318", "line": "#1b2833", "fg": "#cfe3f0", "dim": "#55707f", "accent": "#6ec1ff", "accent2": "#a78bfa" },
        "magenta": { "bg": "#0d080c", "panel": "#150e13", "line": "#2b1826", "fg": "#f0cfe4", "dim": "#7d5570", "accent": "#ff6ec7", "accent2": "#ffd166" }
    })

    readonly property var current: palettes[paletteName]
    readonly property color bg: current.bg
    readonly property color panel: current.panel
    readonly property color line: current.line
    readonly property color fg: current.fg
    readonly property color dim: current.dim
    readonly property color accent: current.accent
    readonly property color accent2: current.accent2
    readonly property color selection: Qt.rgba(accent.r, accent.g, accent.b, 0.14)
    readonly property color hover: "#ffffff0d"
    readonly property string mono: "JetBrains Mono, JetBrainsMono Nerd Font, monospace"

    function setPalette(name) {
        if (palettes[name] !== undefined)
            paletteName = name
    }
}
