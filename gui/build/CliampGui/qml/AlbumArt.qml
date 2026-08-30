pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Effects

// 96px cover slot. Real embedded artwork wins when the daemon publishes it;
// otherwise this draws the mockup's synthwave placeholder: a sunset gradient, a
// scanlined sun, and a perspective-warped neon grid, all as vector primitives
// so it stays crisp at any scale and costs no image assets.
Item {
    id: root

    required property string artworkUrl
    required property string album

    implicitWidth: 96
    implicitHeight: 96

    // Every metric below is expressed in units of the mockup's 94px content
    // box, so the placeholder keeps its proportions at any cover size instead
    // of stranding a small sun in a large frame.
    readonly property real unit: Math.max(1, width - 2) / 94

    Rectangle {
        anchors.fill: parent
        color: "transparent"
        border.width: 1
        border.color: Theme.line
        z: 2
    }

    Item {
        id: content
        anchors.fill: parent
        anchors.margins: 1
        clip: true

        Image {
            id: cover
            anchors.fill: parent
            source: root.artworkUrl
            visible: status === Image.Ready
            asynchronous: true
            fillMode: Image.PreserveAspectCrop
            sourceSize.width: 192
            sourceSize.height: 192
            Accessible.role: Accessible.Graphic
            Accessible.name: qsTr("Album artwork for %1").arg(root.album)
            Accessible.ignored: status !== Image.Ready
        }

        Item {
            id: placeholder
            anchors.fill: parent
            visible: cover.status !== Image.Ready

            // Sky above the horizon, ground below it, with a hard cut at 46%.
            Rectangle {
                anchors.fill: parent
                gradient: Gradient {
                    GradientStop { position: 0.0; color: "#1a0b2e" }
                    GradientStop { position: 0.4599; color: "#40126b" }
                    GradientStop { position: 0.46; color: "#0a0416" }
                    GradientStop { position: 1.0; color: "#0a0416" }
                }
            }

            Rectangle {
                id: sun
                width: 50 * root.unit
                height: width
                radius: width / 2
                anchors.horizontalCenter: parent.horizontalCenter
                y: 16 * root.unit
                gradient: Gradient {
                    GradientStop { position: 0.0; color: "#ffe066" }
                    GradientStop { position: 0.45; color: "#ff8a3d" }
                    GradientStop { position: 1.0; color: "#ff2d95" }
                }
            }

            // Scanlines across the sun's lower half: 2px bars on a 5px pitch.
            Item {
                id: scanlines
                anchors.left: parent.left
                anchors.right: parent.right
                y: 30 * root.unit
                height: 30 * root.unit
                clip: true

                readonly property real pitch: 5 * root.unit

                Repeater {
                    model: Math.ceil(scanlines.height / scanlines.pitch)

                    Rectangle {
                        required property int index
                        anchors.left: parent.left
                        anchors.right: parent.right
                        y: index * scanlines.pitch + 3 * root.unit
                        height: 2 * root.unit
                        color: "#1a0b2e"
                    }
                }
            }

            // The horizon grid. Lines are laid out flat, then projected with
            // the mockup's exact CSS transform: perspective(60px) rotateX(52deg)
            // about the bottom edge.
            Item {
                id: grid
                width: parent.width * 1.6
                height: 44 * root.unit
                x: -parent.width * 0.3
                y: parent.height - height

                transform: Matrix4x4 {
                    matrix: {
                        const originX = grid.width / 2
                        const originY = grid.height
                        const angle = 52 * Math.PI / 180
                        const cos = Math.cos(angle)
                        const sin = Math.sin(angle)
                        const toOrigin = Qt.matrix4x4(1, 0, 0, -originX,
                                                      0, 1, 0, -originY,
                                                      0, 0, 1, 0,
                                                      0, 0, 0, 1)
                        const rotate = Qt.matrix4x4(1, 0, 0, 0,
                                                    0, cos, -sin, 0,
                                                    0, sin, cos, 0,
                                                    0, 0, 0, 1)
                        const perspective = Qt.matrix4x4(1, 0, 0, 0,
                                                         0, 1, 0, 0,
                                                         0, 0, 1, 0,
                                                         0, 0, -1 / 60, 1)
                        const fromOrigin = Qt.matrix4x4(1, 0, 0, originX,
                                                        0, 1, 0, originY,
                                                        0, 0, 1, 0,
                                                        0, 0, 0, 1)
                        return fromOrigin.times(perspective).times(rotate).times(toOrigin)
                    }
                }

                Repeater {
                    model: Math.ceil(grid.height / (7 * root.unit))

                    Rectangle {
                        required property int index
                        anchors.left: parent.left
                        anchors.right: parent.right
                        y: index * 7 * root.unit
                        height: 1
                        color: "#00e5ff"
                        opacity: 0.333
                    }
                }
                Repeater {
                    model: Math.ceil(grid.width / (12 * root.unit))

                    Rectangle {
                        required property int index
                        anchors.top: parent.top
                        anchors.bottom: parent.bottom
                        x: index * 12 * root.unit
                        width: 1
                        color: "#ff2d95"
                        opacity: 0.4
                    }
                }
            }

            Rectangle {
                anchors.fill: parent
                gradient: Gradient {
                    GradientStop { position: 0.6; color: "#000a0416" }
                    GradientStop { position: 1.0; color: "#990a0416" }
                }
            }

            Text {
                id: caption
                anchors.left: parent.left
                anchors.bottom: parent.bottom
                anchors.leftMargin: 6 * root.unit
                anchors.bottomMargin: 5 * root.unit
                width: parent.width - 12 * root.unit
                text: root.album.toUpperCase()
                color: "#ffd7f0"
                elide: Text.ElideRight
                font.family: Theme.mono
                readonly property int captionSize: Math.max(6, Math.round(7 * root.unit))
                font.pixelSize: captionSize
                font.letterSpacing: captionSize * 0.14
                layer.enabled: true
                layer.effect: MultiEffect {
                    shadowEnabled: true
                    shadowColor: "#ff2d95"
                    shadowBlur: 1.0
                    shadowHorizontalOffset: 0
                    shadowVerticalOffset: 0
                }
            }
        }
    }
}
