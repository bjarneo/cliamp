pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

// Provider strip plus browser. A filled dot means the provider is configured
// and reachable; a hollow dot means it has no config.toml section yet, and
// selecting it opens the connect sheet instead of a browser. The sheet drives
// `cliamp setup --provider`, so it reuses the same field specs, connection
// probes and TOML writer as the interactive wizard.
Item {
    id: root
    required property var backend

    // Key of the unconfigured provider whose connect sheet is open, or "".
    property string setupKey: ""

    readonly property var chips: buildChips(backend.providersModel.count, backend.setupSpecs)
    readonly property var activeSpec: specFor(setupKey)
    readonly property bool showSetup: setupKey.length > 0 && activeSpec !== null

    // Field values typed into the connect sheet, keyed by field key.
    property var setupValues: ({})
    // Which picker option is selected, kept separate from setupValues so that
    // typing in a field does not churn the visible-field list and tear down
    // the input the user is currently in.
    property string pickerValue: ""

    Component.onCompleted: backend.loadSetupSpecs()

    function specFor(key) {
        if (key.length === 0)
            return null
        const specs = backend.setupSpecs
        for (let i = 0; i < specs.length; ++i) {
            if (specs[i].key === key)
                return specs[i]
        }
        return null
    }

    // buildChips lists every provider the daemon offers, flagged with whether
    // config.toml actually has a section for it. The daemon advertises remote
    // providers whether or not they are set up, so the setup specs are what
    // decide between a filled dot (ready to browse) and a hollow one (opens
    // the connect sheet). Providers with no spec at all -- local files, the
    // radio directory -- need no credentials and are always ready.
    function buildChips(providerCount, specs) {
        const needsSetup = {}
        for (let j = 0; j < specs.length; ++j)
            needsSetup[specs[j].key] = !specs[j].configured

        const chips = []
        const seen = {}
        for (let i = 0; i < providerCount; ++i) {
            const provider = backend.providersModel.get(i)
            if (!provider || !provider.key)
                continue
            seen[provider.key] = true
            chips.push({
                "key": provider.key,
                "label": String(provider.name).toUpperCase(),
                "configured": !needsSetup[provider.key]
            })
        }
        for (let k = 0; k < specs.length; ++k) {
            if (specs[k].configured || seen[specs[k].key])
                continue
            chips.push({
                "key": specs[k].key,
                "label": String(specs[k].key).toUpperCase(),
                "configured": false
            })
        }
        return chips
    }

    // activePickerValue is the picker choice in effect: the user's pick, or the
    // spec's first option before they touch it.
    function activePickerValue(spec) {
        if (spec === null || !spec.picker)
            return ""
        return pickerValue.length > 0 ? pickerValue : spec.picker.options[0].value
    }

    // visibleFields drops the fields the spec's picker hides, mirroring the
    // wizard's onlyIf predicates.
    function visibleFields(spec, picked) {
        if (spec === null)
            return []
        const fields = []
        for (let i = 0; i < spec.fields.length; ++i) {
            const field = spec.fields[i]
            if (!field.visible_for || field.visible_for.length === 0
                    || field.visible_for.indexOf(picked) >= 0)
                fields.push(field)
        }
        return fields
    }

    function openSetup(key) {
        setupKey = key
        setupValues = {}
        pickerValue = ""
        backend.clearSetupResult()
    }

    // Values are stored in place: the sheet reads them only on submit, so there
    // is no binding to invalidate and no delegate to rebuild while typing.
    function setValue(key, value) {
        setupValues[key] = value
    }

    function submitSetup(force) {
        const spec = activeSpec
        if (spec === null)
            return
        const values = {}
        for (const key in setupValues)
            values[key] = setupValues[key]
        if (spec.picker)
            values[spec.picker.key] = activePickerValue(spec)
        backend.connectProvider(spec.key, values, force)
    }

    Item {
        id: providerStrip
        anchors.top: parent.top
        anchors.left: parent.left
        anchors.right: parent.right
        height: 33

        Rectangle {
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            height: 1
            color: Theme.line
        }

        ListView {
            anchors.fill: parent
            anchors.leftMargin: 12
            anchors.rightMargin: 12
            anchors.topMargin: 8
            anchors.bottomMargin: 8
            orientation: ListView.Horizontal
            spacing: 6
            clip: true
            boundsBehavior: Flickable.StopAtBounds
            model: root.chips

            delegate: FlatButton {
                id: chip
                required property var modelData
                readonly property bool selected: modelData.configured
                                                 ? (root.setupKey.length === 0
                                                    && root.backend.selectedProvider === modelData.key)
                                                 : root.setupKey === modelData.key
                text: modelData.label
                fontSize: 9
                letterSpacing: 9 * 0.1
                horizontalPadding: 8
                leftPadding: 19
                implicitHeight: 17
                contentColor: selected ? Theme.accent : Theme.dim
                hoverContentColor: Theme.accent
                borderColor: selected ? Theme.accent : Theme.line
                fillColor: selected ? Theme.surfaceRaised : "transparent"
                hoverFillColor: Theme.rowHover
                Accessible.name: chip.modelData.configured
                                 ? qsTr("Browse %1").arg(chip.modelData.label)
                                 : qsTr("Set up %1").arg(chip.modelData.label)
                onClicked: {
                    if (chip.modelData.configured) {
                        root.setupKey = ""
                        root.backend.selectProvider(chip.modelData.key)
                    } else {
                        root.openSetup(chip.modelData.key)
                    }
                }

                Rectangle {
                    anchors.left: parent.left
                    anchors.leftMargin: 8
                    anchors.verticalCenter: parent.verticalCenter
                    width: 5
                    height: 5
                    radius: 2.5
                    color: chip.modelData.configured
                           ? (chip.selected ? Theme.accent : Theme.dim) : "transparent"
                    border.width: chip.modelData.configured ? 0 : 1
                    border.color: Theme.dim
                }
            }
        }
    }

    // ----- Connect sheet ---------------------------------------------------

    Item {
        anchors.top: providerStrip.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        visible: root.showSetup

        Flickable {
            anchors.fill: parent
            contentHeight: sheet.height
            clip: true
            boundsBehavior: Flickable.StopAtBounds
            ScrollBar.vertical: ScrollBar {}

            Column {
                id: sheet
                width: parent.width
                topPadding: 16
                bottomPadding: 16
                leftPadding: 14
                rightPadding: 14
                spacing: 10

                Text {
                    text: qsTr("CONNECT %1").arg(root.activeSpec ? String(root.activeSpec.name).toUpperCase() : "")
                    color: Theme.accent2
                    font.family: Theme.mono
                    font.pixelSize: 10
                    font.letterSpacing: 10 * 0.12
                }

                Row {
                    visible: root.activeSpec !== null && root.activeSpec.picker !== undefined
                             && root.activeSpec.picker !== null
                    spacing: 8

                    Text {
                        width: 92
                        height: 24
                        verticalAlignment: Text.AlignVCenter
                        text: root.activeSpec && root.activeSpec.picker ? root.activeSpec.picker.label : ""
                        color: Theme.dim
                        font.family: Theme.mono
                        font.pixelSize: 10
                    }
                    Repeater {
                        model: root.activeSpec && root.activeSpec.picker
                               ? root.activeSpec.picker.options : []

                        FlatButton {
                            id: option
                            required property var modelData
                            readonly property bool selected:
                                root.activePickerValue(root.activeSpec) === modelData.value
                            text: modelData.label
                            fontSize: 9
                            letterSpacing: 9 * 0.1
                            horizontalPadding: 9
                            implicitHeight: 24
                            contentColor: selected ? Theme.accent : Theme.dim
                            hoverContentColor: Theme.accent
                            borderColor: selected ? Theme.accent : Theme.line
                            fillColor: selected ? Theme.surfaceRaised : "transparent"
                            hoverFillColor: Theme.rowHover
                            onClicked: root.pickerValue = option.modelData.value
                        }
                    }
                }

                Repeater {
                    model: root.visibleFields(root.activeSpec, root.activePickerValue(root.activeSpec))

                    Row {
                        id: fieldRow
                        required property var modelData
                        required property int index
                        width: sheet.width - sheet.leftPadding - sheet.rightPadding
                        spacing: 10

                        Text {
                            width: 92
                            height: 26
                            verticalAlignment: Text.AlignVCenter
                            text: fieldRow.modelData.label.toLowerCase()
                            color: Theme.dim
                            elide: Text.ElideRight
                            font.family: Theme.mono
                            font.pixelSize: 10
                        }
                        Rectangle {
                            width: parent.width - 92 - parent.spacing
                            height: 26
                            color: Theme.panel
                            border.width: 1
                            border.color: input.activeFocus ? Theme.accent : Theme.line

                            TextInput {
                                id: input
                                anchors.fill: parent
                                anchors.leftMargin: 8
                                anchors.rightMargin: 8
                                verticalAlignment: TextInput.AlignVCenter
                                activeFocusOnTab: true
                                color: Theme.fg
                                font.family: Theme.mono
                                font.pixelSize: 11
                                selectByMouse: true
                                clip: true
                                echoMode: fieldRow.modelData.secret ? TextInput.Password : TextInput.Normal
                                // Seed once rather than bind, so a rebuild keeps what was
                                // typed and typing never fights a binding. The first field
                                // takes focus so the sheet is ready to type into.
                                Component.onCompleted: {
                                    text = root.setupValues[fieldRow.modelData.key] !== undefined
                                           ? root.setupValues[fieldRow.modelData.key]
                                           : (fieldRow.modelData.default || "")
                                    if (fieldRow.index === 0)
                                        forceActiveFocus()
                                }
                                Accessible.role: Accessible.EditableText
                                Accessible.name: fieldRow.modelData.label
                                Accessible.description: fieldRow.modelData.help || ""
                                onTextEdited: root.setValue(fieldRow.modelData.key, text)
                                onAccepted: root.submitSetup(false)

                                Text {
                                    anchors.verticalCenter: parent.verticalCenter
                                    visible: input.text.length === 0 && fieldRow.modelData.help
                                    text: fieldRow.modelData.help || ""
                                    color: Theme.dim
                                    elide: Text.ElideRight
                                    width: parent.width
                                    font.family: Theme.mono
                                    font.pixelSize: 11
                                }
                            }
                        }
                    }
                }

                Row {
                    spacing: 10
                    topPadding: 2

                    FlatButton {
                        text: root.backend.setupCanForce ? qsTr("SAVE ANYWAY") : qsTr("TEST & SAVE")
                        fontSize: 10
                        letterSpacing: 10 * 0.1
                        horizontalPadding: 12
                        implicitHeight: 26
                        enabled: !root.backend.setupBusy
                        contentColor: Theme.accent
                        hoverContentColor: Theme.accent
                        borderColor: Theme.accent
                        fillColor: Theme.surfaceRaised
                        hoverFillColor: Theme.rowSelected
                        Accessible.name: text
                        onClicked: root.submitSetup(root.backend.setupCanForce)
                    }

                    Text {
                        width: sheet.width - sheet.leftPadding - sheet.rightPadding - 140
                        height: 26
                        verticalAlignment: Text.AlignVCenter
                        text: root.backend.setupMessage.length > 0
                              ? root.backend.setupMessage
                              : qsTr("writes to ~/.config/cliamp/config.toml")
                        color: root.backend.setupFailed ? Theme.accent2 : Theme.dim
                        elide: Text.ElideRight
                        wrapMode: Text.NoWrap
                        font.family: Theme.mono
                        font.pixelSize: 9
                    }
                }
            }
        }
    }

    // ----- Browser ---------------------------------------------------------

    Item {
        id: browser
        anchors.top: providerStrip.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        visible: !root.showSetup

        Item {
            id: groupsPane
            anchors.top: parent.top
            anchors.bottom: parent.bottom
            anchors.left: parent.left
            width: Math.round(Math.max(140, Math.min(260, root.width * 0.33)))

            Rectangle {
                anchors.right: parent.right
                anchors.top: parent.top
                anchors.bottom: parent.bottom
                width: 1
                color: Theme.line
            }

            Item {
                id: groupsHeader
                anchors.top: parent.top
                anchors.left: parent.left
                anchors.right: parent.right
                height: 27

                Rectangle {
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.bottom: parent.bottom
                    height: 1
                    color: Theme.line
                }
                Text {
                    anchors.fill: parent
                    anchors.leftMargin: 12
                    anchors.rightMargin: 12
                    verticalAlignment: Text.AlignVCenter
                    visible: !root.backend.selectedProviderSearchable
                    text: qsTr("PLAYLISTS")
                    color: Theme.dim
                    font.family: Theme.mono
                    font.pixelSize: 9
                    font.letterSpacing: 9 * 0.14
                }
                // For searchable providers the header row becomes the search
                // input, keeping the pane's metrics unchanged.
                TextInput {
                    id: search
                    anchors.fill: parent
                    anchors.leftMargin: 12
                    anchors.rightMargin: 12
                    verticalAlignment: TextInput.AlignVCenter
                    activeFocusOnTab: true
                    visible: root.backend.selectedProviderSearchable
                    enabled: root.backend.connected && visible
                    color: Theme.fg
                    font.family: Theme.mono
                    font.pixelSize: 11
                    selectByMouse: true
                    clip: true
                    Accessible.role: Accessible.EditableText
                    Accessible.name: qsTr("Search selected provider")
                    Accessible.searchEdit: true
                    onAccepted: root.backend.searchProvider(text)

                    Text {
                        anchors.fill: parent
                        verticalAlignment: Text.AlignVCenter
                        visible: search.text.length === 0
                        text: qsTr("SEARCH")
                        color: Theme.dim
                        font.family: Theme.mono
                        font.pixelSize: 9
                        font.letterSpacing: 9 * 0.14
                    }
                }
            }

            ListView {
                id: groups
                anchors.top: groupsHeader.bottom
                anchors.bottom: parent.bottom
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.rightMargin: 1
                clip: true
                model: root.backend.providerPlaylistsModel
                reuseItems: true
                boundsBehavior: Flickable.StopAtBounds
                activeFocusOnTab: true
                keyNavigationWraps: false
                ScrollBar.vertical: ScrollBar {}
                Accessible.role: Accessible.List
                Accessible.name: qsTr("Provider playlists")

                function browseSelected(event) {
                    if (currentIndex < 0)
                        return
                    const playlist = root.backend.providerPlaylistsModel.get(currentIndex)
                    root.backend.browseProviderPlaylist(playlist.playlistId)
                    event.accepted = true
                }
                Keys.onReturnPressed: event => browseSelected(event)
                Keys.onEnterPressed: event => browseSelected(event)

                delegate: Item {
                    id: groupRow
                    required property int index
                    required property string playlistId
                    required property string name
                    required property int track_count

                    width: groups.width
                    height: 27
                    readonly property bool selected: root.backend.selectedPlaylist === playlistId
                    Accessible.role: Accessible.ListItem
                    Accessible.name: track_count > 0
                                     ? qsTr("%1, %2 tracks").arg(name).arg(track_count) : name
                    Accessible.selected: selected
                    Accessible.onPressAction: browse()

                    function browse() {
                        groups.currentIndex = index
                        groups.forceActiveFocus()
                        root.backend.browseProviderPlaylist(playlistId)
                    }

                    Rectangle {
                        anchors.fill: parent
                        color: groupRow.selected ? Theme.rowSelected
                                                 : mouse.containsMouse ? Theme.rowHover : "transparent"
                        border.width: groups.activeFocus && groups.currentIndex === groupRow.index ? 1 : 0
                        border.color: Theme.accent
                    }
                    Text {
                        anchors.left: parent.left
                        anchors.leftMargin: 12
                        anchors.right: count.left
                        anchors.rightMargin: 8
                        anchors.verticalCenter: parent.verticalCenter
                        text: groupRow.name
                        elide: Text.ElideRight
                        color: groupRow.selected ? Theme.accent : Theme.fg
                        font.family: Theme.mono
                        font.pixelSize: 11
                    }
                    Text {
                        id: count
                        anchors.right: parent.right
                        anchors.rightMargin: 12
                        anchors.verticalCenter: parent.verticalCenter
                        text: groupRow.track_count > 0 ? groupRow.track_count : ""
                        color: Theme.dim
                        font.family: Theme.mono
                        font.pixelSize: 11
                    }
                    MouseArea {
                        id: mouse
                        anchors.fill: parent
                        hoverEnabled: true
                        cursorShape: Qt.PointingHandCursor
                        onClicked: groupRow.browse()
                        onDoubleClicked: root.backend.loadSelectedPlaylist()
                    }
                }
            }
        }

        Item {
            id: tracksPane
            anchors.top: parent.top
            anchors.bottom: parent.bottom
            anchors.left: groupsPane.right
            anchors.right: parent.right

            Item {
                id: tracksHeader
                anchors.top: parent.top
                anchors.left: parent.left
                anchors.right: parent.right
                height: 27

                Rectangle {
                    anchors.left: parent.left
                    anchors.right: parent.right
                    anchors.bottom: parent.bottom
                    height: 1
                    color: Theme.line
                }
                Text {
                    anchors.left: parent.left
                    anchors.leftMargin: 12
                    anchors.right: loadAll.left
                    anchors.rightMargin: 8
                    anchors.verticalCenter: parent.verticalCenter
                    text: root.backend.selectedPlaylist.length > 0
                          ? root.backend.selectedPlaylist.toUpperCase()
                          : String(root.backend.selectedProvider).toUpperCase()
                    color: Theme.dim
                    elide: Text.ElideRight
                    font.family: Theme.mono
                    font.pixelSize: 9
                    font.letterSpacing: 9 * 0.14
                }
                FlatButton {
                    id: loadAll
                    anchors.right: parent.right
                    anchors.rightMargin: 12
                    anchors.verticalCenter: parent.verticalCenter
                    text: qsTr("LOAD")
                    fontSize: 9
                    letterSpacing: 9 * 0.1
                    horizontalPadding: 8
                    implicitHeight: 17
                    enabled: root.backend.selectedPlaylist.length > 0
                    contentColor: Theme.dim
                    hoverContentColor: Theme.accent
                    hoverBorderColor: Theme.accent
                    fillColor: "transparent"
                    Accessible.name: qsTr("Load selected playlist into the queue")
                    onClicked: root.backend.loadSelectedPlaylist()
                }
            }

            ListView {
                id: tracks
                anchors.top: tracksHeader.bottom
                anchors.bottom: parent.bottom
                anchors.left: parent.left
                anchors.right: parent.right
                clip: true
                model: root.backend.libraryTracksModel
                reuseItems: true
                boundsBehavior: Flickable.StopAtBounds
                activeFocusOnTab: true
                keyNavigationWraps: false
                ScrollBar.vertical: ScrollBar {}
                Accessible.role: Accessible.List
                Accessible.name: qsTr("Library tracks")

                function playSelected(event) {
                    if (currentIndex < 0)
                        return
                    root.backend.playLibraryTrack(currentIndex)
                    event.accepted = true
                }
                Keys.onReturnPressed: event => playSelected(event)
                Keys.onEnterPressed: event => playSelected(event)

                delegate: Item {
                    id: trackRow
                    required property int index
                    required property string title
                    required property string album
                    required property string artist
                    required property int duration_secs

                    width: tracks.width
                    height: 27
                    Accessible.role: Accessible.ListItem
                    Accessible.name: qsTr("%1 by %2").arg(title || qsTr("Unknown title"))
                        .arg(artist || qsTr("Unknown artist"))
                    Accessible.selected: tracks.currentIndex === index
                    Accessible.onPressAction: play()

                    function play() {
                        tracks.currentIndex = index
                        tracks.forceActiveFocus()
                        root.backend.playLibraryTrack(index)
                    }

                    Rectangle {
                        anchors.fill: parent
                        color: trackMouse.containsMouse ? Theme.rowHover : "transparent"
                        border.width: tracks.activeFocus && tracks.currentIndex === trackRow.index ? 1 : 0
                        border.color: Theme.accent
                    }
                    Text {
                        anchors.left: parent.left
                        anchors.leftMargin: 12
                        anchors.right: durationText.left
                        anchors.rightMargin: 8
                        anchors.verticalCenter: parent.verticalCenter
                        text: trackRow.album.length > 0
                              ? trackRow.album + " / " + (trackRow.title || qsTr("Unknown title"))
                              : (trackRow.title || qsTr("Unknown title"))
                        color: Theme.fg
                        elide: Text.ElideRight
                        font.family: Theme.mono
                        font.pixelSize: 11
                    }
                    Text {
                        id: durationText
                        anchors.right: parent.right
                        anchors.rightMargin: 12
                        anchors.verticalCenter: parent.verticalCenter
                        text: trackRow.duration_secs > 0
                              ? root.backend.formatDuration(trackRow.duration_secs) : qsTr("live")
                        color: Theme.dim
                        font.family: Theme.mono
                        font.pixelSize: 11
                    }
                    MouseArea {
                        id: trackMouse
                        anchors.fill: parent
                        hoverEnabled: true
                        cursorShape: Qt.PointingHandCursor
                        onClicked: {
                            tracks.currentIndex = trackRow.index
                            tracks.forceActiveFocus()
                        }
                        onDoubleClicked: trackRow.play()
                    }
                }

                Text {
                    anchors.centerIn: parent
                    visible: tracks.count === 0
                    text: root.backend.selectedPlaylist.length > 0
                          ? qsTr("No tracks") : qsTr("Select a playlist")
                    color: Theme.dim
                    font.family: Theme.mono
                    font.pixelSize: 11
                }
            }
        }
    }
}
