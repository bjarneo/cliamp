pragma ComponentBehavior: Bound

import QtQuick

// Window chrome: traffic lights, app name, the queue's source, and the palette
// swatches. The bar doubles as the drag handle, since the window is frameless.
Item {
    id: root

    required property string sourceLabel
    required property int trackCount

    signal closeRequested()
    signal minimizeRequested()
    signal maximizeRequested()

    implicitHeight: 31

    Rectangle {
        anchors.fill: parent
        color: Theme.panel
    }
    Rectangle {
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        height: 1
        color: Theme.line
    }

    Row {
        id: windowControls
        anchors.left: parent.left
        anchors.leftMargin: 12
        anchors.verticalCenter: parent.verticalCenter
        anchors.verticalCenterOffset: -0.5
        spacing: 6

        Repeater {
            model: [
                { "fill": "#3a2a2a", "stroke": "#52393a", "action": "close", "name": qsTr("Close") },
                { "fill": "#33301f", "stroke": "#4d4a2e", "action": "minimize", "name": qsTr("Minimize") },
                { "fill": "#21301f", "stroke": "#33492f", "action": "maximize", "name": qsTr("Maximize") }
            ]

            Rectangle {
                id: light
                required property var modelData
                width: 10
                height: 10
                radius: 5
                color: modelData.fill
                border.width: 1
                border.color: modelData.stroke
                Accessible.role: Accessible.Button
                Accessible.name: light.modelData.name
                Accessible.onPressAction: light.trigger()

                function trigger() {
                    switch (light.modelData.action) {
                    case "close":
                        root.closeRequested()
                        break
                    case "minimize":
                        root.minimizeRequested()
                        break
                    default:
                        root.maximizeRequested()
                        break
                    }
                }

                MouseArea {
                    anchors.fill: parent
                    anchors.margins: -2
                    hoverEnabled: true
                    cursorShape: Qt.PointingHandCursor
                    onClicked: light.trigger()
                    onEntered: light.opacity = 0.75
                    onExited: light.opacity = 1
                }
            }
        }
    }

    Text {
        id: appName
        anchors.left: windowControls.right
        anchors.leftMargin: 12
        anchors.verticalCenter: parent.verticalCenter
        anchors.verticalCenterOffset: -0.5
        text: qsTr("CLIAMP")
        color: Theme.dim
        font.family: Theme.mono
        font.pixelSize: 11
        font.letterSpacing: 11 * 0.16
    }

    Text {
        anchors.left: appName.right
        anchors.right: swatches.left
        anchors.leftMargin: 12
        anchors.rightMargin: 12
        anchors.verticalCenter: parent.verticalCenter
        anchors.verticalCenterOffset: -0.5
        horizontalAlignment: Text.AlignHCenter
        text: root.trackCount === 1 ? qsTr("%1 — 1 track").arg(root.sourceLabel)
                                    : qsTr("%1 — %2 tracks").arg(root.sourceLabel).arg(root.trackCount)
        color: Theme.dim
        elide: Text.ElideMiddle
        font.family: Theme.mono
        font.pixelSize: 11
    }

    Row {
        id: swatches
        anchors.right: parent.right
        anchors.rightMargin: 10
        anchors.verticalCenter: parent.verticalCenter
        anchors.verticalCenterOffset: -0.5
        spacing: 5

        Repeater {
            model: Theme.paletteOrder

            Item {
                id: swatch
                required property string modelData
                width: 12
                height: 12
                scale: swatchMouse.containsMouse ? 1.2 : 1
                readonly property bool active: !Theme.hasSystemColors && Theme.paletteName === modelData
                Accessible.role: Accessible.RadioButton
                Accessible.name: qsTr("%1 palette").arg(swatch.modelData)
                Accessible.checked: swatch.active
                Accessible.onPressAction: Theme.setPalette(swatch.modelData)

                Behavior on scale {
                    NumberAnimation { duration: 120 }
                }

                Rectangle {
                    anchors.fill: parent
                    radius: 2
                    color: Theme.palettes[swatch.modelData].accent
                    border.width: 1
                    border.color: swatch.active ? Theme.palettes[swatch.modelData].accent : Theme.hairline
                }

                MouseArea {
                    id: swatchMouse
                    anchors.fill: parent
                    hoverEnabled: true
                    cursorShape: Qt.PointingHandCursor
                    onClicked: Theme.setPalette(swatch.modelData)
                }
            }
        }
    }
}
