package model

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/playlist"
)

type djSession interface {
	Next(context.Context) ([]playlist.Track, error)
	Close()
}

type spotifyDJState struct {
	ctx     context.Context
	cancel  context.CancelFunc
	session djSession
	gen     uint64
	loading bool
	paused  bool
}

type djBatchMsg struct {
	gen     uint64
	session djSession
	tracks  []playlist.Track
	err     error
}

func (m *Model) cancelDJ() {
	if m.spotifyDJ.cancel != nil {
		if track, _ := m.currentPlaybackTrack(); track.Meta("spotify.dj") == "true" {
			m.stopPlayback()
		}
		nextRequest(&m.requests.preload)
		m.player.ClearPreload()
		m.preloading = false
	}
	if m.spotifyDJ.loading {
		m.provPane.loading = false
	}
	if m.spotifyDJ.cancel != nil {
		m.spotifyDJ.cancel()
	}
	if m.spotifyDJ.session != nil {
		m.spotifyDJ.session.Close()
	}
	m.spotifyDJ = spotifyDJState{gen: m.spotifyDJ.gen + 1}
}

func (m *Model) startDJ() tea.Cmd {
	source, ok := m.provider.(*spotify.SpotifyProvider)
	if !ok {
		return nil
	}
	m.cancelDJ()
	nextRequest(&m.requests.tracks)
	m.tracksPaging = false
	m.stopPlayback()
	nextRequest(&m.requests.preload)
	m.player.ClearPreload()
	m.preloading = false
	m.provPane.loading = true
	m.err = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.spotifyDJ.ctx, m.spotifyDJ.cancel, m.spotifyDJ.loading = ctx, cancel, true
	gen := m.spotifyDJ.gen
	m.status.Activity("Starting Spotify DJ…", statusTTLLong)
	return func() tea.Msg {
		setup, done := context.WithTimeout(ctx, 30*time.Second)
		defer done()
		// The session survives the setup deadline; each request has its own bound.
		session, err := source.StartDJ(ctx)
		var tracks []playlist.Track
		if err == nil {
			tracks, err = session.Next(setup)
		}
		return djBatchMsg{gen: gen, session: session, tracks: tracks, err: err}
	}
}

func (m *Model) nextDJBatch() tea.Cmd {
	if m.spotifyDJ.loading {
		return nil
	}
	m.stopPlayback()
	m.player.ClearPreload()
	m.preloading = false
	m.spotifyDJ.loading = true
	m.provPane.loading = true
	ctx, session, gen := m.spotifyDJ.ctx, m.spotifyDJ.session, m.spotifyDJ.gen
	m.status.Activity("Loading next DJ batch…", statusTTLLong)
	return func() tea.Msg {
		tracks, err := session.Next(ctx)
		return djBatchMsg{gen: gen, session: session, tracks: tracks, err: err}
	}
}

func (m *Model) handleDJBatch(msg djBatchMsg) tea.Cmd {
	if msg.gen != m.spotifyDJ.gen || m.spotifyDJ.cancel == nil {
		if msg.session != nil {
			msg.session.Close()
		}
		return nil
	}
	m.spotifyDJ.loading = false
	m.provPane.loading = false
	m.spotifyDJ.session = msg.session
	if msg.err != nil || len(msg.tracks) == 0 {
		m.cancelDJ()
		m.stopPlayback()
		if msg.err == nil {
			msg.err = fmt.Errorf("Spotify DJ returned no tracks")
		}
		m.err = msg.err
		return nil
	}
	// Each batch replaces the exhausted one, bounding the queue and narration cache.
	nextRequest(&m.requests.queue)
	m.resetYTDLBatch()
	m.playlistUndo = playlistUndo{}
	m.playlist.Replace(msg.tracks)
	if m.playlist.Shuffled() {
		m.playlist.ToggleShuffle()
	}
	m.playlist.SetRepeat(playlist.RepeatOff)
	m.normalizeQueueOverlay()
	m.clearLoadedPlaylist()
	m.setHeaderStateFromTracks(msg.tracks)
	m.plCursor, m.plScroll, m.focus = 0, 0, focusPlaylist
	m.adjustScroll()
	if m.spotifyDJ.paused {
		return nil
	}
	return m.playCurrentTrack()
}

// Cancelling setup keeps an unfinished narration from starting after Pause.
func (m *Model) pauseDJSetup() bool {
	if m.spotifyDJ.cancel == nil || !m.buffering {
		return false
	}
	m.stopPlayback()
	m.spotifyDJ.paused = true
	return true
}
