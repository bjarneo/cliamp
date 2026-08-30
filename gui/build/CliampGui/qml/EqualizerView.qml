pragma ComponentBehavior: Bound

import QtQuick

// 10-band parametric EQ, +/-12 dB. Preset chips load gain curves; dragging a
// band sets it directly. Each band is a 3px rail with a fill that grows up or
// down from the centre line plus a wide flat handle: deliberately mechanical,
// not a native slider.
Item {
    id: root
    required property var backend

    // The daemon's real band centres (player/eq.go), not the mockup's.
    readonly property var frequencies: ["70", "180", "320", "600", "1k", "3k", "6k", "12k", "14k", "16k"]
    // At the design's 244px panel this resolves to the mockup's 118px rail. The
    // 126px is the chrome around it (padding, preset row, value and frequency
    // labels) plus the mockup's slack under the bands, so that slack stays
    // constant and every extra pixel of panel goes to the rails.
    readonly property int railHeight: Math.max(70, Math.min(520, root.height - 126))

    // Presets come from the daemon, so the GUI offers exactly the set the TUI
    // has, in the same order, and picks up any that are added later.
    readonly property var presets: backend.eqPresets

    // Sixteen presets do not fit a 640px row, so the chips scroll sideways the
    // way the library's provider strip does.
    ListView {
        id: presetRow
        anchors.top: parent.top
        anchors.left: parent.left
        anchors.right: dbLabel.left
        anchors.topMargin: 14
        anchors.leftMargin: 14
        anchors.rightMargin: 10
        height: 22
        orientation: ListView.Horizontal
        spacing: 8
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        model: root.presets

        // Keep the active preset in view when it is set from elsewhere.
        readonly property int selectedIndex: root.presets.indexOf(root.backend.eqPreset)
        onSelectedIndexChanged: if (selectedIndex >= 0) positionViewAtIndex(selectedIndex, ListView.Contain)

        delegate: FlatButton {
            id: chip
            required property string modelData
            readonly property bool selected: root.backend.eqPreset.toLowerCase() === modelData.toLowerCase()
            text: modelData.toUpperCase()
            fontSize: 9
            letterSpacing: 9 * 0.1
            horizontalPadding: 9
            implicitHeight: 22
            enabled: root.backend.connected
            contentColor: selected ? Theme.accent : Theme.dim
            hoverContentColor: Theme.accent
            borderColor: selected ? Theme.accent : Theme.line
            fillColor: selected ? Theme.surfaceRaised : "transparent"
            hoverFillColor: Theme.surfaceRaised
            Accessible.name: qsTr("Apply %1 equalizer preset").arg(chip.modelData)
            onClicked: root.backend.setEqPreset(chip.modelData)
        }
    }

    Text {
        id: dbLabel
        anchors.right: parent.right
        anchors.rightMargin: 14
        anchors.verticalCenter: presetRow.verticalCenter
        text: qsTr("±12 dB")
        color: Theme.dim
        font.family: Theme.mono
        font.pixelSize: 9
    }

    Row {
        id: bandsRow
        anchors.top: presetRow.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.topMargin: 14
        anchors.leftMargin: 14
        anchors.rightMargin: 14
        spacing: 10

        Repeater {
            model: root.frequencies.length

            Item {
                id: band
                required property int index
                width: (bandsRow.width - bandsRow.spacing * (root.frequencies.length - 1))
                       / root.frequencies.length
                height: gainLabel.height + 6 + railZone.height + 6 + frequency.height
                activeFocusOnTab: true

                readonly property real gain: Number(root.backend.eqBands[band.index] || 0)
                // 0 at +12 dB, 1 at -12 dB: the fraction of the rail above the handle.
                readonly property real fraction: (12 - gain) / 24

                Accessible.role: Accessible.Slider
                Accessible.name: qsTr("%1 Hz equalizer band").arg(root.frequencies[band.index])
                Accessible.description: qsTr("%1 decibels").arg(gain.toFixed(0))
                Accessible.onIncreaseAction: adjustGain(1)
                Accessible.onDecreaseAction: adjustGain(-1)
                Keys.onUpPressed: event => {
                    adjustGain(1)
                    event.accepted = true
                }
                Keys.onDownPressed: event => {
                    adjustGain(-1)
                    event.accepted = true
                }

                function adjustGain(delta) {
                    root.backend.setEqBand(index, Math.max(-12, Math.min(12, gain + delta)))
                }

                Text {
                    id: gainLabel
                    anchors.top: parent.top
                    anchors.horizontalCenter: parent.horizontalCenter
                    text: (band.gain > 0 ? "+" : "") + band.gain.toFixed(0)
                    color: Theme.accent2
                    font.family: Theme.mono
                    font.pixelSize: 9
                }

                Item {
                    id: railZone
                    anchors.top: gainLabel.bottom
                    anchors.topMargin: 6
                    anchors.left: parent.left
                    anchors.right: parent.right
                    height: root.railHeight

                    Rectangle {
                        id: rail
                        anchors.horizontalCenter: parent.horizontalCenter
                        width: 3
                        height: parent.height
                        color: Theme.line

                        Rectangle {
                            anchors.left: parent.left
                            anchors.right: parent.right
                            anchors.leftMargin: -1
                            anchors.rightMargin: -1
                            y: parent.height / 2
                            height: 1
                            color: Theme.hairline
                        }
                        Rectangle {
                            anchors.left: parent.left
                            anchors.right: parent.right
                            y: band.gain >= 0 ? band.fraction * parent.height : parent.height / 2
                            height: Math.abs(parent.height / 2 - band.fraction * parent.height)
                            color: Theme.accent
                        }
                    }

                    Rectangle {
                        anchors.horizontalCenter: parent.horizontalCenter
                        y: band.fraction * parent.height - 3
                        width: 18
                        height: 6
                        color: Theme.fg
                        border.width: 1
                        border.color: band.activeFocus ? Theme.accent : Theme.bg
                    }

                    MouseArea {
                        anchors.fill: parent
                        cursorShape: Qt.SizeVerCursor
                        enabled: root.backend.connected

                        function setGain(pointerY) {
                            const value = Math.max(0, Math.min(1, pointerY / height))
                            root.backend.setEqBand(band.index, 12 - value * 24)
                        }
                        onPressed: mouse => {
                            band.forceActiveFocus()
                            setGain(mouse.y)
                        }
                        onPositionChanged: mouse => {
                            if (pressed)
                                setGain(mouse.y)
                        }
                    }
                }

                Text {
                    id: frequency
                    anchors.top: railZone.bottom
                    anchors.topMargin: 6
                    anchors.horizontalCenter: parent.horizontalCenter
                    text: root.frequencies[band.index]
                    color: Theme.dim
                    font.family: Theme.mono
                    font.pixelSize: 8
                }
            }
        }
    }
}
