pragma ComponentBehavior: Bound

import QtQuick

// One small square per built-in palette. The active one gets a ring in its own
// accent; the rest get a hairline. Picking one pins that palette, overriding the
// desktop theme for the rest of the session.
Row {
    id: root

    property int swatchSize: 12

    spacing: 5

    Repeater {
        model: Theme.paletteOrder

        Item {
            id: swatch
            required property string modelData
            width: root.swatchSize
            height: root.swatchSize
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
