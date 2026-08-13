pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

Item {
    id: root
    required property var backend

    ListView {
        id: stations
        anchors.fill: parent
        clip: true
        model: root.backend.radioModel
        reuseItems: true
        ScrollBar.vertical: ScrollBar {}

        delegate: Item {
            id: stationRow
            required property string playlistId
            required property string name
            required property string section
            required property int track_count
            required property bool favorite
            width: stations.width
            height: 38

            Rectangle {
                anchors.fill: parent
                color: mouse.containsMouse ? Theme.hover : "transparent"
            }
            Text {
                id: signal
                anchors.left: parent.left
                anchors.leftMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                text: stationRow.favorite ? "*" : "~"
                color: stationRow.favorite ? Theme.accent2 : Theme.accent
                font.family: Theme.mono
                font.pixelSize: 17
            }
            Text {
                id: nameText
                anchors.left: signal.right
                anchors.leftMargin: 9
                anchors.right: metadata.left
                anchors.top: parent.top
                anchors.topMargin: 5
                text: stationRow.name
                color: Theme.fg
                elide: Text.ElideRight
                font.family: Theme.mono
                font.pixelSize: 11
            }
            Text {
                anchors.left: nameText.left
                anchors.right: metadata.left
                anchors.bottom: parent.bottom
                anchors.bottomMargin: 5
                text: stationRow.section || qsTr("STREAM")
                color: Theme.dim
                elide: Text.ElideRight
                font.family: Theme.mono
                font.pixelSize: 9
                font.letterSpacing: 0.08
            }
            Text {
                id: metadata
                anchors.right: parent.right
                anchors.rightMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                width: 96
                horizontalAlignment: Text.AlignRight
                text: stationRow.track_count > 0 ? qsTr("%1 tracks").arg(stationRow.track_count) : qsTr("live stream")
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 9
            }
            MouseArea {
                id: mouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: root.backend.loadRadioStation(stationRow.playlistId)
            }
        }

        Text {
            anchors.centerIn: parent
            visible: stations.count === 0
            text: qsTr("Loading stations...")
            color: Theme.dim
            font.family: Theme.mono
            font.pixelSize: 11
        }
    }
}
