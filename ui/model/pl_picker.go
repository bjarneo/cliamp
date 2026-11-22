package model

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
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
	m.plPicker = playlistPickerState{
		visible:   true,
		screen:    plPickerChoose,
		playlists: playlistTargets(m.localProvider, lists),
		tracks:    append([]playlist.Track(nil), tracks...),
		title:     title,
	}
	m.refreshChrome()
	m.applyHeightMode()
	m.plPickerMaybeAdjustScroll(m.plPickerVisible())
}

// playlistTargets returns the lists of prov that can take new tracks. A
// provider whose list holds entries that reject adds reports them through
// provider.PlaylistTargetFilter. The local provider rejects its virtual
// Favorites and Recently Played lists this way.
func playlistTargets(prov playlist.Provider, lists []playlist.PlaylistInfo) []playlist.PlaylistInfo {
	targets, ok := prov.(provider.PlaylistTargetFilter)
	if !ok {
		return lists
	}
	filtered := make([]playlist.PlaylistInfo, 0, len(lists))
	for _, pl := range lists {
		if targets.CanAddToPlaylist(pl) {
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

func (m Model) plPickerItems() []string {
	n := len(m.plPicker.playlists)
	items := make([]string, 0, n+2)
	for _, pl := range m.plPicker.playlists {
		items = append(items, playlistLabel("", pl))
	}
	if n > 0 {
		items = append(items, "")
	}
	items = append(items, "+ New Playlist...")
	return items
}

func (m Model) plPickerBlankIdx() int {
	if len(m.plPicker.playlists) == 0 {
		return -1
	}
	return len(m.plPicker.playlists)
}

func (m Model) plPickerNewIdx() int {
	return len(m.plPickerItems()) - 1
}

func (m *Model) plPickerCount() int {
	return len(m.plPickerItems())
}

func (m *Model) plPickerVisible() int {
	return m.effectivePlaylistVisible()
}

func (m *Model) plPickerSkipBlank(idx int) int {
	blank := m.plPickerBlankIdx()
	if blank >= 0 && idx == blank {
		return m.plPickerNewIdx()
	}
	return idx
}

func (m *Model) plPickerStepCursor(delta int) {
	count := m.plPickerCount()
	if count == 0 {
		return
	}
	blank := m.plPickerBlankIdx()
	c := m.plPicker.cursor
	if delta > 0 {
		if c < count-1 {
			c++
			if blank >= 0 && c == blank {
				c++
			}
		} else {
			c = 0
		}
	} else {
		if c > 0 {
			c--
			if blank >= 0 && c == blank {
				c--
			}
		} else {
			c = count - 1
			if blank >= 0 && c == blank {
				c--
			}
		}
	}
	m.plPicker.cursor = c
}

func (m *Model) plPickerMaybeAdjustScroll(visible int) {
	m.plPicker.cursor = m.plPickerSkipBlank(m.plPicker.cursor)
	clampScroll(&m.plPicker.cursor, &m.plPicker.scroll, m.plPickerCount(), visible)
	m.plPicker.cursor = m.plPickerSkipBlank(m.plPicker.cursor)
}

func plPickerTrackDetail(tracks []playlist.Track, title string) string {
	if title != "" {
		if after, ok := strings.CutPrefix(title, "Track: "); ok {
			return after
		}
		return title
	}
	switch n := len(tracks); {
	case n == 0:
		return ""
	case n == 1:
		return tracks[0].DisplayName()
	default:
		return fmt.Sprintf("%d tracks selected", n)
	}
}

func (m Model) plPickerHeaderLine() string {
	if m.plPicker.screen == plPickerNewName {
		return m.promptHeader("playlist-picker-name", "New Playlist", m.plPicker.newName)
	}
	label := "Add to playlist"
	if detail := plPickerTrackDetail(m.plPicker.tracks, m.plPicker.title); detail != "" {
		label += " : " + terminalText(detail)
	}
	return sepHeader(label, m.layout.panelWidth)
}

func (m Model) renderPlaylistPickerBody() string {
	budget := m.effectivePlaylistVisible()
	if m.plPicker.screen == plPickerNewName {
		msg := "Create an empty playlist."
		if n := len(m.plPicker.tracks); n == 1 {
			msg = "Create and add: " + truncate(terminalText(m.plPicker.tracks[0].DisplayName()), max(1, m.layout.panelWidth-18))
		} else if n > 1 {
			msg = fmt.Sprintf("Create and add %d tracks.", n)
		}
		lines := []string{dimStyle.Render("  " + msg)}
		if m.plPicker.inputErr != "" {
			lines = append(lines, errorStyle.Render("  "+m.plPicker.inputErr))
		}
		return bodyLines(lines, budget)
	}

	return plPickerWindowList(m.plPickerItems(), m.plPicker.cursor, m.plPicker.scroll, budget)
}

func plPickerWindowList(items []string, cursor, scroll, budget int) string {
	if budget <= 0 {
		return ""
	}
	lines := make([]string, 0, budget)
	for i := scroll; i < len(items) && len(lines) < budget; i++ {
		if items[i] == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, cursorLine(items[i], i == cursor))
	}
	return strings.Join(padLines(lines, budget, len(lines)), "\n")
}

func (m *Model) handlePlaylistPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.plPicker.screen == plPickerNewName {
		return m.handlePlaylistPickerNewNameKey(msg)
	}

	count := m.plPickerCount()
	switch msg.String() {
	case "esc", "backspace", "q":
		m.closePlaylistPicker()
	case "ctrl+x":
		m.toggleExpandedView()
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case "up", "k":
		m.plPickerStepCursor(-1)
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case "down", "j":
		m.plPickerStepCursor(1)
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case "pgup", "ctrl+u":
		if m.plPicker.cursor > 0 {
			visible := m.plPickerVisible()
			prev := m.plPicker.cursor
			for range min(prev, visible) {
				m.plPickerStepCursor(-1)
			}
			m.plPickerMaybeAdjustScroll(visible)
		}
	case "pgdown", "ctrl+d":
		if m.plPicker.cursor < count-1 {
			visible := m.plPickerVisible()
			for range visible {
				if m.plPicker.cursor >= count-1 {
					break
				}
				m.plPickerStepCursor(1)
			}
			m.plPickerMaybeAdjustScroll(visible)
		}
	case "home", "g":
		m.plPicker.cursor = 0
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
	case "end", "G":
		if count > 0 {
			m.plPicker.cursor = m.plPickerNewIdx()
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
		m.plPicker.cursor = m.plPickerNewIdx()
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
	return provider.AddTracks(context.Background(), m.localProvider, name, tracks)
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
			m.plMgrTracksMaybeAdjustScroll(m.effectivePlaylistVisible())
		}
	}
}
