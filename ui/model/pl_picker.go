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

func (m *Model) openPlaylistPicker(tracks []playlist.Track, title string) {
	if m.localProvider == nil {
		m.status.Warning("Local playlists are unavailable", statusTTLDefault)
		return
	}
	lists, err := m.localProvider.Playlists()
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Playlist list failed: %s", err)
		return
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
	m.refreshChrome()
	m.applyHeightMode()
	m.plPickerMaybeAdjustScroll(m.plPickerVisible())
}

func (m *Model) closePlaylistPicker() {
	m.plPicker = playlistPickerState{}
	m.refreshChrome()
	m.applyHeightMode()
}

func (m *Model) plPickerCount() int {
	return len(m.plPicker.playlists) + 1
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

	items := make([]string, len(m.plPicker.playlists)+1)
	for i, pl := range m.plPicker.playlists {
		items[i] = playlistLabel("", pl)
	}
	items[len(items)-1] = "+ New Playlist..."

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
	list := windowList(items, m.plPicker.cursor, m.plPicker.scroll, max(0, budget-1))
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
	case "enter":
		if m.plPicker.cursor < len(m.plPicker.playlists) {
			if m.writePickerTracks(m.plPicker.playlists[m.plPicker.cursor].Name) {
				m.closePlaylistPicker()
			}
			return nil
		}
		m.plPicker.screen = plPickerNewName
		m.plPicker.newName = ""
		m.plPicker.cursor = 0
		m.plPicker.scroll = 0
	}
	return nil
}

func (m *Model) handlePlaylistPickerNewNameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.plPicker.screen = plPickerChoose
		m.plPicker.cursor = len(m.plPicker.playlists)
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case tea.KeyEnter:
		name := strings.TrimSpace(m.plPicker.newName)
		if name == "" {
			m.plPicker.inputErr = "Playlist name is required."
			return nil
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
