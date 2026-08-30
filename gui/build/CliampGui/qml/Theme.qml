pragma Singleton
pragma ComponentBehavior: Bound

import QtQuick

QtObject {
    property string paletteName: "phosphor"
    property var systemColors: ({})
    property bool systemDark: true

    // Built-in palettes. Values are the design's exact tokens: bg/panel/line/
    // fg/dim/accent/accent2 map 1:1 onto the CSS custom properties of the
    // mockup. paletteName picks the dark one; paper is the light counterpart.
    readonly property var palettes: ({
        "phosphor": { "bg": "#0b0e0c", "panel": "#0f1310", "line": "#1d2a1b", "fg": "#cfe8c8", "dim": "#5c7355", "accent": "#6ee787", "accent2": "#ffb454", "danger": "#ff5c70" },
        "amber": { "bg": "#0d0a06", "panel": "#141009", "line": "#2c2113", "fg": "#f0d8a8", "dim": "#7a6237", "accent": "#ffb454", "accent2": "#ff8a5c", "danger": "#ff6b73" },
        "ice": { "bg": "#080b0e", "panel": "#0e1318", "line": "#1b2833", "fg": "#cfe3f0", "dim": "#55707f", "accent": "#6ec1ff", "accent2": "#a78bfa", "danger": "#ff6475" },
        "magenta": { "bg": "#0d080c", "panel": "#150e13", "line": "#2b1826", "fg": "#f0cfe4", "dim": "#7d5570", "accent": "#ff6ec7", "accent2": "#ffd166", "danger": "#ff667a" },
        "paper": { "bg": "#f4f1e8", "panel": "#fffdf6", "line": "#b8b3a7", "fg": "#181a1f", "dim": "#555b65", "accent": "#3655b3", "accent2": "#0b6f75", "danger": "#a52835" }
    })

    readonly property bool hasSystemColors: systemColors.background !== undefined
                                            && systemColors.foreground !== undefined
    readonly property bool desktopDark: hasSystemColors && systemColors.mode !== undefined
                                        ? systemColors.mode !== "light" : systemDark
    // The built-in palette every desktop key falls back to. A colors.toml that
    // is partial, or caught half-written while the desktop theme switches, then
    // still yields a complete set of tokens.
    readonly property var builtinPalette: systemDark ? palettes[paletteName] : palettes.paper
    readonly property var desktopPalette: ({
        "bg": systemColors.background || builtinPalette.bg,
        "panel": (desktopDark
                  ? (systemColors.dark_background || systemColors.darker_background || systemColors.background)
                  : (systemColors.lighter_background || systemColors.background)) || builtinPalette.panel,
        "line": systemColors.muted || systemColors.dark_foreground || systemColors.foreground
                || builtinPalette.line,
        "fg": systemColors.foreground || builtinPalette.fg,
        "dim": systemColors.dark_foreground || systemColors.muted || systemColors.foreground
               || builtinPalette.dim,
        "accent": systemColors.accent || systemColors.green || systemColors.foreground
                  || builtinPalette.accent,
        "accent2": systemColors.cyan || systemColors.magenta || systemColors.accent
                   || systemColors.foreground || builtinPalette.accent2,
        "danger": systemColors.red || systemColors.accent || systemColors.foreground
                  || builtinPalette.danger
    })
    // The desktop's own colours win where they exist; otherwise fall back to the
    // built-in palette matching the system light/dark preference.
    readonly property var current: hasSystemColors ? desktopPalette : builtinPalette

    // Built-in palettes are used verbatim: they are deliberate design tokens.
    // A desktop palette is arbitrary user colour and gets clamped to a legible
    // contrast ratio against the background before it reaches the UI.
    readonly property color bg: current.bg
    readonly property color panel: current.panel
    readonly property color line: current.line
    readonly property color fg: hasSystemColors ? ensureContrast(current.fg, bg, 7.0) : asColor(current.fg)
    readonly property color dim: hasSystemColors ? ensureContrast(current.dim, bg, 4.5) : asColor(current.dim)
    readonly property color accent: hasSystemColors ? ensureContrast(current.accent, bg, 4.5) : asColor(current.accent)
    readonly property color accent2: hasSystemColors ? ensureContrast(current.accent2, bg, 4.5) : asColor(current.accent2)
    readonly property color danger: hasSystemColors ? ensureContrast(current.danger, bg, 4.5) : asColor(current.danger)

    // White overlays lifted straight from the mockup. Keeping them as tokens
    // means a row highlight stays identical across every palette.
    readonly property color rowHover: Qt.rgba(1, 1, 1, 0.039)      // #ffffff0a
    readonly property color rowSelected: Qt.rgba(1, 1, 1, 0.071)   // #ffffff12
    readonly property color surfaceRaised: Qt.rgba(1, 1, 1, 0.051) // #ffffff0d
    readonly property color rowDivider: Qt.rgba(1, 1, 1, 0.024)    // #ffffff06
    readonly property color hairline: Qt.rgba(1, 1, 1, 0.094)      // #ffffff18
    readonly property color inset: Qt.rgba(1, 1, 1, 0.031)         // #ffffff08

    // The play button's tinted fill: the accent at the mockup's 0x1a / 0x2e.
    readonly property color accentFill: Qt.rgba(accent.r, accent.g, accent.b, 0.102)
    readonly property color accentFillHover: Qt.rgba(accent.r, accent.g, accent.b, 0.180)
    readonly property color selection: Qt.rgba(accent.r, accent.g, accent.b, 0.14)
    readonly property color hover: rowHover

    readonly property string mono: "JetBrains Mono, JetBrainsMono Nerd Font, monospace"

    function asColor(value) {
        return Qt.darker(value, 1.0)
    }

    // Qt.darker returns null for anything that is not a parseable colour.
    function isColor(value) {
        const color = Qt.darker(value, 1.0)
        return color !== null && color !== undefined
    }

    function channelLuminance(channel) {
        return channel <= 0.04045 ? channel / 12.92 : Math.pow((channel + 0.055) / 1.055, 2.4)
    }

    function luminance(value) {
        const color = asColor(value)
        return 0.2126 * channelLuminance(color.r)
                + 0.7152 * channelLuminance(color.g)
                + 0.0722 * channelLuminance(color.b)
    }

    function contrast(first, second) {
        const firstLuminance = luminance(first)
        const secondLuminance = luminance(second)
        return (Math.max(firstLuminance, secondLuminance) + 0.05)
                / (Math.min(firstLuminance, secondLuminance) + 0.05)
    }

    function mix(first, second, amount) {
        const left = asColor(first)
        const right = asColor(second)
        return Qt.rgba(left.r + (right.r - left.r) * amount,
                       left.g + (right.g - left.g) * amount,
                       left.b + (right.b - left.b) * amount,
                       left.a + (right.a - left.a) * amount)
    }

    function ensureContrast(value, background, minimum) {
        // Nothing to measure against: hand the value back and let the colour
        // property coerce it, rather than throwing inside a binding.
        if (!isColor(value) || !isColor(background))
            return value
        if (contrast(value, background) >= minimum)
            return value

        const target = contrast("#ffffff", background) >= contrast("#000000", background)
                       ? "#ffffff" : "#000000"
        for (let step = 1; step <= 20; ++step) {
            const candidate = mix(value, target, step / 20)
            if (contrast(candidate, background) >= minimum)
                return candidate
        }
        return target
    }
}
