package model

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

func (m *Model) openPlaylistPicker(tracks []playlist.Track, title string) tea.Cmd {
	if m.localProvider == nil {
		m.status.Warning("Local playlists are unavailable", statusTTLDefault)
		return nil
	}
	lists, err := m.localProvider.Playlists()
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Playlist list failed: %s", err)
		return nil
	}
	playlists := make([]playlist.PlaylistInfo, 0, len(lists))
	for _, pl := range lists {
		if pl.Name != history.PlaylistName {
			playlists = append(playlists, pl)
		}
	}
	m.plPicker = playlistPickerState{
		visible:   true,
		screen:    plPickerChoose,
		playlists: playlists,
		tracks:    append([]playlist.Track(nil), tracks...),
		title:     title,
	}
	// Append a remote section when every selected track belongs to one
	// provider that can write playlists.
	var cmd tea.Cmd
	if prov := m.playlistWriterForTracks(tracks); prov != nil {
		m.plPicker.remoteProv = prov
		m.plPicker.remoteName = prov.Name()
		if m.provider == prov && len(m.providerLists) > 0 {
			m.plPicker.remote = filterRemotePickerPlaylists(m.providerLists)
		} else {
			m.plPicker.remoteLoading = true
			cmd = fetchPlPickerRemoteCmd(prov)
		}
	}
	m.refreshChrome()
	m.applyHeightMode()
	m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	return cmd
}

// playlistWriterForTracks returns the provider that owns every track's URI
// scheme and implements provider.PlaylistWriter, or nil.
func (m *Model) playlistWriterForTracks(tracks []playlist.Track) playlist.Provider {
	if len(tracks) == 0 || trackScheme(tracks[0].Path) == "" {
		return nil
	}
	for _, t := range tracks[1:] {
		if trackScheme(t.Path) != trackScheme(tracks[0].Path) {
			return nil
		}
	}
	ownsAndWrites := func(p playlist.Provider) bool {
		if p == nil || !providerOwnsPath(p, tracks[0].Path) {
			return false
		}
		_, ok := p.(provider.PlaylistWriter)
		return ok
	}
	if ownsAndWrites(m.provider) {
		return m.provider
	}
	for _, pe := range m.providers {
		if ownsAndWrites(pe.Provider) {
			return pe.Provider
		}
	}
	return nil
}

// isSyntheticProviderRow reports whether a provider list ID belongs to a
// synthetic Library row ("YOUR MUSIC", "TOP TRACKS", … — any ID containing a
// space) rather than a real playlist ID that write endpoints accept.
func isSyntheticProviderRow(id string) bool {
	return strings.Contains(id, " ")
}

// filterRemotePickerPlaylists drops synthetic entries whose IDs are not real
// playlist IDs ("YOUR MUSIC", "TOP TRACKS", …).
func filterRemotePickerPlaylists(lists []playlist.PlaylistInfo) []playlist.PlaylistInfo {
	filtered := make([]playlist.PlaylistInfo, 0, len(lists))
	for _, pl := range lists {
		if !isSyntheticProviderRow(pl.ID) {
			filtered = append(filtered, pl)
		}
	}
	return filtered
}

func (m *Model) closePlaylistPicker() {
	m.plPicker = playlistPickerState{}
	m.refreshChrome()
	m.applyHeightMode()
}

// plPickerItem is one rendered row of the picker. Headers and the loading row
// are not selectable.
type plPickerItem struct {
	header   bool
	label    string
	playlist playlist.PlaylistInfo
	isNew    bool
	remote   bool
}

