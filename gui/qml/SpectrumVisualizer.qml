pragma ComponentBehavior: Bound

import QtQuick

Item {
    id: root

    property var bands: []
    property var peaks: Array(10).fill(0)
    property int bandCount: Math.max(10, bands.length)
    property int segmentHeight: 3
    property int segmentGap: 1
    readonly property int rows: Math.max(5, Math.floor(height / (segmentHeight + segmentGap)))

    Timer {
        interval: 50
        running: root.visible
        repeat: true
        onTriggered: {
            const next = root.peaks.slice()
            let changed = false
            for (let i = 0; i < next.length; ++i) {
                const value = Math.max(0, Math.min(1, root.bands[i] || 0))
                const peak = value > next[i] ? value : Math.max(0, next[i] - 0.02)
                if (peak !== next[i]) {
                    next[i] = peak
                    changed = true
                }
            }
            if (changed)
                root.peaks = next
        }
    }

    Row {
        id: bars
        anchors.fill: parent
        spacing: 3

        Repeater {
            model: root.bandCount

            Item {
                id: band
                required property int index
                width: (bars.width - bars.spacing * (root.bandCount - 1)) / root.bandCount
                height: parent.height
                readonly property real level: Math.max(0, Math.min(1, root.bands[band.index] || 0))

                Repeater {
                    model: root.rows

                    Rectangle {
                        required property int index
                        width: parent.width
                        height: root.segmentHeight
                        y: parent.height - (index + 1) * (root.segmentHeight + root.segmentGap) + root.segmentGap
                        visible: index < Math.round(band.level * root.rows)
                        color: index < root.rows * 0.55 ? Theme.accent
                              : index < root.rows * 0.82 ? Theme.accent2 : "#ff5c70"
                    }
                }

                Rectangle {
                    width: parent.width
                    height: root.segmentHeight
                    visible: (root.peaks[band.index] || 0) > 0
                    y: parent.height - Math.max(1, Math.round((root.peaks[band.index] || 0) * root.rows))
                       * (root.segmentHeight + root.segmentGap) + root.segmentGap
                    color: Theme.fg
                }
            }
        }
    }
}
