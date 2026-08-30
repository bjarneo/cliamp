pragma ComponentBehavior: Bound

import QtCore
import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts

// "Winamp-sized" player: now playing, seek, transport, tabs, a tab panel and a
// status bar. It opens at the design's 640x482, where every metric matches the
// mockup exactly, and reflows from there: the tab panel absorbs extra height,
// now playing grows only once the panel is comfortable, and secondary columns
// drop out as the window narrows.
//
// Window chrome is the desktop's job: macOS and regular Linux desktops draw
// their own title bar, and Omarchy (Hyprland) draws none, so the window goes
// frameless there rather than leaving a gap.
ApplicationWindow {
    id: window

    readonly property int cardWidth: 640
    readonly property int cardHeight: 482
    readonly property bool undecorated: controller.omarchySession
    readonly property bool hasTrack: controller.queueCount > 0 && controller.title.length > 0
    // Below this the transport row would collide with the volume slider, so the
    // volume block sheds its label and shrinks instead.
    readonly property bool narrow: width < 520

    width: cardWidth
    height: cardHeight
    minimumWidth: 400
    minimumHeight: 320
    visible: true
    // The desktop's title bar and task switcher are now where the track shows.
    title: hasTrack
           ? (controller.artist.length > 0
              ? qsTr("%1 · %2").arg(controller.title).arg(controller.artist)
              : controller.title)
           : qsTr("cliamp")
    color: "#07090a"
    flags: undecorated ? (Qt.Window | Qt.FramelessWindowHint) : Qt.Window

    property var spectrumBands: []
    property int activeTab: 0
    property int visualizerMode: 0

    function cycleVisualizer() {
        visualizerMode = (visualizerMode + 1) % spectrum.modeNames.length
    }

    // Remembers the chosen visualizer across restarts, in the GUI's own
    // settings file rather than cliamp's config.toml. The name is stored
    // rather than the index so adding or reordering modes cannot silently
    // change which one a returning user gets.
    Settings {
        id: settings
        category: "visualizer"
        property string modeName: "Bars"
    }

    Component.onCompleted: {
        const saved = spectrum.modeNames.indexOf(settings.modeName)
        if (saved >= 0)
            window.visualizerMode = saved
    }
    onVisualizerModeChanged: settings.modeName = spectrum.modeName
    readonly property bool textInputActive: activeFocusItem instanceof TextInput

    GuiController { id: controller }

    Binding {
        target: Theme
        property: "systemColors"
        value: controller.desktopTheme
    }
    Binding {
        target: Theme
        property: "systemDark"
        value: Application.styleHints.colorScheme !== Qt.Light
    }

    Connections {
        target: controller
        function onBandsChanged(bands) { window.spectrumBands = bands }
    }

    Shortcut { sequence: "Space"; enabled: controller.connected && !window.textInputActive; onActivated: controller.toggle() }
    Shortcut { sequence: "Left"; enabled: controller.connected && controller.seekable && !window.textInputActive; onActivated: controller.seekTo(Math.max(0, controller.position - 5)) }
    Shortcut { sequence: "Right"; enabled: controller.connected && controller.seekable && !window.textInputActive; onActivated: controller.seekTo(controller.position + 5) }
    Shortcut { sequence: "P"; enabled: !window.textInputActive; onActivated: window.activeTab = 0 }
    Shortcut { sequence: "E"; enabled: !window.textInputActive; onActivated: window.activeTab = 1 }
    Shortcut { sequence: "L"; enabled: !window.textInputActive; onActivated: window.activeTab = 2 }
    Shortcut { sequence: "R"; enabled: !window.textInputActive; onActivated: window.activeTab = 3 }
    Shortcut { sequence: "V"; enabled: !window.textInputActive; onActivated: window.cycleVisualizer() }
    Shortcut { sequence: "Ctrl+Q"; onActivated: window.close() }

    Rectangle {
        id: card
        anchors.fill: parent
        // Corner rounding belongs to whatever draws the frame, so the card
        // stays square and only keeps its own hairline edge.
        color: Theme.bg
        border.width: 1
        border.color: Theme.line
        clip: true

        Rectangle {
            anchors.fill: parent
            anchors.margins: 1
            color: "transparent"
            border.width: 1
            border.color: Theme.inset
            z: 10
        }

        // ----- Now playing -------------------------------------------------

        Item {
            id: nowPlaying
            anchors.top: parent.top
            anchors.topMargin: 1
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.leftMargin: 1
            anchors.rightMargin: 1
            height: Math.max(120, Math.min(200, Math.round(window.height * 0.22)))

            AlbumArt {
                id: albumArt
                x: 13
                y: 14
                // Square, inset by the design's 14/10 padding, and never so wide
                // that the metadata beside it has nowhere to go.
                width: Math.min(nowPlaying.height - 24, Math.round(window.width * 0.35))
                height: width
                artworkUrl: controller.albumArtUrl
                album: controller.album
            }

            Item {
                id: trackInfo
                anchors.left: albumArt.right
                anchors.leftMargin: 14
                anchors.right: parent.right
                anchors.rightMargin: 14
                y: albumArt.y
                height: albumArt.height

                Text {
                    id: stateGlyph
                    anchors.left: parent.left
                    anchors.baseline: trackTitle.baseline
                    text: controller.state === "playing" ? "▶"
                          : controller.state === "paused" ? "❚❚" : "■"
                    color: Theme.accent
                    font.family: Theme.mono
                    font.pixelSize: 10

                    // The mockup's cl-blink: a hard square wave, not a fade.
                    SequentialAnimation on opacity {
                        running: controller.state === "playing"
                        loops: Animation.Infinite
                        alwaysRunToEnd: false
                        PropertyAction { value: 1.0 }
                        PauseAnimation { duration: 800 }
                        PropertyAction { value: 0.15 }
                        PauseAnimation { duration: 800 }
                        onRunningChanged: {
                            if (!running)
                                stateGlyph.opacity = 1.0
                        }
                    }
                }
                Text {
                    id: trackTitle
                    anchors.top: parent.top
                    anchors.left: stateGlyph.right
                    anchors.leftMargin: 8
                    anchors.right: parent.right
                    text: controller.title
                    color: Theme.fg
                    elide: Text.ElideRight
                    font.family: Theme.mono
                    font.pixelSize: 14
                    font.bold: true
                }
                Text {
                    anchors.top: trackTitle.bottom
                    anchors.topMargin: 8
                    anchors.left: parent.left
                    anchors.right: parent.right
                    text: controller.album.length > 0
                          ? controller.artist + " · " + controller.album : controller.artist
                    color: Theme.dim
                    elide: Text.ElideRight
                    font.family: Theme.mono
                    font.pixelSize: 11
                }
                SpectrumVisualizer {
                    id: spectrum
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.bottom: parent.bottom
                    height: Math.max(24, Math.min(90, Math.round(trackInfo.height * 0.42)))
                    // Hold the mockup's bar pitch as the strip widens rather
                    // than stretching 34 bars across the whole window.
                    barCount: Math.max(12, Math.min(64, Math.round(width / 14.7)))
                    bands: window.spectrumBands
                    active: controller.state === "playing"
                    mode: window.visualizerMode
                    Accessible.role: Accessible.Graphic
                    Accessible.name: qsTr("%1 visualizer").arg(modeName)

                    MouseArea {
                        anchors.fill: parent
                        cursorShape: Qt.PointingHandCursor
                        onClicked: window.cycleVisualizer()
                    }

                    // The mode name shows itself briefly on a change, so the
                    // strip needs no permanent label.
                    Text {
                        id: modeLabel
                        anchors.right: parent.right
                        anchors.top: parent.top
                        text: spectrum.modeName.toUpperCase()
                        color: Theme.accent2
                        opacity: 0
                        font.family: Theme.mono
                        font.pixelSize: 9
                        font.letterSpacing: 9 * 0.1

                        function flash() {
                            opacity = 1
                            hideTimer.restart()
                        }
                        Behavior on opacity {
                            NumberAnimation { duration: 260 }
                        }
                        Timer {
                            id: hideTimer
                            interval: 1400
                            onTriggered: modeLabel.opacity = 0
                        }
                    }

                    Connections {
                        target: window
                        function onVisualizerModeChanged() { modeLabel.flash() }
                    }
                }
            }
        }

        // ----- Seek --------------------------------------------------------

        Item {
            id: seekRow
            anchors.top: nowPlaying.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.leftMargin: 1
            anchors.rightMargin: 1
            height: 26

            Text {
                id: elapsed
                anchors.left: parent.left
                anchors.leftMargin: 14
                anchors.top: parent.top
                height: 14
                verticalAlignment: Text.AlignVCenter
                text: controller.formatDuration(controller.position)
                color: Theme.accent
                font.family: Theme.mono
                font.pixelSize: 10
            }
            Item {
                id: seekTrack
                anchors.left: elapsed.right
                anchors.right: total.left
                anchors.leftMargin: 10
                anchors.rightMargin: 10
                anchors.top: parent.top
                height: 14
                readonly property real progress: controller.duration > 0
                                                 ? Math.min(1, controller.position / controller.duration) : 0

                Rectangle {
                    id: seekRail
                    anchors.verticalCenter: parent.verticalCenter
                    width: parent.width
                    height: 4
                    color: Theme.line

                    Rectangle {
                        width: parent.width * seekTrack.progress
                        height: parent.height
                        color: Theme.accent
                    }
                }
                Rectangle {
                    visible: controller.duration > 0
                    x: Math.max(0, Math.min(parent.width - width,
                                            parent.width * seekTrack.progress))
                    y: seekRail.y - 3
                    width: 3
                    height: 10
                    color: Theme.fg
                }
                MouseArea {
                    anchors.fill: parent
                    cursorShape: enabled ? Qt.PointingHandCursor : Qt.ArrowCursor
                    enabled: controller.connected && controller.seekable && controller.duration > 0
                    property real target: controller.position
                    function setTarget(x) {
                        target = Math.max(0, Math.min(1, x / width)) * controller.duration
                    }
                    onPressed: mouse => setTarget(mouse.x)
                    onPositionChanged: mouse => { if (pressed) setTarget(mouse.x) }
                    onReleased: controller.seekTo(target)
                }
            }
            Text {
                id: total
                anchors.right: parent.right
                anchors.rightMargin: 14
                anchors.top: parent.top
                height: 14
                verticalAlignment: Text.AlignVCenter
                text: controller.formatDuration(controller.duration)
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 10
            }
        }

        // ----- Transport ---------------------------------------------------

        Item {
            id: transport
            anchors.top: seekRow.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.leftMargin: 1
            anchors.rightMargin: 1
            height: 38

            Row {
                id: transportKeys
                anchors.left: parent.left
                anchors.leftMargin: 14
                anchors.top: parent.top
                spacing: 4

                FlatButton {
                    implicitWidth: 34
                    text: "|◀◀"
                    enabled: controller.connected
                    hoverBorderColor: Theme.accent
                    hoverContentColor: Theme.accent
                    Accessible.name: qsTr("Previous track")
                    onClicked: controller.previous()
                }
                FlatButton {
                    implicitWidth: 42
                    text: controller.state === "playing" ? "❚❚" : "▶"
                    fontSize: 11
                    enabled: controller.connected
                    borderColor: Theme.accent
                    contentColor: Theme.accent
                    hoverContentColor: Theme.accent
                    fillColor: Theme.accentFill
                    hoverFillColor: Theme.accentFillHover
                    Accessible.name: controller.state === "playing" ? qsTr("Pause") : qsTr("Play")
                    onClicked: controller.toggle()
                }
                FlatButton {
                    implicitWidth: 30
                    text: "■"
                    fontSize: 9
                    enabled: controller.connected
                    hoverBorderColor: Theme.accent
                    hoverContentColor: Theme.accent
                    Accessible.name: qsTr("Stop")
                    onClicked: controller.stop()
                }
                FlatButton {
                    implicitWidth: 34
                    text: "▶▶|"
                    enabled: controller.connected
                    hoverBorderColor: Theme.accent
                    hoverContentColor: Theme.accent
                    Accessible.name: qsTr("Next track")
                    onClicked: controller.next()
                }
            }

            FlatButton {
                id: shuffleKey
                anchors.left: transportKeys.right
                anchors.leftMargin: 8
                anchors.top: parent.top
                text: qsTr("SHUF")
                fontSize: 9
                letterSpacing: 9 * 0.08
                enabled: controller.connected
                contentColor: controller.shuffle ? Theme.accent : Theme.dim
                hoverContentColor: contentColor
                Accessible.name: qsTr("Toggle shuffle")
                Accessible.checked: controller.shuffle
                onClicked: controller.setShuffle(!controller.shuffle)
            }
            FlatButton {
                anchors.left: shuffleKey.right
                anchors.leftMargin: 8
                anchors.top: parent.top
                text: qsTr("REP")
                fontSize: 9
                letterSpacing: 9 * 0.08
                enabled: controller.connected
                contentColor: controller.repeat !== "off" ? Theme.accent : Theme.dim
                hoverContentColor: contentColor
                Accessible.name: qsTr("Cycle repeat mode")
                Accessible.checked: controller.repeat !== "off"
                onClicked: controller.cycleRepeat()
            }

            Text {
                id: volumeLabel
                visible: !window.narrow
                anchors.right: volumeTrack.left
                anchors.rightMargin: 8
                anchors.verticalCenter: transportKeys.verticalCenter
                text: qsTr("VOL")
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 9
                font.letterSpacing: 9 * 0.08
            }
            Item {
                id: volumeTrack
                anchors.right: volumeValue.left
                anchors.rightMargin: 8
                anchors.verticalCenter: transportKeys.verticalCenter
                width: window.narrow ? 56 : 92
                height: 14

                // The daemon works in dB; the mockup's bar is a 0..1 fill, so
                // the -50..+6 dB range maps onto the full width.
                readonly property real minimumDb: -50
                readonly property real maximumDb: 6
                readonly property real fraction: Math.max(0, Math.min(1,
                    (controller.volume - minimumDb) / (maximumDb - minimumDb)))

                Rectangle {
                    anchors.verticalCenter: parent.verticalCenter
                    width: parent.width
                    height: 4
                    color: Theme.line

                    Rectangle {
                        width: parent.width * volumeTrack.fraction
                        height: parent.height
                        color: Theme.accent2
                    }
                }
                MouseArea {
                    anchors.fill: parent
                    cursorShape: enabled ? Qt.PointingHandCursor : Qt.ArrowCursor
                    enabled: controller.connected
                    Accessible.role: Accessible.Slider
                    Accessible.name: qsTr("Volume in decibels")
                    function setVolume(x) {
                        const value = Math.max(0, Math.min(1, x / width))
                        controller.setVolume(volumeTrack.minimumDb
                                             + value * (volumeTrack.maximumDb - volumeTrack.minimumDb))
                    }
                    onPressed: mouse => setVolume(mouse.x)
                    onPositionChanged: mouse => { if (pressed) setVolume(mouse.x) }
                }
            }
            Text {
                id: volumeValue
                anchors.right: parent.right
                anchors.rightMargin: 14
                anchors.verticalCenter: transportKeys.verticalCenter
                width: 26
                horizontalAlignment: Text.AlignRight
                text: controller.volume.toFixed(0)
                color: Theme.dim
                font.family: Theme.mono
                font.pixelSize: 9
            }
        }

        // ----- Tabs --------------------------------------------------------

        Item {
            id: tabBar
            anchors.top: transport.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.leftMargin: 1
            anchors.rightMargin: 1
            height: 28

            Rectangle {
                anchors.fill: parent
                color: Theme.panel
            }
            Rectangle {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                height: 1
                color: Theme.line
            }
            Rectangle {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.bottom: parent.bottom
                height: 1
                color: Theme.line
            }

            Row {
                id: tabs
                anchors.left: parent.left
                anchors.top: parent.top
                anchors.topMargin: 1
                spacing: 0

                Repeater {
                    model: [qsTr("PLAYLIST"), qsTr("EQUALIZER"), qsTr("LIBRARY"), qsTr("RADIO")]

                    Item {
                        id: tab
                        required property string modelData
                        required property int index
                        width: tabLabel.implicitWidth + 32
                        height: 26
                        readonly property bool active: window.activeTab === index
                        Accessible.role: Accessible.PageTab
                        Accessible.name: modelData
                        Accessible.selected: active
                        Accessible.onPressAction: window.activeTab = tab.index

                        Rectangle {
                            anchors.fill: parent
                            color: tab.active ? Theme.surfaceRaised
                                              : tabMouse.containsMouse ? Theme.rowHover : "transparent"
                        }
                        Rectangle {
                            anchors.right: parent.right
                            anchors.top: parent.top
                            anchors.bottom: parent.bottom
                            width: 1
                            color: Theme.line
                        }
                        Text {
                            id: tabLabel
                            anchors.centerIn: parent
                            text: tab.modelData
                            color: tab.active ? Theme.accent : Theme.dim
                            font.family: Theme.mono
                            font.pixelSize: 10
                            font.letterSpacing: 10 * 0.14
                        }
                        MouseArea {
                            id: tabMouse
                            anchors.fill: parent
                            hoverEnabled: true
                            cursorShape: Qt.PointingHandCursor
                            onClicked: window.activeTab = tab.index
                        }
                    }
                }
            }

            Text {
                visible: !window.narrow
                anchors.left: tabs.right
                anchors.right: parent.right
                anchors.leftMargin: 12
                anchors.rightMargin: 12
                anchors.verticalCenter: parent.verticalCenter
                horizontalAlignment: Text.AlignRight
                text: window.activeTab === 1 ? qsTr("drag bands")
                      : window.activeTab === 2 ? controller.selectedProvider
                      : window.activeTab === 3 ? qsTr("live directory") : qsTr("enter to play")
                color: Theme.dim
                elide: Text.ElideRight
                font.family: Theme.mono
                font.pixelSize: 10
                font.letterSpacing: 10 * 0.1
            }
        }

        // ----- Panel -------------------------------------------------------

        StackLayout {
            id: panel
            anchors.top: tabBar.bottom
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: statusBar.top
            anchors.leftMargin: 1
            anchors.rightMargin: 1
            currentIndex: window.activeTab
            clip: true

            PlaylistView { backend: controller; enabled: window.activeTab === 0 }
            EqualizerView { backend: controller; enabled: window.activeTab === 1 }
            LibraryView { backend: controller; enabled: window.activeTab === 2 }
            RadioView { backend: controller; enabled: window.activeTab === 3 }
        }

        // ----- Status bar --------------------------------------------------

        Item {
            id: statusBar
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            anchors.leftMargin: 1
            anchors.rightMargin: 1
            anchors.bottomMargin: 1
            height: 24

            Rectangle {
                anchors.fill: parent
                color: Theme.panel
            }
            Rectangle {
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                height: 1
                color: Theme.line
            }

            Text {
                id: formatLabel
                anchors.left: parent.left
                anchors.leftMargin: 14
                anchors.verticalCenter: parent.verticalCenter
                text: controller.format
                color: Theme.accent2
                font.family: Theme.mono
                font.pixelSize: 9
                font.letterSpacing: 9 * 0.06
            }
            Text {
                id: deviceLabel
                anchors.left: formatLabel.right
                anchors.leftMargin: 14
                anchors.verticalCenter: parent.verticalCenter
                text: controller.mono ? qsTr("%1 · mono").arg(controller.audioDevice)
                                      : controller.audioDevice
                color: Theme.dim
                elide: Text.ElideRight
                font.family: Theme.mono
                font.pixelSize: 9
                font.letterSpacing: 9 * 0.06
            }
            Text {
                anchors.left: deviceLabel.right
                anchors.leftMargin: 14
                anchors.right: startButton.visible ? startButton.left : parent.right
                anchors.rightMargin: startButton.visible ? 10 : 14
                anchors.verticalCenter: parent.verticalCenter
                horizontalAlignment: Text.AlignRight
                text: controller.connected
                      ? qsTr("space play · ←→ seek · e eq · l library")
                      : (controller.connectionMessage.length > 0
                         ? controller.connectionMessage : qsTr("daemon offline"))
                color: controller.connected ? Theme.dim : Theme.accent2
                elide: Text.ElideRight
                font.family: Theme.mono
                font.pixelSize: 9
                font.letterSpacing: 9 * 0.06
            }
            FlatButton {
                id: startButton
                visible: !controller.connected
                anchors.right: parent.right
                anchors.rightMargin: 8
                anchors.verticalCenter: parent.verticalCenter
                text: qsTr("START")
                fontSize: 9
                letterSpacing: 9 * 0.1
                horizontalPadding: 8
                implicitHeight: 17
                contentColor: Theme.accent
                hoverContentColor: Theme.accent
                borderColor: Theme.accent
                fillColor: Theme.surfaceRaised
                hoverFillColor: Theme.rowSelected
                Accessible.name: qsTr("Start the cliamp daemon")
                onClicked: controller.startDaemon()
            }
        }
    }
}
