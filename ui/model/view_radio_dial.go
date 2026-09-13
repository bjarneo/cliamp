package model

import "github.com/bjarneo/cliamp/ui"

// renderRadioDial returns the radio frequency dial for the currently playing
// track, or "" when the track has no frequency metadata.
func (m Model) renderRadioDial() string {
	if !m.playingTrackActive {
		return ""
	}
	freq := m.playingTrack.Meta("radio.frequency")
	if freq == "" {
		return ""
	}
	return ui.RenderRadioDial(freq, ui.PanelWidth)
}
