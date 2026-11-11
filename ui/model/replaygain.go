package model

import (
	"fmt"

	"github.com/bjarneo/cliamp/internal/replaygain"
	"github.com/bjarneo/cliamp/playlist"
)

// replayGainReporter is the player's view of normalisation, for display.
type replayGainReporter interface {
	ReplayGainMode() string
	ReplayGainApplied() (db float64, used string)
}

// replayGainText describes the gain applied to the playing track, "none" when it
// has no values, and "" for any other track or when normalisation is off. The
// player reads a track's tags only when it starts, so nothing is known before.
func (m Model) replayGainText(t playlist.Track) string {
	r, ok := m.player.(replayGainReporter)
	if !ok {
		return ""
	}
	mode := r.ReplayGainMode()
	if mode == "" || mode == replaygain.ModeOff || t.Path == "" {
		return ""
	}
	if playing, _ := m.currentPlaybackTrack(); m.player.IsPlaying() && playing.Path == t.Path {
		db, used := r.ReplayGainApplied()
		if used == "" {
			return "none (no ReplayGain values)"
		}
		return fmt.Sprintf("%+.2f dB (%s gain)", db, used)
	}
	return ""
}
