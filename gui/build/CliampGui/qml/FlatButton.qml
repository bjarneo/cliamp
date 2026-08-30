pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

// The mockup's only button shape: a 1px bordered box on the panel colour.
// Every variant (transport keys, SHUF/REP, EQ presets, provider chips, TEST &
// SAVE) is this component with different colours and padding, so the hover and
// focus behaviour stays identical everywhere.
Button {
    id: root

    property color borderColor: Theme.line
    property color hoverBorderColor: borderColor
    property color contentColor: Theme.fg
    property color hoverContentColor: contentColor
    property color fillColor: Theme.panel
    property color hoverFillColor: fillColor
    property real fontSize: 10
    property real letterSpacing: 0

    // horizontalPadding, leftPadding and implicitWidth come from Control: a
    // chip only has to say how much breathing room its label needs.
    horizontalPadding: 8
    implicitHeight: 26
    focusPolicy: Qt.StrongFocus

    readonly property bool lit: hovered || down

    contentItem: Text {
        id: label
        text: root.text
        color: !root.enabled ? Theme.dim
                             : root.lit ? root.hoverContentColor : root.contentColor
        font.family: Theme.mono
        font.pixelSize: root.fontSize
        font.letterSpacing: root.letterSpacing
        horizontalAlignment: Text.AlignHCenter
        verticalAlignment: Text.AlignVCenter
        elide: Text.ElideRight
    }

    background: Rectangle {
        color: root.lit ? root.hoverFillColor : root.fillColor
        border.width: 1
        border.color: root.activeFocus ? Theme.accent
                                       : root.lit ? root.hoverBorderColor : root.borderColor
    }
}
