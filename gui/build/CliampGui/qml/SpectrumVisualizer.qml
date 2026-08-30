pragma ComponentBehavior: Bound

import QtQuick

// Spectrum strip with several render modes, named after their TUI counterparts
// in ui/vis_*.go so the two front-ends stay recognisable to each other.
//
// The daemon publishes 10 smoothed bands at ~30 Hz, so every mode here is
// band-driven; the TUI's waveform and stereo modes have no data to work from
// over IPC and are deliberately absent.
//
// Cheap modes draw with scene-graph rectangles, which cost almost nothing on a
// player that runs for hours. The painterly modes need a Canvas, so it is only
// created while one of them is showing.
Item {
    id: root

    property var bands: []
    property int barCount: 34
    property bool active: true
    property int minBarHeight: 3
    property int mode: 0

    readonly property var modeNames: [
        "Bars", "Mirror", "BarsDot", "ClassicPeak", "ClassicLED", "Bricks",
        "Columns", "BarsOutline", "Scatter", "Pulse", "Terrain", "Flame"
    ]
    readonly property string modeName: modeNames[Math.max(0, Math.min(modeNames.length - 1, mode))]

    readonly property bool isMirror: modeName === "Mirror"
    readonly property bool isOutline: modeName === "BarsOutline"
    readonly property bool hasPeaks: modeName === "ClassicPeak" || modeName === "ClassicLED"
    readonly property bool isBallistic: modeName === "ClassicPeak"
    readonly property bool zoned: modeName === "ClassicLED"
    // Horizontal gaps cut the bars into segments, the way a terminal cell grid does.
    readonly property bool segmented: modeName === "BarsDot" || modeName === "Bricks"
                                      || modeName === "ClassicPeak" || modeName === "ClassicLED"
    // A second, vertical cut turns those segments into a dot matrix.
    readonly property bool stippled: modeName === "BarsDot"
    readonly property bool painterly: modeName === "Scatter" || modeName === "Pulse"
                                      || modeName === "Terrain" || modeName === "Flame"

    // Columns are deliberately thin and dense, interpolated between bands.
    readonly property int renderBars: modeName === "Columns" ? Math.max(barCount, Math.round(width / 4))
                                                             : barCount

    // The mockup's envelope, used by most modes.
    readonly property real attack: 0.55
    readonly property real decay: 0.12

    // ClassicPeak's physics, ported from ui/vis_classic_peak.go.
    readonly property real barRiseRate: 34.0
    readonly property real barFallRate: 10.0
    readonly property real launchBase: 0.8
    readonly property real launchGain: 1.4
    readonly property real launchMax: 1.7
    readonly property real gravity: 9.5
    readonly property real apexHold: 0.08
    readonly property real visibleEpsilon: 0.01
    // ClassicLED holds its cap, then slides it down at a constant rate.
    readonly property real ledRiseRate: 60.0
    readonly property real ledPeakHold: 0.45
    readonly property real ledPeakFall: 0.55

    property var levels: []
    property var peaks: []
    property var peakVelocity: []
    property var peakHold: []
    // Overall energy, used by the radial and landscape modes.
    property real energy: 0

    function cycleMode() {
        mode = (mode + 1) % modeNames.length
    }

    onRenderBarsChanged: resetState()
    onModeChanged: resetState()
    Component.onCompleted: resetState()

    function resetState() {
        const n = Math.max(1, renderBars)
        levels = Array(n).fill(0)
        peaks = Array(n).fill(0)
        peakVelocity = Array(n).fill(0)
        peakHold = Array(n).fill(0)
    }

    // sampleBand reads the level for a bar, interpolating between the 10
    // published bands so a wider strip stays continuous instead of stepping.
    function sampleBand(bar) {
        const source = root.bands
        if (!source || source.length === 0)
            return 0
        if (source.length === 1)
            return Math.max(0, Math.min(1, source[0]))

        const position = bar / Math.max(1, root.renderBars - 1) * (source.length - 1)
        const lower = Math.floor(position)
        const upper = Math.min(source.length - 1, lower + 1)
        const blend = position - lower
        const value = (source[lower] || 0) * (1 - blend) + (source[upper] || 0) * blend
        return Math.max(0, Math.min(1, value))
    }

    // levelColor paints valleys, slopes and peaks apart, the way the TUI's
    // spectrum colouring does.
    function levelColor(level, index) {
        if (root.zoned)
            return level > 0.8 ? Theme.danger : level > 0.55 ? Theme.accent2 : Theme.accent
        return index % 8 === 7 ? Theme.accent2 : Theme.accent
    }

    Timer {
        interval: 16
        running: root.visible
        repeat: true

        readonly property real dt: 0.016
        // The daemon publishes bands at ~30 Hz, so repainting the canvas on
        // every 60 Hz frame is wasted work. Bars stay on the fast path because
        // the scene graph redraws them for free; the canvas steps every other
        // frame, which halves its cost with no visible difference.
        property bool canvasFrame: false

        onTriggered: {
            canvasFrame = !canvasFrame
            if (root.levels.length !== root.renderBars) {
                root.resetState()
                return
            }

            const nextLevels = root.levels.slice()
            const ballistic = root.isBallistic
            const led = root.zoned
            let changed = false
            let sum = 0

            for (let i = 0; i < nextLevels.length; ++i) {
                const target = root.active ? root.sampleBand(i) : 0
                const current = nextLevels[i]
                let value
                if (ballistic) {
                    const rate = target > current ? root.barRiseRate : root.barFallRate
                    value = current + (target - current) * (1 - Math.exp(-rate * dt))
                } else if (led) {
                    const rate = target > current ? root.ledRiseRate : root.barFallRate
                    value = current + (target - current) * (1 - Math.exp(-rate * dt))
                } else {
                    value = current + (target - current) * (target > current ? root.attack : root.decay)
                }
                if (Math.abs(value - current) > 0.0005) {
                    nextLevels[i] = value
                    changed = true
                }
                sum += nextLevels[i]
            }

            const nextEnergy = sum / Math.max(1, nextLevels.length)
            if (Math.abs(nextEnergy - root.energy) > 0.0005) {
                root.energy = nextEnergy
                changed = true
            }

            if (root.hasPeaks) {
                const nextPeaks = root.peaks.slice()
                const nextVelocity = root.peakVelocity.slice()
                const nextHold = root.peakHold.slice()

                for (let j = 0; j < nextPeaks.length; ++j) {
                    const bar = nextLevels[j]

                    if (ballistic) {
                        // A cap resting on its bar relaunches when the bar overtakes it.
                        if (nextVelocity[j] === 0 && nextPeaks[j] <= bar + root.visibleEpsilon
                                && bar > nextPeaks[j]) {
                            const delta = bar - nextPeaks[j]
                            nextPeaks[j] = bar
                            nextVelocity[j] = Math.min(root.launchMax,
                                                       root.launchBase + root.launchGain * delta)
                            changed = true
                            continue
                        }
                        if (nextHold[j] > 0) {
                            nextHold[j] = Math.max(0, nextHold[j] - dt)
                            continue
                        }
                        const previousVelocity = nextVelocity[j]
                        nextPeaks[j] += nextVelocity[j] * dt
                        nextVelocity[j] -= root.gravity * dt
                        if (nextPeaks[j] > 1)
                            nextPeaks[j] = 1
                        // Pause briefly at the apex before falling back.
                        if (previousVelocity > 0 && nextVelocity[j] <= 0
                                && nextPeaks[j] > bar + root.visibleEpsilon) {
                            nextVelocity[j] = 0
                            nextHold[j] = root.apexHold
                            changed = true
                            continue
                        }
                        if (nextPeaks[j] <= bar) {
                            nextPeaks[j] = bar
                            nextVelocity[j] = 0
                        }
                    } else {
                        // ClassicLED: hold the cap, then slide it down.
                        if (bar >= nextPeaks[j]) {
                            nextPeaks[j] = bar
                            nextHold[j] = root.ledPeakHold
                        } else if (nextHold[j] > 0) {
                            nextHold[j] = Math.max(0, nextHold[j] - dt)
                        } else {
                            nextPeaks[j] = Math.max(bar, nextPeaks[j] - root.ledPeakFall * dt)
                        }
                    }
                    changed = true
                }

                if (changed) {
                    root.peaks = nextPeaks
                    root.peakVelocity = nextVelocity
                    root.peakHold = nextHold
                }
            }

            if (changed) {
                root.levels = nextLevels
                if (root.painterly && root.painter && canvasFrame)
                    root.painter.step(dt * 2)
            }
        }
    }

    readonly property real barPitch: (width + (modeName === "Columns" ? 1 : 2)) / Math.max(1, renderBars)
    readonly property real barWidth: Math.max(1, barPitch - (modeName === "Columns" ? 1 : 2))

    // Mirror hangs its bars off a persistent centre axis.
    Rectangle {
        visible: root.isMirror
        anchors.left: parent.left
        anchors.right: parent.right
        y: Math.round(root.height / 2)
        height: 1
        color: Theme.accent
        opacity: 0.45
    }

    Repeater {
        model: root.painterly ? 0 : root.renderBars

        Rectangle {
            id: bar
            required property int index

            readonly property real level: root.levels[index] || 0
            readonly property real span: Math.max(root.minBarHeight,
                                                  root.minBarHeight + level * (root.height - root.minBarHeight))

            x: index * root.barPitch
            width: root.barWidth
            // BarsOutline keeps only the top edge, giving a line-graph read.
            height: root.isOutline ? 2
                    : root.isMirror ? Math.max(1, level * root.height) : span
            y: root.isOutline ? root.height - span
               : root.isMirror ? (root.height - height) / 2 : root.height - height
            opacity: 0.85
            color: root.levelColor(level, index)
        }
    }

    // Falling peak caps for the two classic modes.
    Repeater {
        model: root.hasPeaks && !root.painterly ? root.renderBars : 0

        Rectangle {
            id: cap
            required property int index

            readonly property real peak: root.peaks[index] || 0
            x: index * root.barPitch
            width: root.barWidth
            height: 2
            y: Math.max(0, root.height - root.minBarHeight
                        - peak * (root.height - root.minBarHeight) - height)
            color: Theme.fg
        }
    }

    // Segment gaps, painted in the background colour over the bars.
    Repeater {
        model: root.segmented ? Math.ceil(root.height / 5) : 0

        Rectangle {
            required property int index
            anchors.left: parent.left
            anchors.right: parent.right
            y: root.height - (index + 1) * 5
            height: 2
            color: Theme.bg
        }
    }
    Repeater {
        model: root.stippled ? root.renderBars : 0

        Rectangle {
            required property int index
            x: index * root.barPitch + root.barWidth / 2 - 0.5
            width: 1
            height: root.height
            color: Theme.bg
        }
    }

    // ----- Painterly modes -------------------------------------------------

    Loader {
        anchors.fill: parent
        active: root.painterly
        sourceComponent: painterComponent
        onLoaded: root.painter = item
    }
    property var painter: null

    Component {
        id: painterComponent

        Canvas {
            id: canvas
            renderStrategy: Canvas.Cooperative

            // Terrain scrolls a history of energy; Flame keeps a heat field.
            property var history: []
            property var heat: []
            property int cell: 4
            property int cols: Math.max(1, Math.floor(width / cell))
            property int rows: Math.max(1, Math.floor(height / cell))

            onColsChanged: reset()
            onRowsChanged: reset()
            Component.onCompleted: reset()

            function reset() {
                history = Array(cols).fill(0)
                heat = Array(cols * rows).fill(0)
            }

            // step advances whichever simulation the active mode needs, then
            // asks for a repaint. Called from the shared frame timer so the
            // canvas never runs faster than the rest of the strip.
            function step(dt) {
                if (root.modeName === "Terrain") {
                    const next = history.slice(1)
                    next.push(root.energy)
                    history = next
                } else if (root.modeName === "Flame") {
                    advanceFlame()
                }
                requestPaint()
            }

            // advanceFlame is the classic doom-fire propagation the TUI uses:
            // the bottom row is fed from the spectrum, then every cell inherits
            // its neighbour-below's heat with lateral jitter and a random decay.
            function advanceFlame() {
                const next = heat.slice()
                for (let x = 0; x < cols; ++x) {
                    const band = root.sampleBand(Math.floor(x / cols * root.renderBars))
                    next[(rows - 1) * cols + x] = Math.min(1, band * (0.7 + Math.random() * 0.5))
                }
                for (let y = 0; y < rows - 1; ++y) {
                    for (let x = 0; x < cols; ++x) {
                        const drift = Math.round(Math.random() * 2) - 1
                        const src = Math.max(0, Math.min(cols - 1, x + drift))
                        const below = next[(y + 1) * cols + src]
                        next[y * cols + x] = Math.max(0, below - Math.random() * 0.18)
                    }
                }
                heat = next
            }

            onPaint: {
                const ctx = getContext("2d")
                ctx.reset()
                switch (root.modeName) {
                case "Scatter":
                    paintScatter(ctx)
                    break
                case "Pulse":
                    paintPulse(ctx)
                    break
                case "Terrain":
                    paintTerrain(ctx)
                    break
                case "Flame":
                    paintFlame(ctx)
                    break
                }
            }

            // Twinkling particle field: density rises with the square of the
            // band energy, biased toward the bottom as if under gravity.
            function paintScatter(ctx) {
                const columns = root.renderBars
                const pitch = width / columns
                for (let i = 0; i < columns; ++i) {
                    const level = root.levels[i] || 0
                    const count = Math.round(level * level * rows * 2)
                    ctx.fillStyle = root.levelColor(level, i)
                    for (let d = 0; d < count; ++d) {
                        const bias = Math.random() * Math.random()
                        const y = height - bias * level * height
                        const x = i * pitch + Math.random() * pitch
                        ctx.fillRect(Math.round(x), Math.round(y), 2, 2)
                    }
                }
            }

            // Concentric rings breathing with the overall energy.
            function paintPulse(ctx) {
                const cx = width / 2
                const cy = height / 2
                const maxRadius = Math.min(width, height) / 2
                const rings = 5
                for (let r = 0; r < rings; ++r) {
                    const phase = (r / rings + root.energy) % 1
                    const radius = phase * maxRadius * 2
                    if (radius <= 0)
                        continue
                    ctx.beginPath()
                    ctx.ellipse(cx - radius * 2, cy - radius, radius * 4, radius * 2)
                    ctx.strokeStyle = r % 2 === 0 ? Theme.accent : Theme.accent2
                    ctx.globalAlpha = Math.max(0, 1 - phase) * (0.25 + root.energy)
                    ctx.lineWidth = 1.5
                    ctx.stroke()
                }
                ctx.globalAlpha = 1
            }

            // A side-view landscape: new energy enters at the right and the
            // range scrolls left, painted valleys to peaks.
            function paintTerrain(ctx) {
                if (history.length < 2)
                    return
                const step = width / (history.length - 1)
                ctx.beginPath()
                ctx.moveTo(0, height)
                for (let i = 0; i < history.length; ++i) {
                    ctx.lineTo(i * step, height - history[i] * height)
                }
                ctx.lineTo(width, height)
                ctx.closePath()
                ctx.fillStyle = Theme.accent
                ctx.globalAlpha = 0.35
                ctx.fill()
                ctx.globalAlpha = 1

                ctx.beginPath()
                for (let j = 0; j < history.length; ++j) {
                    const y = height - history[j] * height
                    j === 0 ? ctx.moveTo(0, y) : ctx.lineTo(j * step, y)
                }
                ctx.strokeStyle = root.energy > 0.7 ? Theme.danger
                                  : root.energy > 0.45 ? Theme.accent2 : Theme.accent
                ctx.lineWidth = 1.5
                ctx.stroke()
            }

            function paintFlame(ctx) {
                for (let y = 0; y < rows; ++y) {
                    for (let x = 0; x < cols; ++x) {
                        const value = heat[y * cols + x]
                        if (value <= 0.05)
                            continue
                        ctx.fillStyle = value > 0.66 ? Theme.danger
                                        : value > 0.33 ? Theme.accent2 : Theme.accent
                        ctx.globalAlpha = Math.min(1, value + 0.15)
                        ctx.fillRect(x * cell, y * cell, cell, cell)
                    }
                }
                ctx.globalAlpha = 1
            }
        }
    }
}
