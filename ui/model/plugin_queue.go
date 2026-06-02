package model

import (
	tea "charm.land/bubbletea/v2"

	"cliamp/playlist"
	"cliamp/resolve"
)

// PluginQueueMsg is sent by Lua plugins (cliamp.queue.*) to mutate the queue.
// Mutations are routed through the Update loop rather than applied directly
// from the plugin goroutine so the model's derived state (cursor, current
// index, playback) stays consistent. Indices are 0-based.
type PluginQueueMsg struct {
	Op    string // "add" | "jump" | "remove" | "move"
	Path  string // add
	Index int    // jump, remove, move (from)
	To    int    // move (to)
}

// pluginQueueAddedMsg carries tracks resolved for a cliamp.queue.add() call
// back to the Update loop, which appends them.
type pluginQueueAddedMsg struct{ tracks []playlist.Track }

// handlePluginQueue applies a queue mutation requested by a plugin and returns
// any follow-up command (resolve for add, playback for jump).
func (m *Model) handlePluginQueue(msg PluginQueueMsg) tea.Cmd {
	switch msg.Op {
	case "add":
		return resolvePluginAddCmd(msg.Path)

	case "jump":
		if msg.Index < 0 || msg.Index >= m.playlist.Len() {
			return nil
		}
		m.scrobbleCurrent()
		m.playlist.SetIndex(msg.Index)
		cmd := m.playCurrentTrack()
		m.notifyPlayback()
		return cmd

	case "remove":
		m.removeIndex(msg.Index)
		return nil

	case "move":
		if m.playlist.Move(msg.Index, msg.To) {
			m.adjustScroll()
		}
		return nil
	}
	return nil
}

// removeIndex removes the track at idx, mirroring the side effects of the
// interactive delete: stop playback if the active track was removed and clamp
// the playlist cursor.
func (m *Model) removeIndex(idx int) {
	if idx < 0 || idx >= m.playlist.Len() {
		return
	}
	wasActive := idx == m.playlist.Index()
	if !m.playlist.Remove(idx) {
		return
	}
	if wasActive {
		m.player.Stop()
		m.player.ClearPreload()
	}
	if newLen := m.playlist.Len(); newLen == 0 {
		m.plCursor = 0
	} else if m.plCursor >= newLen {
		m.plCursor = newLen - 1
	}
	m.adjustScroll()
	m.notifyPlayback()
}

// resolvePluginAddCmd resolves a plugin-supplied path/URL off the UI thread,
// reusing the same pipeline as CLI positional arguments, and hands the tracks
// back via pluginQueueAddedMsg.
func resolvePluginAddCmd(path string) tea.Cmd {
	return func() tea.Msg {
		res, err := resolve.Args([]string{path})
		if err != nil {
			return pluginQueueAddedMsg{}
		}
		tracks := res.Tracks
		if len(res.Pending) > 0 {
			if remote, rerr := resolve.Remote(res.Pending); rerr == nil {
				tracks = append(tracks, remote...)
			}
		}
		return pluginQueueAddedMsg{tracks: tracks}
	}
}
