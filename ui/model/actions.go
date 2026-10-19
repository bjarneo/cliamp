package model

import (
	"math"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
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
	m.cancelSonosHandoff()
	m.playlist.SetIndex(idx)
	return m.playCurrentTrack()
}

// setShuffle turns shuffle on or off, saves the mode and re-arms the preload
// for the new next track.
func (m *Model) setShuffle(on bool) tea.Cmd {
	if m.rejectSonosLocalControl("Shuffle") {
		return nil
	}
	if m.playlist.Shuffled() != on {
		m.cancelSonosHandoff()
		m.playlist.ToggleShuffle()
	}
	m.adjustScroll()
	_ = m.saveConfigBool("shuffle", m.playlist.Shuffled())
	return m.rearmStalePreload()
}

// setRepeat sets the repeat mode, saves it and re-arms the preload for the
// new next track.
func (m *Model) setRepeat(mode playlist.RepeatMode) tea.Cmd {
	if m.rejectSonosLocalControl("Repeat") {
		return nil
	}
	m.cancelSonosHandoff()
	m.playlist.SetRepeat(mode)
	_ = m.saveConfigString("repeat", m.playlist.Repeat().String())
	return m.rearmStalePreload()
}

// cycleVisualizer switches to the next visualizer mode, fits the layout to
// it and saves the choice. It returns the error of the config save.
func (m *Model) cycleVisualizer() error {
	if m.rejectSonosLocalControl("Visualizer") {
		return nil
	}
	m.vis.CycleMode()
	m.vis.RequestRefresh()
	m.applyHeightMode()
	m.adjustScroll()
	return m.saveVisualizerChoice()
}

// setVolume sets the volume in dB. The player clamps db to its range.
func (m *Model) setVolume(db float64) tea.Cmd {
	return m.setVolumeJob(db, nil, "")
}

func (m *Model) setVolumeJob(db float64, jobs *ipc.JobStore, jobID string) tea.Cmd {
	if m.sonosOutput.selected != "" {
		if m.sonosOutput.state == nil || m.sonosOutput.state.Volume == nil {
			m.status.Warning("Sonos group volume is unavailable", statusTTLDefault)
			return nil
		}
		volume := sonosVolumePercent(db)
		return m.setSonosVolumeJob(volume-m.intendedSonosVolume(), jobs, jobID, ipc.Response{OK: true})
	}
	m.player.SetVolume(db)
	if jobs != nil {
		m.completeV2Job(jobs, jobID, ipc.Response{OK: true, Volume: m.player.Volume()})
	}
	return nil
}

// adjustVolume changes the volume by delta dB. See setVolume.
func (m *Model) adjustVolume(delta float64) tea.Cmd {
	return m.adjustVolumeJob(delta, nil, "")
}

func (m *Model) adjustVolumeJob(delta float64, jobs *ipc.JobStore, jobID string) tea.Cmd {
	if m.sonosOutput.selected != "" {
		if m.sonosOutput.state == nil || m.sonosOutput.state.Volume == nil {
			m.status.Warning("Sonos group volume is unavailable", statusTTLDefault)
			return nil
		}
		base := m.intendedSonosVolume()
		target := sonosVolumePercent(sonosVolumeDB(base) + delta)
		return m.setSonosVolumeJob(target-base, jobs, jobID, ipc.Response{OK: true})
	}
	if jobs != nil {
		m.player.SetVolume(m.player.Volume() + delta)
		m.completeV2Job(jobs, jobID, ipc.Response{OK: true, Volume: m.player.Volume()})
		return nil
	}
	return m.setVolume(m.player.Volume() + delta)
}

func currentSonosVolume(m *Model) int {
	if m.sonosOutput.state == nil || m.sonosOutput.state.Volume == nil {
		return 0
	}
	return *m.sonosOutput.state.Volume
}

func sonosVolumePercent(db float64) int {
	// Map cliamp's -90..+6 dB scale onto Sonos's integer 0..100 group volume.
	return max(0, min(100, int(math.Round((db+90)*100/96))))
}

func sonosVolumeDB(volume int) float64 {
	return float64(volume)*96/100 - 90
}

func (m *Model) intendedSonosVolume() int {
	value := currentSonosVolume(m)
	if active := m.sonosOutput.active; active != nil && active.action == "volume" {
		value = active.volume
	}
	for _, pending := range m.sonosOutput.pending {
		if pending.action == "volume" {
			value = pending.volume
		}
	}
	return value
}
