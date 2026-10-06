package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// This file holds one action verb for each user intent that more than one
// entry point starts. The keys, the full-screen visualizer keys, the
// playback messages from media controls and Lua, the Lua queue calls and V2
// IPC call the same verb. So each intent gets the same config save and
// preload rearm, whatever starts it. The scrobble of a track that is left
// comes from playTrack and stopPlayback, and Update tells the media controls
// and plugins about each change.

// skipNext starts the next track. playTrack or stopPlayback scrobbles the
// track that it leaves.
func (m *Model) skipNext() tea.Cmd {
	return m.nextTrack()
}

// skipPrev goes to the previous track. Past 3 seconds into a track, it
// restarts that track instead. A restart also scrobbles the track.
func (m *Model) skipPrev() tea.Cmd {
	return m.prevTrack()
}

// playIndex starts the track at idx. An unplayable track is skipped forward.
func (m *Model) playIndex(idx int) tea.Cmd {
	m.playlist.SetIndex(idx)
	return m.playCurrentTrack()
}

// setShuffle turns shuffle on or off, saves the mode and re-arms the preload
// for the new next track.
func (m *Model) setShuffle(on bool) tea.Cmd {
	if m.playlist.Shuffled() != on {
		m.playlist.ToggleShuffle()
	}
	m.adjustScroll()
	_ = m.saveConfigBool("shuffle", m.playlist.Shuffled())
	return m.rearmStalePreload()
}

// setRepeat sets the repeat mode, saves it and re-arms the preload for the
// new next track.
func (m *Model) setRepeat(mode playlist.RepeatMode) tea.Cmd {
	m.playlist.SetRepeat(mode)
	_ = m.saveConfigString("repeat", m.playlist.Repeat().String())
	return m.rearmStalePreload()
}

// cycleVisualizer switches to the next visualizer mode, fits the layout to
// it and saves the choice. It returns the error of the config save.
func (m *Model) cycleVisualizer() error {
	m.vis.CycleMode()
	m.vis.RequestRefresh()
	m.applyHeightMode()
	m.adjustScroll()
	return m.saveVisualizerChoice()
}

// setVolume sets the volume in dB. The player clamps db to its range.
func (m *Model) setVolume(db float64) {
	m.player.SetVolume(db)
}

// adjustVolume changes the volume by delta dB. See setVolume.
func (m *Model) adjustVolume(delta float64) {
	m.setVolume(m.player.Volume() + delta)
}
