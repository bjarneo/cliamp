pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

// Station directory. Row = signal glyph, station name, genre/section, and the
// track count or "live" for open-ended streams.
Item {
    id: root
    required property var backend

    readonly property bool showTags: width >= 520

    ListView {
        id: stations
        anchors.fill: parent
        clip: true
        model: root.backend.radioModel
        reuseItems: true
        boundsBehavior: Flickable.StopAtBounds
        activeFocusOnTab: true
        keyNavigationWraps: false
        ScrollBar.vertical: ScrollBar {}
        Accessible.role: Accessible.List
        Accessible.name: qsTr("Radio stations")

        function playSelected(event) {
            if (currentIndex < 0)
                return
            const station = root.backend.radioModel.get(currentIndex)
            root.backend.loadRadioStation(station.playlistId)
            event.accepted = true
        }
        Keys.onReturnPressed: event => playSelected(event)
        Keys.onEnterPressed: event => playSelected(event)

        delegate: Item {
            id: stationRow
            required property int index
            required property string playlistId
            required property string name
            required property string section
            required property int track_count
            required property bool favorite

            width: stations.width
            height: 28
            readonly property bool current: stations.currentIndex === index
            readonly property color rowColor: current ? Theme.accent : Theme.fg
            readonly property color metaColor: current ? Theme.accent : Theme.dim
            Accessible.role: Accessible.ListItem
            Accessible.name: favorite ? qsTr("Favorite station: %1").arg(name) : name
            Accessible.selected: current
            Accessible.onPressAction: play()

            function play() {
                stations.currentIndex = index
                stations.forceActiveFocus()
                root.backend.loadRadioStation(playlistId)
            }

            Rectangle {
                anchors.fill: parent
                color: stationRow.current ? Theme.rowSelected
                                          : mouse.containsMouse ? Theme.rowHover : "transparent"
                border.width: stations.activeFocus && stations.currentIndex === stationRow.index ? 1 : 0
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
                id: signal
                anchors.left: parent.left
                anchors.leftMargin: 14
                anchors.verticalCenter: parent.verticalCenter
                width: 14
                text: "≋"
                color: stationRow.favorite ? Theme.accent2 : stationRow.metaColor
                font.family: Theme.mono
                font.pixelSize: 11
            }
            Text {
                anchors.left: signal.right
                anchors.leftMargin: 10
                anchors.right: tags.left
                anchors.rightMargin: 10
                anchors.verticalCenter: parent.verticalCenter
                text: stationRow.name
                color: stationRow.rowColor
                elide: Text.ElideRight
                font.family: Theme.mono
                font.pixelSize: 11
            }
            Text {
                id: tags
                visible: root.showTags && stationRow.section.length > 0
                anchors.right: rate.left
                anchors.rightMargin: visible ? 10 : 0
                anchors.verticalCenter: parent.verticalCenter
                width: visible ? 170 : 0
                text: stationRow.section
                color: stationRow.metaColor
                elide: Text.ElideRight
                font.family: Theme.mono
                font.pixelSize: 11
            }
            Text {
                id: rate
                anchors.right: parent.right
                anchors.rightMargin: 14
                anchors.verticalCenter: parent.verticalCenter
                text: stationRow.track_count > 0
                      ? (stationRow.track_count === 1 ? qsTr("1 track")
                                                      : qsTr("%1 tracks").arg(stationRow.track_count))
                      : qsTr("live")
                color: stationRow.metaColor
                font.family: Theme.mono
                font.pixelSize: 11
            }
            MouseArea {
                id: mouse
                anchors.fill: parent
                hoverEnabled: true
                cursorShape: Qt.PointingHandCursor
                onClicked: stationRow.play()
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
