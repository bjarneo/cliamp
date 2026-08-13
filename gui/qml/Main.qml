pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts

ApplicationWindow {
    id: window

    width: 760
    height: 700
    minimumWidth: 688
    minimumHeight: 620
    visible: true
    title: qsTr("cliamp GUI")
    color: "#07090a"

    property var spectrumBands: []
    property int activeTab: 0
    GuiController { id: controller }

    Connections {
        target: controller
        function onBandsChanged(bands) { window.spectrumBands = bands }
    }

    Shortcut { sequence: "Space"; onActivated: controller.toggle() }
    Shortcut { sequence: "Left"; onActivated: controller.seekTo(Math.max(0, controller.position - 5)) }
    Shortcut { sequence: "Right"; onActivated: controller.seekTo(controller.position + 5) }
    Shortcut { sequence: "E"; onActivated: window.activeTab = 1 }
    Shortcut { sequence: "L"; onActivated: window.activeTab = 2 }
    Shortcut { sequence: "R"; onActivated: window.activeTab = 3 }

    Rectangle {
        anchors.fill: parent
        gradient: Gradient {
            GradientStop { position: 0.0; color: "#101614" }
            GradientStop { position: 0.72; color: "#07090a" }
        }
    }

    Rectangle {
        id: playerFrame
        anchors.centerIn: parent
        width: Math.min(640, parent.width - 48)
        height: Math.min(610, parent.height - 64)
        color: Theme.bg
        border.width: 1
        border.color: Theme.line
        radius: 4

        Rectangle {
            anchors.fill: parent
            anchors.margins: 1
            color: "transparent"
            border.width: 1
            border.color: "#ffffff08"
            radius: 3
        }

        Item {
            id: titleBar
            anchors.top: parent.top
            anchors.left: parent.left
            anchors.right: parent.right
            height: 35

            Rectangle {
                anchors.fill: parent
                color: Theme.panel
                radius: 4
            }
            Rectangle {
                anchors.bottom: parent.bottom
                width: parent.width
                height: 1
                color: Theme.line
            }
            Row {
                anchors.left: parent.left
                anchors.leftMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                spacing: 4
                Repeater {
                    model: ["#5c7355", "#ffb454", "#ff6e87"]
                    Rectangle {
                        required property string modelData
                        width: 8
                        height: 8
                        radius: 4
                        color: modelData
                        opacity: 0.8
                        Accessible.ignored: true
                    }
                }
                Text {
                    leftPadding: 5
                    text: qsTr("cliamp")
                    color: Theme.fg
                    font.family: Theme.mono
                    font.pixelSize: 11
                    font.bold: true
                }
            }
            Text {
                anchors.horizontalCenter: parent.horizontalCenter
                anchors.verticalCenter: parent.verticalCenter
                text: controller.connected ? qsTr("%1 - %2 tracks").arg(controller.selectedPlaylist || qsTr("queue")).arg(controller.queueCount)
                                           : qsTr("daemon offline")
                color: Theme.dim
                elide: Text.ElideRight
                font.family: Theme.mono
                font.pixelSize: 10
            }
            Row {
                anchors.right: parent.right
                anchors.rightMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                spacing: 6
                Repeater {
                    model: ["phosphor", "amber", "ice", "magenta"]
                    Rectangle {
                        required property string modelData
                        width: 12
                        height: 12
                        radius: 6
                        color: Theme.palettes[modelData].accent
                        border.width: Theme.paletteName === modelData ? 1 : 0
                        border.color: Theme.fg
                        Accessible.role: Accessible.Button
                        Accessible.name: qsTr("Use %1 palette").arg(modelData)
                        MouseArea {
                            anchors.fill: parent
                            cursorShape: Qt.PointingHandCursor
                            onClicked: Theme.setPalette(parent.modelData)
                        }
                    }
                }
            }
        }

        Item {
            id: nowPlaying
            anchors.top: titleBar.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            height: 126

            Rectangle {
                id: albumArt
                anchors.left: parent.left
                anchors.top: parent.top
                anchors.leftMargin: 14
                anchors.topMargin: 14
                width: 96
                height: 96
                color: "#1a0b2e"
                border.color: Theme.line
                border.width: 1
                clip: true

                Image {
                    anchors.fill: parent
                    source: controller.albumArtUrl
                    visible: status === Image.Ready
                    asynchronous: true
                    fillMode: Image.PreserveAspectCrop
                    sourceSize.width: 192
                    sourceSize.height: 192
                }
                Rectangle {
                    visible: !controller.albumArtUrl
                    anchors.fill: parent
                    gradient: Gradient {
                        GradientStop { position: 0; color: "#40126b" }
                        GradientStop { position: 1; color: "#0a0416" }
                    }
                }
                Rectangle {
                    visible: !controller.albumArtUrl
                    width: 52
                    height: 52
                    radius: 26
                    anchors.centerIn: parent
                    color: "#ff2d95"
                }
                Text {
                    visible: !controller.albumArtUrl
                    anchors.horizontalCenter: parent.horizontalCenter
                    anchors.bottom: parent.bottom
                    anchors.bottomMargin: 5
                    text: qsTr("CLIAMP")
                    color: "#ffd7f0"
                    font.family: Theme.mono
                    font.pixelSize: 7
                    font.bold: true
                    font.letterSpacing: 0.18
                }
            }

            Item {
                anchors.left: albumArt.right
                anchors.right: parent.right
                anchors.top: parent.top
                anchors.bottom: parent.bottom
                anchors.leftMargin: 14
                anchors.rightMargin: 14
                anchors.topMargin: 14
                anchors.bottomMargin: 14

                Text {
                    id: trackTitle
                    anchors.top: parent.top
                    anchors.left: parent.left
                    anchors.right: stateText.left
                    anchors.rightMargin: 10
                    text: controller.title
                    color: Theme.fg
                    elide: Text.ElideRight
                    font.family: Theme.mono
                    font.pixelSize: 14
                    font.bold: true
                }
                Text {
                    id: stateText
                    anchors.top: parent.top
                    anchors.right: parent.right
                    text: controller.state === "playing" ? ">" : controller.state === "paused" ? "||" : "[]"
                    color: Theme.accent
                    font.family: Theme.mono
                    font.pixelSize: 14
                    font.bold: true
                }
                Text {
                    anchors.top: trackTitle.bottom
                    anchors.topMargin: 5
                    anchors.left: parent.left
                    anchors.right: parent.right
                    text: controller.artist || qsTr("Unknown artist")
                    color: Theme.dim
                    elide: Text.ElideRight
                    font.family: Theme.mono
                    font.pixelSize: 11
                }
                Text {
                    anchors.top: trackTitle.bottom
                    anchors.topMargin: 23
                    anchors.left: parent.left
                    anchors.right: parent.right
                    text: controller.album
                    color: Theme.dim
                    elide: Text.ElideRight
                    font.family: Theme.mono
                    font.pixelSize: 10
                }
                SpectrumVisualizer {
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.bottom: parent.bottom
                    height: 40
                    bands: window.spectrumBands
                }
            }
        }

        Item {
            id: seekRow
            anchors.top: nowPlaying.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            height: 30

            Text {
                id: elapsed
                anchors.left: parent.left
                anchors.leftMargin: 14
                anchors.verticalCenter: parent.verticalCenter
                width: 48
                text: controller.formatDuration(controller.position)
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 10
            }
            Item {
                id: seekTrack
                anchors.left: elapsed.right
                anchors.right: total.left
                anchors.verticalCenter: parent.verticalCenter
                anchors.leftMargin: 4
                anchors.rightMargin: 4
                height: 16
                readonly property real progress: controller.duration > 0 ? Math.min(1, controller.position / controller.duration) : 0
                Rectangle {
                    anchors.verticalCenter: parent.verticalCenter
                    width: parent.width
                    height: 4
                    color: Theme.line
                }
                Rectangle {
                    anchors.verticalCenter: parent.verticalCenter
                    width: parent.width * seekTrack.progress
                    height: 4
                    color: Theme.accent
                }
                Rectangle {
                    visible: controller.duration > 0
                    x: Math.max(0, Math.min(parent.width - width, parent.width * seekTrack.progress - width / 2))
                    anchors.verticalCenter: parent.verticalCenter
                    width: 3
                    height: 12
                    color: Theme.fg
                }
                MouseArea {
                    id: seekMouse
                    anchors.fill: parent
                    cursorShape: controller.duration > 0 ? Qt.PointingHandCursor : Qt.ArrowCursor
                    enabled: controller.duration > 0
                    property real target: controller.position
                    function setTarget(x) { target = Math.max(0, Math.min(1, x / width)) * controller.duration }
                    onPressed: mouse => setTarget(mouse.x)
                    onPositionChanged: mouse => { if (pressed) setTarget(mouse.x) }
                    onReleased: controller.seekTo(target)
                }
            }
            Text {
                id: total
                anchors.right: parent.right
                anchors.rightMargin: 14
                anchors.verticalCenter: parent.verticalCenter
                width: 48
                horizontalAlignment: Text.AlignRight
                text: controller.formatDuration(controller.duration)
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 10
            }
        }

        Item {
            id: transport
            anchors.top: seekRow.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            height: 43

            Row {
                anchors.left: parent.left
                anchors.leftMargin: 10
                anchors.verticalCenter: parent.verticalCenter
                spacing: 2
                LedButton { text: "|<"; compact: true; Accessible.name: qsTr("Previous track"); onClicked: controller.previous() }
                LedButton { text: controller.state === "playing" ? "||" : ">"; accentActive: true; Accessible.name: controller.state === "playing" ? qsTr("Pause") : qsTr("Play"); onClicked: controller.toggle() }
                LedButton { text: "[]"; compact: true; Accessible.name: qsTr("Stop"); onClicked: controller.stop() }
                LedButton { text: ">|"; compact: true; Accessible.name: qsTr("Next track"); onClicked: controller.next() }
                Item { width: 10; height: 1 }
                LedButton { text: qsTr("SHUF"); compact: true; accentActive: controller.shuffle; Accessible.name: qsTr("Toggle shuffle"); onClicked: controller.setShuffle(!controller.shuffle) }
                LedButton { text: qsTr("REP"); compact: true; accentActive: controller.repeat !== "off"; Accessible.name: qsTr("Cycle repeat mode"); onClicked: controller.cycleRepeat() }
            }
            Text {
                id: volumeLabel
                anchors.right: volumeSlider.left
                anchors.rightMargin: 6
                anchors.verticalCenter: parent.verticalCenter
                text: qsTr("VOL")
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 10
            }
            Slider {
                id: volumeSlider
                anchors.right: volumeValue.left
                anchors.rightMargin: 8
                anchors.verticalCenter: parent.verticalCenter
                width: 92
                from: -50
                to: 6
                value: controller.volume
                Accessible.name: qsTr("Volume in decibels")
                onMoved: controller.setVolume(value)
                background: Rectangle {
                    x: volumeSlider.leftPadding
                    y: volumeSlider.topPadding + volumeSlider.availableHeight / 2 - height / 2
                    width: volumeSlider.availableWidth
                    height: 3
                    color: Theme.line
                    Rectangle { width: volumeSlider.visualPosition * parent.width; height: parent.height; color: Theme.accent }
                }
                handle: Rectangle {
                    x: volumeSlider.leftPadding + volumeSlider.visualPosition * (volumeSlider.availableWidth - width)
                    y: volumeSlider.topPadding + volumeSlider.availableHeight / 2 - height / 2
                    width: 7
                    height: 11
                    color: Theme.fg
                }
            }
            Text {
                id: volumeValue
                anchors.right: parent.right
                anchors.rightMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                width: 29
                horizontalAlignment: Text.AlignRight
                text: controller.volume.toFixed(0)
                color: Theme.accent
                font.family: Theme.mono
                font.pixelSize: 10
            }
        }

        Item {
            id: tabBar
            anchors.top: transport.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            height: 37

            Rectangle { anchors.fill: parent; color: Theme.panel; border.color: Theme.line; border.width: 1 }
            Row {
                anchors.left: parent.left
                anchors.leftMargin: 9
                anchors.verticalCenter: parent.verticalCenter
                spacing: 2
                Repeater {
                    model: [qsTr("PLAYLIST"), qsTr("EQUALIZER"), qsTr("LIBRARY"), qsTr("RADIO")]
                    LedButton {
                        id: tabButton
                        required property string modelData
                        required property int index
                        text: modelData
                        compact: true
                        width: modelData === qsTr("EQUALIZER") ? 91 : modelData === qsTr("PLAYLIST") ? 80 : 65
                        accentActive: window.activeTab === tabButton.index
                        onClicked: window.activeTab = tabButton.index
                    }
                }
            }
            Text {
                anchors.right: parent.right
                anchors.rightMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                text: window.activeTab === 0 ? qsTr("double-click to play")
                      : window.activeTab === 1 ? qsTr("-12dB / +12dB")
                      : window.activeTab === 2 ? qsTr("browse providers") : qsTr("live directories")
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 9
            }
        }

        StackLayout {
            id: panel
            anchors.top: tabBar.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: statusBar.top
            currentIndex: window.activeTab
            clip: true
            PlaylistView { backend: controller }
            EqualizerView { backend: controller }
            LibraryView { backend: controller }
            RadioView { backend: controller }
        }

        Item {
            id: statusBar
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            height: 27
            Rectangle { anchors.fill: parent; color: Theme.panel; border.color: Theme.line; border.width: 1 }
            Text {
                anchors.left: parent.left
                anchors.leftMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                text: controller.connected ? (controller.state.toUpperCase() + "  /  " + controller.speed.toFixed(2) + "x") : qsTr("OFFLINE")
                color: controller.connected ? Theme.accent : Theme.accent2
                font.family: Theme.mono
                font.pixelSize: 9
                font.letterSpacing: 0.07
            }
            Text {
                anchors.horizontalCenter: parent.horizontalCenter
                anchors.verticalCenter: parent.verticalCenter
                text: controller.mono ? qsTr("mono output") : qsTr("stereo output")
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 9
            }
            Text {
                anchors.right: parent.right
                anchors.rightMargin: controller.connected ? 12 : startButton.width + 19
                anchors.verticalCenter: parent.verticalCenter
                text: qsTr("space play  e eq  l library")
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 9
            }
            LedButton {
                id: startButton
                visible: !controller.connected
                anchors.right: parent.right
                anchors.rightMargin: 5
                anchors.verticalCenter: parent.verticalCenter
                text: qsTr("START")
                compact: true
                Accessible.name: qsTr("Start cliamp daemon")
                onClicked: controller.startDaemon()
            }
        }

        Rectangle {
            visible: !controller.connected && controller.connectionMessage.length > 0
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: statusBar.top
            height: 27
            color: Qt.rgba(Theme.accent2.r, Theme.accent2.g, Theme.accent2.b, 0.12)
            border.color: Theme.line
            border.width: 1
            Text {
                anchors.fill: parent
                anchors.leftMargin: 10
                anchors.rightMargin: 10
                verticalAlignment: Text.AlignVCenter
                text: controller.connectionMessage
                color: Theme.accent2
                elide: Text.ElideRight
                font.family: Theme.mono
                font.pixelSize: 9
            }
        }
    }
}
