pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

Button {
    id: root

    property bool accentActive: false
    property bool compact: false

    implicitWidth: compact ? 30 : 42
    implicitHeight: 26
    font.family: Theme.mono
    font.pixelSize: 10
    font.bold: accentActive
    focusPolicy: Qt.StrongFocus

    contentItem: Text {
        text: root.text
        color: root.enabled ? (root.accentActive ? Theme.accent : Theme.fg) : Theme.dim
        font: root.font
        horizontalAlignment: Text.AlignHCenter
        verticalAlignment: Text.AlignVCenter
        elide: Text.ElideRight
    }

    background: Rectangle {
        color: root.down ? Qt.rgba(Theme.accent.r, Theme.accent.g, Theme.accent.b, 0.18)
                         : root.hovered ? Theme.hover : "transparent"
        border.width: root.activeFocus ? 1 : 0
        border.color: Theme.accent
        radius: 2
    }
}
