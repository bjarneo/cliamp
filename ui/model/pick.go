package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// providerPickLoadedMsg carries every dialog of the provider and the rows of
// the current filter, so the picker can mark the latter.
type providerPickLoadedMsg struct {
	dialogs  []playlist.PlaylistInfo
	selected []playlist.PlaylistInfo
	err      error
}

// startChatPicker swaps the provider pane into selection mode. The pane shows
// every dialog with a checkbox and keeps the current filter marked; enter
// writes the checked ids through provider.ChatFilter.
func (m *Model) startChatPicker() tea.Cmd {
	cf, ok := m.provider.(provider.ChatFilter)
	if !ok {
		return nil
	}
	m.provPane.pickPrev = m.provPane.lists
	m.provPane.pickCur = m.provPane.cursor
	m.provPane.pickScr = m.provPane.scroll
	m.provPane.lists = nil
	m.provPane.cursor, m.provPane.scroll = 0, 0
	m.provPane.picking = true
	m.provPane.pickLoad = true
	m.provPane.loading = true
	m.provPane.picked = map[string]bool{}
	m.status.Show("Pick chats: space marks, enter saves, esc cancels.", statusTTLShort)

	p := m.provider
	return func() tea.Msg {
		dialogs, err := cf.Dialogs()
		if err != nil {
			return providerPickLoadedMsg{err: err}
		}
		var selected []playlist.PlaylistInfo
		if len(cf.Chats()) > 0 {
			selected, _ = p.Playlists()
		}
		return providerPickLoadedMsg{dialogs: dialogs, selected: selected}
	}
}

// handleChatPickerLoaded fills the pane with the dialogs and marks the current
// filter. A failure puts the pane back and reports the error.
func (m *Model) handleChatPickerLoaded(msg providerPickLoadedMsg) {
	if !m.provPane.picking {
		return
	}
	m.provPane.pickLoad = false
	m.provPane.loading = false
	if msg.err != nil {
		m.err = msg.err
		m.cancelChatPicker()
		return
	}
	m.provPane.lists = msg.dialogs
	m.provPane.cursor, m.provPane.scroll = 0, 0
	for _, info := range msg.selected {
		m.provPane.picked[info.ID] = true
	}
}

// handleChatPickerKey owns the keys while the picker is open.
func (m *Model) handleChatPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.provPane.pickLoad {
		if msg.String() == "esc" {
			m.cancelChatPicker()
		}
		return nil
	}
	switch msg.String() {
	case "esc", "q":
		m.cancelChatPicker()
	case "up", "k":
		if m.provPane.cursor > 0 {
			m.provPane.cursor--
		} else {
			m.provPane.cursor = len(m.provPane.lists) - 1
		}
	case "down", "j":
		if m.provPane.cursor < len(m.provPane.lists)-1 {
			m.provPane.cursor++
		} else {
			m.provPane.cursor = 0
		}
	case "home", "g":
		m.provPane.cursor = 0
	case "end", "G":
		if len(m.provPane.lists) > 0 {
			m.provPane.cursor = len(m.provPane.lists) - 1
		}
	case "space", " ":
		if len(m.provPane.lists) > 0 {
			id := m.provPane.lists[m.provPane.cursor].ID
			m.provPane.picked[id] = !m.provPane.picked[id]
		}
	case "a":
		for _, info := range m.provPane.lists {
			m.provPane.picked[info.ID] = true
		}
	case "n":
		m.provPane.picked = map[string]bool{}
	case "enter":
		return m.applyChatPicker()
	}
	return nil
}

// cancelChatPicker restores the rows the pane had before picking.
func (m *Model) cancelChatPicker() {
	m.provPane.picking = false
	m.provPane.pickLoad = false
	m.provPane.loading = false
	m.provPane.lists = m.provPane.pickPrev
	m.provPane.cursor = m.provPane.pickCur
	m.provPane.scroll = m.provPane.pickScr
	m.provPane.picked = nil
	m.provPane.pickPrev = nil
}

// applyChatPicker writes the checked ids and keeps those rows. Checking every
// chat, or none, means no filter: the pane keeps the full list and new
// dialogs show up later.
func (m *Model) applyChatPicker() tea.Cmd {
	cf, ok := m.provider.(provider.ChatFilter)
	if !ok {
		m.cancelChatPicker()
		return nil
	}
	rows := make([]playlist.PlaylistInfo, 0, len(m.provPane.picked))
	ids := make([]string, 0, len(m.provPane.picked))
	for _, info := range m.provPane.lists {
		if m.provPane.picked[info.ID] {
			ids = append(ids, info.ID)
			rows = append(rows, info)
		}
	}
	everything := len(ids) == 0 || len(ids) == len(m.provPane.lists)
	if everything {
		ids = nil
		rows = m.provPane.lists
	}
	if err := cf.SetChats(ids); err != nil {
		m.err = err
		m.cancelChatPicker()
		return nil
	}
	m.provPane.picking = false
	m.provPane.pickLoad = false
	m.provPane.loading = false
	m.provPane.picked = nil
	m.provPane.pickPrev = nil
	m.replaceProviderLists(rows)
	switch {
	case everything:
		m.status.Show("Showing every chat.", statusTTLShort)
	case len(ids) == 1:
		m.status.Show("Showing 1 chat.", statusTTLShort)
	default:
		m.status.Showf(statusTTLShort, "Showing %d chats.", len(ids))
	}
	return nil
}
