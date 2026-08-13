pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

Item {
    id: root
    required property var backend

    ListView {
        id: list
        anchors.fill: parent
        clip: true
        model: root.backend.queueModel
        reuseItems: true
        boundsBehavior: Flickable.StopAtBounds
        ScrollBar.vertical: ScrollBar {}

        delegate: Item {
            id: row
            required property int index
            required property string title
            required property string artist
            required property int duration_secs

            width: list.width
            height: 28
            readonly property bool current: index === root.backend.currentIndex

            Rectangle {
                anchors.fill: parent
                color: row.current ? Theme.selection : mouse.containsMouse ? Theme.hover : "transparent"
            }
            Rectangle {
                width: 2
                height: parent.height
                visible: row.current
                color: Theme.accent
            }
            Text {
                id: number
                anchors.left: parent.left
                anchors.leftMargin: 10
                anchors.verticalCenter: parent.verticalCenter
                width: 28
                text: (row.index + 1).toString().padStart(2, "0")
                color: row.current ? Theme.accent : Theme.dim
                font.family: Theme.mono
                font.pixelSize: 10
            }
            Text {
                id: trackTitle
                anchors.left: number.right
                anchors.right: trackArtist.left
                anchors.rightMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                text: row.title || qsTr("Unknown title")
                elide: Text.ElideRight
                color: row.current ? Theme.accent : Theme.fg
                font.family: Theme.mono
                font.pixelSize: 11
            }
            Text {
                id: trackArtist
                anchors.right: trackDuration.left
                anchors.rightMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                width: 150
                horizontalAlignment: Text.AlignRight
                text: row.artist
                elide: Text.ElideRight
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 10
            }
            Text {
                id: trackDuration
                anchors.right: parent.right
                anchors.rightMargin: 10
                anchors.verticalCenter: parent.verticalCenter
                width: 42
                horizontalAlignment: Text.AlignRight
                text: root.backend.formatDuration(row.duration_secs)
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 10
            }
            MouseArea {
                id: mouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: root.backend.playQueue(row.index)
            }
        }

        Text {
            anchors.centerIn: parent
            visible: list.count === 0
            text: root.backend.connected ? qsTr("Queue is empty") : qsTr("Start the cliamp daemon to load a queue")
            color: Theme.dim
            font.family: Theme.mono
            font.pixelSize: 11
        }
    }
}