// plPickerItems builds the rendered rows: local playlists and "+ New" first,
// then the remote section when one was appended.
func (m Model) plPickerItems() []plPickerItem {
	var items []plPickerItem
	if m.plPicker.remoteName != "" {
		items = append(items, plPickerItem{header: true, label: "Local Playlists"})
	}
	for _, pl := range m.plPicker.playlists {
		items = append(items, plPickerItem{label: playlistLabel("", pl), playlist: pl})
	}
	items = append(items, plPickerItem{isNew: true, label: "+ New Playlist..."})
	if m.plPicker.remoteName != "" {
		items = append(items, plPickerItem{header: true, label: m.plPicker.remoteName + " Playlists"})
		if m.plPicker.remoteLoading {
			items = append(items, plPickerItem{header: true, label: "Loading " + m.plPicker.remoteName + " playlists…"})
		} else {
			for _, pl := range m.plPicker.remote {
				items = append(items, plPickerItem{label: playlistLabel("", pl), playlist: pl, remote: true})
			}
			items = append(items, plPickerItem{isNew: true, remote: true, label: "+ New " + m.plPicker.remoteName + " Playlist..."})
		}
	}
	return items
}

// plPickerItemAt maps the cursor (an index over selectable rows) to its item.
func (m Model) plPickerItemAt(cursor int) (plPickerItem, bool) {
	ord := 0
	for _, item := range m.plPickerItems() {
		if item.header {
			continue
		}
		if ord == cursor {
			return item, true
		}
		ord++
	}
	return plPickerItem{}, false
}

func (m *Model) plPickerCount() int {
	count := 0
	for _, item := range m.plPickerItems() {
		if !item.header {
			count++
		}
	}
	return count
}

func (m *Model) plPickerVisible() int {
	if m.plPicker.screen == plPickerChoose {
		return max(1, m.effectivePlaylistVisible()-1)
	}
	return m.effectivePlaylistVisible()
}

func (m *Model) plPickerMaybeAdjustScroll(visible int) {
	clampScroll(&m.plPicker.cursor, &m.plPicker.scroll, m.plPickerCount(), visible)
}

func (m Model) plPickerHeaderLine() string {
	if m.plPicker.screen == plPickerNewName {
		return m.promptHeader("playlist-picker-name", "New Playlist", m.plPicker.newName)
	}
	return sepHeaderN("Write to Playlist", m.plPicker.cursor+1, m.plPickerCount())
}

func (m Model) plPickerHelpLine() string {
	if m.plPicker.screen == plPickerNewName {
		return m.commandHelp(commandModePlaylistPickerInput)
	}
	return m.commandHelp(commandModePlaylistPicker)
}

func (m Model) renderPlaylistPickerBody() string {
	budget := m.effectivePlaylistVisible()
	if m.plPicker.screen == plPickerNewName {
		msg := "Create an empty playlist."
		if n := len(m.plPicker.tracks); n == 1 {
			msg = "Create and add: " + truncate(m.plPicker.tracks[0].DisplayName(), max(1, ui.PanelWidth-18))
		} else if n > 1 {
			msg = fmt.Sprintf("Create and add %d tracks.", n)
		}
		lines := []string{dimStyle.Render("  " + msg)}
		if m.plPicker.inputErr != "" {
			lines = append(lines, errorStyle.Render("  "+m.plPicker.inputErr))
		}
		return bodyLines(lines, budget)
	}

	var lines []string
	ord := 0
	for _, item := range m.plPickerItems() {
		if len(lines) >= budget {
			break
		}
		if item.header {
			lines = append(lines, dimStyle.Render(labeledSeparator("  ", item.label)))
			continue
		}
		lines = append(lines, cursorLine(item.label, ord == m.plPicker.cursor))
		ord++
	}

	var head string
	switch n := len(m.plPicker.tracks); {
	case m.plPicker.title != "":
		head = m.plPicker.title
	case n == 0:
		head = "No tracks selected. Choose + New Playlist to create an empty one."
	case n == 1:
		head = "Track: " + m.plPicker.tracks[0].DisplayName()
	default:
		head = fmt.Sprintf("%d tracks selected", n)
	}
	head = dimStyle.Render("  " + truncate(head, max(1, ui.PanelWidth-2)))
	list := strings.Join(fitLines(lines, max(0, budget-1)), "\n")
	return strings.Join([]string{head, list}, "\n")
}

