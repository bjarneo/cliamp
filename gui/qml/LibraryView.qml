pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

Item {
    id: root
    required property var backend

    Rectangle {
        id: providerStrip
        anchors.top: parent.top
        anchors.left: parent.left
        anchors.right: parent.right
        height: 42
        color: Theme.panel
        border.color: Theme.line
        border.width: 1

        ListView {
            anchors.fill: parent
            anchors.leftMargin: 8
            anchors.rightMargin: 8
            orientation: ListView.Horizontal
            spacing: 4
            clip: true
            model: root.backend.providersModel

            delegate: LedButton {
                required property string key
                required property string name
                required property bool searchable
                text: name.toUpperCase()
                compact: false
                accentActive: root.backend.selectedProvider === key
                width: Math.max(65, implicitWidth + 18)
                Accessible.name: qsTr("Browse %1").arg(name)
                onClicked: root.backend.selectProvider(key)
            }
        }
    }

    Rectangle {
        id: browserPane
        anchors.top: providerStrip.bottom
        anchors.bottom: parent.bottom
        anchors.left: parent.left
        width: 210
        color: Qt.darker(Theme.bg, 1.08)
        border.color: Theme.line
        border.width: 1

        Text {
            id: browserTitle
            anchors.top: parent.top
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.margins: 10
            height: 18
            text: root.backend.selectedProvider.toUpperCase()
            color: Theme.accent
            font.family: Theme.mono
            font.pixelSize: 10
            font.letterSpacing: 0.12
        }

        ListView {
            id: playlists
            anchors.top: browserTitle.bottom
            anchors.bottom: parent.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.margins: 5
            clip: true
            model: root.backend.providerPlaylistsModel
            reuseItems: true
            ScrollBar.vertical: ScrollBar {}

            delegate: Item {
                id: playlistRow
                required property string playlistId
                required property string name
                required property int track_count
                width: playlists.width
                height: 28
                readonly property bool selected: root.backend.selectedPlaylist === playlistId

                Rectangle {
                    anchors.fill: parent
                    color: parent.selected ? Theme.selection : mouse.containsMouse ? Theme.hover : "transparent"
                }
                Text {
                    anchors.left: parent.left
                    anchors.right: count.left
                    anchors.rightMargin: 6
                    anchors.verticalCenter: parent.verticalCenter
                    anchors.leftMargin: 6
                    text: playlistRow.name
                    elide: Text.ElideRight
                    color: parent.selected ? Theme.accent : Theme.fg
                    font.family: Theme.mono
                    font.pixelSize: 10
                }
                Text {
                    id: count
                    anchors.right: parent.right
                    anchors.rightMargin: 6
                    anchors.verticalCenter: parent.verticalCenter
                    text: playlistRow.track_count > 0 ? playlistRow.track_count : ""
                    color: Theme.dim
                    font.family: Theme.mono
                    font.pixelSize: 9
                }
                MouseArea {
                    id: mouse
                    anchors.fill: parent
                    hoverEnabled: true
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.backend.browseProviderPlaylist(playlistRow.playlistId)
                    onDoubleClicked: root.backend.loadSelectedPlaylist()
                }
            }
        }
    }

    Item {
        id: tracksPane
        anchors.top: providerStrip.bottom
        anchors.bottom: parent.bottom
        anchors.left: browserPane.right
        anchors.right: parent.right

        TextField {
            id: search
            anchors.top: parent.top
            anchors.left: parent.left
            anchors.right: playAll.left
            anchors.margins: 8
            height: 28
            placeholderText: qsTr("Search provider")
            color: Theme.fg
            font.family: Theme.mono
            font.pixelSize: 10
            selectByMouse: true
            background: Rectangle {
                color: Theme.bg
                border.width: search.activeFocus ? 1 : 1
                border.color: search.activeFocus ? Theme.accent : Theme.line
                radius: 1
            }
            onAccepted: root.backend.searchProvider(text)
        }

        LedButton {
            id: playAll
            anchors.top: parent.top
            anchors.right: parent.right
            anchors.topMargin: 8
            anchors.rightMargin: 8
            text: qsTr("LOAD")
            compact: true
            enabled: root.backend.selectedPlaylist.length > 0
            Accessible.name: qsTr("Load selected playlist")
            onClicked: root.backend.loadSelectedPlaylist()
        }

        ListView {
            id: tracks
            anchors.top: search.bottom
            anchors.bottom: parent.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.topMargin: 2
            clip: true
            model: root.backend.libraryTracksModel
            reuseItems: true
            ScrollBar.vertical: ScrollBar {}

            delegate: Item {
                id: trackRow
                required property int index
                required property string title
                required property string artist
                required property string album
                required property int duration_secs
                width: tracks.width
                height: 34

                Rectangle {
                    anchors.fill: parent
                    color: trackMouse.containsMouse ? Theme.hover : "transparent"
                }
                Text {
                    id: titleText
                    anchors.left: parent.left
                    anchors.leftMargin: 9
                    anchors.right: durationText.left
                    anchors.top: parent.top
                    anchors.topMargin: 4
                    text: trackRow.title || qsTr("Unknown title")
                    color: Theme.fg
                    elide: Text.ElideRight
                    font.family: Theme.mono
                    font.pixelSize: 10
                }
                Text {
                    anchors.left: titleText.left
                    anchors.right: durationText.left
                    anchors.bottom: parent.bottom
                    anchors.bottomMargin: 4
                    text: trackRow.artist + (trackRow.album.length > 0 ? "  /  " + trackRow.album : "")
                    color: Theme.dim
                    elide: Text.ElideRight
                    font.family: Theme.mono
                    font.pixelSize: 9
                }
                Text {
                    id: durationText
                    anchors.right: parent.right
                    anchors.rightMargin: 9
                    anchors.verticalCenter: parent.verticalCenter
                    width: 42
                    horizontalAlignment: Text.AlignRight
                    text: root.backend.formatDuration(trackRow.duration_secs)
                    color: Theme.dim
                    font.family: Theme.mono
                    font.pixelSize: 9
                }
                MouseArea {
                    id: trackMouse
                    anchors.fill: parent
                    hoverEnabled: true
                    cursorShape: Qt.PointingHandCursor
                    onDoubleClicked: root.backend.playLibraryTrack(trackRow.index)
                }
            }
        }

        Text {
            anchors.centerIn: tracks
            visible: tracks.count === 0
            text: root.backend.selectedPlaylist.length > 0 ? qsTr("Select a track to play") : qsTr("Select a playlist")
            color: Theme.dim
            font.family: Theme.mono
            font.pixelSize: 10
        }
    }
}
