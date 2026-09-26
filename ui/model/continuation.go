package model

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type continuationState struct {
	source                      provider.TrackExtender
	playlistID                  string
	offset                      int
	generation                  uint64
	loading, exhausted, waiting bool
	retryAt                     time.Time
}

type tracksExtendedMsg struct {
	tracks     []playlist.Track
	generation uint64
	err        error
}

// clearContinuation invalidates pending results and clears the continuation source.
func (m *Model) clearContinuation() {
	m.continuation = continuationState{generation: m.continuation.generation + 1}
}

// startContinuation attaches continuation support to the loaded playlist.
func (m *Model) startContinuation(msg tracksLoadedMsg) {
	if source, ok := m.provider.(provider.TrackExtender); ok && source.CanExtendPlaylist(msg.playlistID) {
		m.continuation.source = source
		m.continuation.playlistID = msg.playlistID
		m.continuation.offset = len(msg.tracks)
	}
}

// extendTracks requests one batch and optionally resumes playback on completion.
func (m *Model) extendTracks(wait bool) tea.Cmd {
	s := &m.continuation
	if s.source == nil || s.exhausted || time.Now().Before(s.retryAt) {
		return nil
	}
	s.waiting = s.waiting || wait
	if s.loading {
		return nil
	}
	s.loading = true
	// A new batch may change the upcoming shuffle order or prevent repeat-all
	// from wrapping. Do not let a preload for the old tail advance meanwhile.
	m.player.ClearPreload()
	nextRequest(&m.requests.preload)
	m.preloading = false
	source, id, offset, gen := s.source, s.playlistID, s.offset, s.generation
	m.status.Activity("Loading more tracks...", statusTTLLong)
	return func() tea.Msg {
		tracks, err := source.ExtendTracks(id, offset)
		return tracksExtendedMsg{tracks: tracks, generation: gen, err: err}
	}
}

// extendAtCursor requests more tracks when navigation reaches the last row.
func (m *Model) extendAtCursor(key string) tea.Cmd {
	if m.activeScreen() != screenMain || m.focus != focusPlaylist || m.playlist.Len() == 0 || m.plCursor != m.playlist.Len()-1 {
		return nil
	}
	switch key {
	case "up", "k", "down", "j", "pgdown", "ctrl+d", "end", "G":
		return m.extendTracks(false)
	}
	return nil
}

// applyExtendedTracks appends a current result or handles exhaustion and retries.
func (m *Model) applyExtendedTracks(msg tracksExtendedMsg) tea.Cmd {
	s := &m.continuation
	if msg.generation != s.generation || !s.loading {
		return nil
	}
	s.loading = false
	waiting := s.waiting
	s.waiting = false
	if msg.err != nil {
		s.retryAt = time.Now().Add(5 * time.Second)
		m.status.Warning("Could not load more tracks; move to the end or press Next to retry", statusTTLLong)
		if waiting {
			return m.nextTrack()
		}
		return nil
	}
	s.offset += len(msg.tracks)
	s.exhausted = len(msg.tracks) == 0
	if len(msg.tracks) == 0 {
		m.status.Show("No more tracks available", statusTTLDefault)
		if waiting {
			return m.nextTrack()
		}
		return nil
	}
	m.playlist.Add(msg.tracks...)
	m.loadedPlaylist = ""
	m.normalizeQueueOverlay()
	m.addToHeaderState(msg.tracks)
	// Appending can remix shuffle order; discard any preload for the old order.
	m.player.ClearPreload()
	nextRequest(&m.requests.preload)
	m.preloading = false
	m.adjustScroll()
	m.status.Showf(statusTTLShort, "Loaded %d more tracks", len(msg.tracks))
	m.notifyAll()
	if waiting {
		return m.nextTrack()
	}
	return nil
}

// atContinuationBoundary reports whether playback should request more tracks.
// A finite repeat-all list wraps, but an extensible sequential list should
// first ask for new tracks. Explicit queued tracks and repeat-one keep priority.
func (m Model) atContinuationBoundary() bool {
	if !m.playlist.HasNext() {
		return true
	}
	return !m.playlist.Shuffled() && m.playlist.Repeat() == playlist.RepeatAll &&
		m.playlist.QueueLen() == 0 && !m.playlist.CurrentIsQueued() && m.playlist.Index() == m.playlist.Len()-1
}