func (m *Model) handlePlaylistPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.plPicker.screen == plPickerNewName {
		return m.handlePlaylistPickerNewNameKey(msg)
	}

	count := m.plPickerCount()
	switch msg.String() {
	case "ctrl+c":
		m.closePlaylistPicker()
		return m.quit()
	case "esc", "backspace", "q":
		m.closePlaylistPicker()
	case "ctrl+x":
		m.toggleExpandedView()
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case "up", "k":
		if m.plPicker.cursor > 0 {
			m.plPicker.cursor--
		} else if count > 0 {
			m.plPicker.cursor = count - 1
		}
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case "down", "j":
		if m.plPicker.cursor < count-1 {
			m.plPicker.cursor++
		} else if count > 0 {
			m.plPicker.cursor = 0
		}
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case "pgup", "ctrl+u":
		if m.plPicker.cursor > 0 {
			visible := m.plPickerVisible()
			m.plPicker.cursor -= min(m.plPicker.cursor, visible)
			m.plPickerMaybeAdjustScroll(visible)
		}
	case "pgdown", "ctrl+d":
		if m.plPicker.cursor < count-1 {
			visible := m.plPickerVisible()
			m.plPicker.cursor = min(count-1, m.plPicker.cursor+visible)
			m.plPickerMaybeAdjustScroll(visible)
		}
	case "home", "g":
		m.plPicker.cursor = 0
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case "end", "G":
		if count > 0 {
			m.plPicker.cursor = count - 1
		}
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case "p":
		if m.plPicker.cursor < len(m.plPicker.playlists) {
			if m.prependPickerTracks(m.plPicker.playlists[m.plPicker.cursor].Name) {
				m.closePlaylistPicker()
			}
			return nil
		}
		// On the new-playlist row there is nothing to prepend to, so this is
		// the same request as Enter.
		m.plPicker.screen = plPickerNewName
		m.plPicker.newName = ""
		m.plPicker.cursor = 0
	case "enter":
		item, ok := m.plPickerItemAt(m.plPicker.cursor)
		if !ok {
			return nil
		}
		if item.isNew {
			m.plPicker.screen = plPickerNewName
			m.plPicker.newNameRemote = item.remote
			m.plPicker.newName = ""
			m.plPicker.cursor = 0
			m.plPicker.scroll = 0
			return nil
		}
		if item.remote {
			// Optimistic close; the result arrives via pickerRemoteWriteMsg.
			prov, tracks := m.plPicker.remoteProv, m.plPicker.tracks
			m.closePlaylistPicker()
			return addRemotePickerTracksCmd(m.newLikeContext(), prov, item.playlist.ID, item.playlist.Name, tracks)
		}
		if m.writePickerTracks(item.playlist.Name) {
			m.closePlaylistPicker()
		}
		return nil
	}
	return nil
}

func (m *Model) handlePlaylistPickerNewNameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.plPicker.screen = plPickerChoose
		// Back to the "+ New ..." row that opened this input: the local row
		// sits right after the local playlists; the remote row is last.
		if m.plPicker.newNameRemote {
			m.plPicker.cursor = max(0, m.plPickerCount()-1)
		} else {
			m.plPicker.cursor = len(m.plPicker.playlists)
		}
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case tea.KeyEnter:
		name := strings.TrimSpace(m.plPicker.newName)
		if name == "" {
			m.plPicker.inputErr = "Playlist name is required."
			return nil
		}
		if m.plPicker.newNameRemote {
			// Optimistic close; the result arrives via pickerRemoteWriteMsg.
			tracks := m.plPicker.tracks
			prov := m.plPicker.remoteProv
			m.closePlaylistPicker()
			return createRemotePickerPlaylistCmd(m.newLikeContext(), prov, name, tracks)
		}
		if m.createPickerPlaylist(name) {
			m.closePlaylistPicker()
		}
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("playlist-picker-name", &m.plPicker.newName, " ")
			m.plPicker.inputErr = ""
		} else if m.editText("playlist-picker-name", &m.plPicker.newName, msg) {
			m.plPicker.inputErr = ""
		}
	}
	return nil
}

