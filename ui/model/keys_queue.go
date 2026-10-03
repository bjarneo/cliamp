package model

import tea "charm.land/bubbletea/v2"

// normalizeQueueOverlay keeps the selected queue row and scroll window valid
// after queue mutations, including mutations made while the overlay is hidden.
func (m *Model) normalizeQueueOverlay() {
	if m.playlist == nil {
		m.queue.cursor = 0
		m.queue.scroll = 0
		return
	}
	count := m.playlist.QueueLen()
	if count == 0 {
		m.queue.cursor = 0
		m.queue.scroll = 0
		return
	}
	m.queue.cursor = min(max(0, m.queue.cursor), count-1)
	visible := m.effectivePlaylistVisible()
	if visible <= 0 {
		m.queue.scroll = min(max(0, m.queue.scroll), count-1)
		if m.queue.cursor < m.queue.scroll {
			m.queue.scroll = m.queue.cursor
		}
		return
	}
	clampScroll(&m.queue.cursor, &m.queue.scroll, count, visible)
}

// handleQueueKey processes key presses while the queue manager overlay is open.
func (m *Model) handleQueueKey(msg tea.KeyPressMsg) tea.Cmd {
	qLen := m.playlist.QueueLen()

	switch msg.String() {
	case "?":
		m.openKeymap()
	case "ctrl+x":
		m.toggleExpandedView()
		m.normalizeQueueOverlay()
	case "up", "k":
		if m.queue.cursor > 0 {
			m.queue.cursor--
		} else if qLen > 0 {
			m.queue.cursor = qLen - 1
		}
		m.normalizeQueueOverlay()

	case "down", "j":
		if m.queue.cursor < qLen-1 {
			m.queue.cursor++
		} else if qLen > 0 {
			m.queue.cursor = 0
		}
		m.normalizeQueueOverlay()
	case "shift+up":
		moved := false
		if m.queue.cursor > 0 {
			if m.playlist.MoveQueue(m.queue.cursor, m.queue.cursor-1) {
				m.queue.cursor--
				moved = true
			}
		}
		m.normalizeQueueOverlay()
		if moved {
			return m.rearmStalePreload()
		}
	case "shift+down":
		moved := false
		if m.queue.cursor < qLen-1 {
			if m.playlist.MoveQueue(m.queue.cursor, m.queue.cursor+1) {
				m.queue.cursor++
				moved = true
			}
		}
		m.normalizeQueueOverlay()
		if moved {
			return m.rearmStalePreload()
		}
	case "d":
		removed := false
		if qLen > 0 {
			snapshot := m.playlist.Snapshot()
			m.playlist.RemoveQueueAt(m.queue.cursor)
			m.recordPlaylistUndo(playlistUndo{snapshot: snapshot})
			removed = true
			m.status.Show("Removed queued track (Ctrl+Z to undo)", statusTTLDefault)
		}
		m.normalizeQueueOverlay()
		if removed {
			return m.rearmStalePreload()
		}
	case "c":
		cleared := false
		if qLen > 0 {
			snapshot := m.playlist.Snapshot()
			m.playlist.ClearQueue()
			m.recordPlaylistUndo(playlistUndo{snapshot: snapshot})
			cleared = true
			m.normalizeQueueOverlay()
			m.status.Show("Cleared queue (Ctrl+Z to undo)", statusTTLDefault)
		}
		m.queue.visible = false
		if cleared {
			return m.rearmStalePreload()
		}
	case "esc", "A":
		m.queue.visible = false
	}
	return nil
}
