// Entry point: run with `qs -p contrib/quickshell/shell.qml`
// or symlink this directory into ~/.config/quickshell/cliamp/ and run `qs -c cliamp`.
//
// Renders a notification-sized "now playing" card centered along the bottom of
// every screen, driven by cliamp's MPRIS service. Hides when cliamp is gone.

import Quickshell
import Quickshell.Services.Mpris
import Quickshell.Wayland
import QtQuick

Scope {
    id: root

    readonly property var cliampPlayer: {
        for (let i = 0; i < Mpris.players.values.length; ++i) {
            const p = Mpris.players.values[i];
            if (p.dbusName === "cliamp" || p.identity === "Cliamp")
                return p;
        }
        return null;
    }

    Variants {
        model: Quickshell.screens

        PanelWindow {
            id: panel
            required property var modelData
            screen: modelData

            // Full-width strip along the bottom; the card is centered inside it.
            // The strip itself is transparent, so only the card is visible.
            anchors {
                bottom: true
                left: true
                right: true
            }
            margins { bottom: 16 }

            exclusionMode: ExclusionMode.Ignore

            // A now-playing overlay must not steal keyboard focus from the
            // active application when it appears or is clicked.
            WlrLayershell.keyboardFocus: WlrKeyboardFocus.None

            implicitHeight: 72
            color: "transparent"

            visible: root.cliampPlayer !== null

            NowPlaying {
                width: 300
                height: parent.height
                anchors.horizontalCenter: parent.horizontalCenter
                player: root.cliampPlayer
            }
        }
    }
}
