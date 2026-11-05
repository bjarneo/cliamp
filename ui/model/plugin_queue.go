package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

// PluginQueueMsg is sent by Lua plugins (cliamp.queue.*) to mutate the queue.
// Mutations are routed through the Update loop rather than applied directly
// from the plugin goroutine so the model's derived state (cursor, current
// index, playback) stays consistent. Indices are 0-based.
type PluginQueueMsg struct {
	Op    string         // "add" | "add_track" | "jump" | "remove" | "move"
	Path  string         // add
	Track playlist.Track // add_track
	Index int            // jump, remove, move (from)
	To    int            // move (to)
}

// PluginStreamTitleMsg is sent by Lua plugins (cliamp.player.set_stream_title)
// to override the display title of a live stream. Mutations are routed
// through the Update loop so the model's derived state stays consistent.
type PluginStreamTitleMsg struct {
	Title string // new stream title; "" clears any plugin override
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

	case "add_track":
		// Queued as described, like IPC track.queue: the path is not resolved.
		track := msg.Track
		track.Stream = track.Stream || playlist.IsURL(track.Path)
		return m.appendPluginTracks(track)

	case "jump":
		if msg.Index < 0 || msg.Index >= m.playlist.Len() {
			return nil
		}
		return m.playIndex(msg.Index)

	case "remove":
		cmd, _ := m.removeTrack(msg.Index, false)
		return cmd

	case "move":
		cmd, _ := m.moveTrack(msg.Index, msg.To)
		return cmd
	}
	return nil
}

// handlePluginStreamTitle applies a stream title override requested by a plugin.
// It only succeeds on a live stream that has no ICY stream title metadata.
// Streams that carry ICY metadata reject the override; non-streams are ignored.
// The title is cleared whenever a new track starts (see setPlaybackTrack).
func (m *Model) handlePluginStreamTitle(msg PluginStreamTitleMsg) tea.Cmd {
	track, _ := m.currentPlaybackTrack()
	// Only an active live stream can take a plugin title override. A
	// non-stream, a stopped player, or a track still buffering must be
	// ignored so the override never leaks onto unrelated playback.
	if !track.Stream || !m.playingTrackActive || !m.player.IsPlaying() {
		return nil
	}
	// A plugin override wins over ICY metadata, but a station that already
	// sends inline ICY StreamTitle refuses the override.
	if m.streamTitle != "" {
		// Stream already carries ICY metadata; refuse to override it.
		applog.Warn("set_stream_title: stream already has ICY metadata, refusing override")
		return nil
	}
	m.pluginStreamTitle = msg.Title
	m.refreshStreamTitleUI()
	return nil
}

// refreshStreamTitleUI reapplies the layout and scroll after a stream title
// change. tickStreamTitle and handlePluginStreamTitle both set m.streamTitle,
// so they share these three calls instead of repeating them.
func (m *Model) refreshStreamTitleUI() {
	m.resetTitleScroll()
	m.applyHeightMode()
	m.adjustScroll()
}

// appendPluginTracks appends tracks a plugin added to the end of the playlist
// and re-arms the gapless preload, since an append can change the next track
// (repeat-all on the last track, or a shuffle of the upcoming order).
func (m *Model) appendPluginTracks(tracks ...playlist.Track) tea.Cmd {
	if len(tracks) == 0 {
		return nil
	}
	m.appendTracks(tracks...)
	return m.rearmStalePreload()
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
