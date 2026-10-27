package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// queueContinuedMsg carries the next batch of an endless list, such as a
// Plex radio station, that continues after the track at path after.
type queueContinuedMsg struct {
	after  string
	tracks []playlist.Track
	err    error
}

// continueQueueCmd asks the provider for the next batch when track ends a
// batch of an endless list. Asking when the last track starts leaves its
// whole length to load the batch and preload the next track, so playback
// does not pause.
func (m *Model) continueQueueCmd(track playlist.Track) tea.Cmd {
	if m.playlist == nil {
		return nil
	}
	continuer, _ := findCapable(m, func(c provider.QueueContinuer) bool {
		return c.CanContinueAfter(track)
	})
	if continuer == nil {
		return nil
	}
	return func() tea.Msg {
		tracks, err := continuer.ContinueAfter(track)
		return queueContinuedMsg{after: track.Path, tracks: tracks, err: err}
	}
}

// handleQueueContinued appends the next batch when the queue still ends with
// the track it continues; a queue replaced in the meantime keeps its content.
func (m *Model) handleQueueContinued(msg queueContinuedMsg) {
	if msg.err != nil {
		applog.Warn("continue queue: %v", msg.err)
		return
	}
	if len(msg.tracks) == 0 || m.playlist == nil {
		return
	}
	tracks := m.playlist.Tracks()
	if len(tracks) == 0 || tracks[len(tracks)-1].Path != msg.after {
		return
	}
	m.playlist.Add(msg.tracks...)
}
