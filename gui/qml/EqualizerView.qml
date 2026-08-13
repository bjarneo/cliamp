pragma ComponentBehavior: Bound

import QtQuick

Item {
    id: root
    required property var backend

    readonly property var frequencies: ["60", "170", "310", "600", "1k", "3k", "6k", "12k", "14k", "16k"]
    readonly property var presets: ["Flat", "Rock", "Jazz", "Vocal", "Bass"]

    Row {
        id: presetRow
        anchors.top: parent.top
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.topMargin: 12
        anchors.leftMargin: 12
        anchors.rightMargin: 12
        spacing: 4

        Text {
            width: 70
            height: 24
            verticalAlignment: Text.AlignVCenter
            text: qsTr("PRESET")
            color: Theme.dim
            font.family: Theme.mono
            font.pixelSize: 10
            font.letterSpacing: 0.12
        }
        Repeater {
            model: root.presets
            LedButton {
                required property string modelData
                text: modelData.toUpperCase()
                compact: true
                accentActive: root.backend.eqPreset.toLowerCase() === modelData.toLowerCase()
                Accessible.name: qsTr("Apply %1 equalizer preset").arg(modelData)
                onClicked: root.backend.setEqPreset(modelData)
            }
        }
        Item { width: 1; height: 1 }
        Text {
            width: 88
            height: 24
            verticalAlignment: Text.AlignVCenter
            horizontalAlignment: Text.AlignRight
            text: root.backend.eqPreset.toUpperCase()
            color: Theme.accent
            elide: Text.ElideLeft
            font.family: Theme.mono
            font.pixelSize: 10
        }
    }

    Row {
        anchors.top: presetRow.bottom
        anchors.bottom: parent.bottom
        anchors.horizontalCenter: parent.horizontalCenter
        anchors.topMargin: 8
        anchors.bottomMargin: 10
        spacing: 16

        Repeater {
            model: root.frequencies.length

            Item {
                id: eqBand
                required property int index
                width: 25
                height: parent.height
                readonly property real gain: Number(root.backend.eqBands[eqBand.index] || 0)
                readonly property real normalized: (gain + 12) / 24

                Text {
                    anchors.top: parent.top
                    anchors.horizontalCenter: parent.horizontalCenter
                    text: root.frequencies[eqBand.index]
                    color: Theme.dim
                    font.family: Theme.mono
                    font.pixelSize: 8
                }
                Text {
                    anchors.top: parent.top
                    anchors.topMargin: 14
                    anchors.horizontalCenter: parent.horizontalCenter
                    text: eqBand.gain >= 0 ? "+" + eqBand.gain.toFixed(0) : eqBand.gain.toFixed(0)
                    color: Theme.fg
                    font.family: Theme.mono
                    font.pixelSize: 8
                }
                Rectangle {
                    id: rail
                    anchors.top: parent.top
                    anchors.topMargin: 34
                    anchors.bottom: frequency.bottom
                    anchors.horizontalCenter: parent.horizontalCenter
                    width: 3
                    color: Theme.line
                }
                Rectangle {
                    anchors.horizontalCenter: rail.horizontalCenter
                    y: rail.y + rail.height * 0.5
                    width: 9
                    height: 1
                    color: Theme.dim
                }
                Rectangle {
                    id: handle
                    anchors.horizontalCenter: rail.horizontalCenter
                    width: 22
                    height: 6
                    y: rail.y + rail.height * (1 - eqBand.normalized) - height / 2
                    color: Theme.accent
                    border.color: Theme.fg
                    border.width: 1
                }
                Text {
                    id: frequency
                    anchors.bottom: parent.bottom
                    anchors.horizontalCenter: parent.horizontalCenter
                    text: qsTr("Hz")
                    color: Theme.dim
                    font.family: Theme.mono
                    font.pixelSize: 7
                }
                MouseArea {
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.top: rail.top
                    anchors.bottom: rail.bottom
                    cursorShape: Qt.SizeVerCursor
                    function setGain(y) {
                        const fraction = Math.max(0, Math.min(1, (y - rail.y) / rail.height))
                        root.backend.setEqBand(eqBand.index, 12 - fraction * 24)
                    }
                    onPressed: mouse => setGain(mouse.y)
                    onPositionChanged: mouse => {
                        if (pressed)
                            setGain(mouse.y)
                    }
                }
            }
        }
    }
}
