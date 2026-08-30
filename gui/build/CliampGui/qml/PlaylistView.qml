pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

// The play queue. One flat row per track: index, title, artist, duration.
// The playing row takes the accent colour and a lighter background.
Item {
    id: root
    required property var backend

    // The artist column is secondary information: drop it before the title has
    // to start eliding.
    readonly property bool showArtist: width >= 520

    ListView {
        id: list
        anchors.fill: parent
        clip: true
        model: root.backend.queueModel
        reuseItems: true
        boundsBehavior: Flickable.StopAtBounds
        activeFocusOnTab: true
        keyNavigationWraps: false
        ScrollBar.vertical: ScrollBar {}
        Accessible.role: Accessible.List
        Accessible.name: qsTr("Playback queue")

        function playSelected(event) {
            if (currentIndex < 0)
                return
            root.backend.playQueue(currentIndex)
            event.accepted = true
        }
        Keys.onReturnPressed: event => playSelected(event)
        Keys.onEnterPressed: event => playSelected(event)

        delegate: Item {
            id: row
            required property int index
            required property string title
            required property string artist
            required property int duration_secs

            width: list.width
            height: 28
            readonly property bool current: index === root.backend.currentIndex
            readonly property color rowColor: current ? Theme.accent : Theme.fg
            readonly property color metaColor: current ? Theme.accent : Theme.dim
            Accessible.role: Accessible.ListItem
            Accessible.name: qsTr("%1 by %2")
                .arg(row.title || qsTr("Unknown title"))
                .arg(row.artist || qsTr("Unknown artist"))
            Accessible.onPressAction: play()

            function play() {
                list.currentIndex = index
                list.forceActiveFocus()
                root.backend.playQueue(index)
            }

            Rectangle {
                anchors.fill: parent
                color: row.current ? Theme.rowSelected
                                   : mouse.containsMouse ? Theme.rowHover : "transparent"
                border.width: list.activeFocus && list.currentIndex === row.index ? 1 : 0
                border.color: Theme.accent
            }
            Rectangle {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.bottom: parent.bottom
                height: 1
                color: Theme.rowDivider
            }
            Text {
                id: number
                anchors.left: parent.left
                anchors.leftMargin: 14
                anchors.verticalCenter: parent.verticalCenter
                width: 20
                text: (row.index + 1).toString().padStart(2, "0")
                color: row.metaColor
                font.family: Theme.mono
                font.pixelSize: 11
            }
            Text {
                anchors.left: number.right
                anchors.leftMargin: 10
                anchors.right: trackArtist.left
                anchors.rightMargin: 10
                anchors.verticalCenter: parent.verticalCenter
                text: row.title || qsTr("Unknown title")
                elide: Text.ElideRight
                color: row.rowColor
                font.family: Theme.mono
                font.pixelSize: 11
            }
            Text {
                id: trackArtist
                visible: root.showArtist
                anchors.right: trackDuration.left
                anchors.rightMargin: root.showArtist ? 10 : 0
                anchors.verticalCenter: parent.verticalCenter
                width: root.showArtist ? 160 : 0
                text: row.artist
                elide: Text.ElideRight
                color: row.metaColor
                font.family: Theme.mono
                font.pixelSize: 11
            }
            Text {
                id: trackDuration
                anchors.right: parent.right
                anchors.rightMargin: 14
                anchors.verticalCenter: parent.verticalCenter
                text: root.backend.formatDuration(row.duration_secs)
                color: row.metaColor
                font.family: Theme.mono
                font.pixelSize: 11
            }
            MouseArea {
                id: mouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: row.play()
            }
        }

        Text {
            anchors.centerIn: parent
            visible: list.count === 0
            text: root.backend.connected ? qsTr("Queue is empty")
                                         : qsTr("Start the cliamp daemon to load a queue")
            color: Theme.dim
            font.family: Theme.mono
            font.pixelSize: 11
        }
    }
}