func (m *Model) createPickerPlaylist(name string) bool {
	c, ok := m.localProvider.(provider.PlaylistCreator)
	if !ok {
		m.plPicker.inputErr = "Playlist creation is not supported."
		return false
	}
	id, err := c.CreatePlaylist(context.Background(), name)
	if err != nil {
		m.plPicker.inputErr = "Create failed: " + err.Error()
		return false
	}
	if len(m.plPicker.tracks) == 0 {
		m.status.Showf(statusTTLDefault, "Created %q", name)
		m.refreshPlaylistManagerAfterWrite(id)
		return true
	}
	return m.writePickerTracks(id)
}

func (m *Model) writePickerTracks(name string) bool {
	added, skipped, err := m.writeTracksToPlaylist(name, m.plPicker.tracks)
	if err != nil {
		m.plPicker.inputErr = "Write failed: " + err.Error()
		return false
	}
	switch {
	case added > 0 && skipped > 0:
		m.status.Warningf(statusTTLBatch, "Added %d to %q, skipped %d duplicates", added, name, skipped)
	case added > 0:
		m.status.Showf(statusTTLDefault, "Added %d to %q", added, name)
	case skipped > 0:
		m.status.Warningf(statusTTLDefault, "Skipped %d duplicates in %q", skipped, name)
	default:
		m.status.Warningf(statusTTLDefault, "Nothing added to %q", name)
	}
	m.refreshPlaylistManagerAfterWrite(name)
	return true
}

// prependPickerTracks writes the picker's tracks to the front of a playlist.
func (m *Model) prependPickerTracks(name string) bool {
	pr, ok := m.localProvider.(provider.PlaylistPrepender)
	if !ok {
		m.plPicker.inputErr = "Adding to the start is not supported here"
		return false
	}
	added, moved, skipped, err := pr.PrependTracksToPlaylist(context.Background(), name, m.plPicker.tracks)
	if err != nil {
		m.plPicker.inputErr = "Write failed: " + err.Error()
		return false
	}
	switch {
	case added+moved == 0 && skipped > 0:
		m.status.Warningf(statusTTLDefault, "Nothing added to the start of %q, skipped %d", name, skipped)
	case added+moved == 0:
		m.status.Warningf(statusTTLDefault, "Nothing added to the start of %q", name)
	case moved > 0 && skipped > 0:
		m.status.Warningf(statusTTLBatch, "Put %d at the start of %q, moved %d up, skipped %d", added, name, moved, skipped)
	case moved > 0:
		m.status.Showf(statusTTLBatch, "Put %d at the start of %q, moved %d up", added, name, moved)
	case skipped > 0:
		m.status.Warningf(statusTTLBatch, "Put %d at the start of %q, skipped %d", added, name, skipped)
	default:
		m.status.Showf(statusTTLDefault, "Put %d at the start of %q", added, name)
	}
	m.refreshPlaylistManagerAfterWrite(name)
	return true
}

func (m *Model) writeTracksToPlaylist(name string, tracks []playlist.Track) (added, skipped int, err error) {
	if len(tracks) == 0 {
		return 0, 0, nil
	}
	if bw, ok := m.localProvider.(provider.PlaylistBatchWriter); ok {
		return bw.AddTracksToPlaylist(context.Background(), name, tracks)
	}
	w, ok := m.localProvider.(provider.PlaylistWriter)
	if !ok {
		return 0, 0, fmt.Errorf("playlist writes are not supported")
	}
	for _, track := range tracks {
		if err := w.AddTrackToPlaylist(context.Background(), name, track); err != nil {
			return added, skipped, err
		}
		added++
	}
	return added, skipped, nil
}

func (m *Model) refreshPlaylistManagerAfterWrite(name string) {
	if !m.plManager.visible {
		return
	}
	m.plMgrRefreshList()
	if m.plManager.screen == plMgrScreenTracks && m.plManager.selPlaylist == name {
		if tracks, err := m.localProvider.Tracks(name); err == nil {
			m.plMgrLoadTracks(tracks)
			m.plMgrRecomputeFilter()
			m.plMgrTracksMaybeAdjustScroll(m.plMgrTracksVisible())
		}
	}
}
