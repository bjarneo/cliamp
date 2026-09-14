package model

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

// playbackTrack keeps source identity and its original queue entry together.
// Paths alone cannot distinguish repeated entries or a replaced playlist.
type playbackTrack struct {
	ticket   uint64
	track    playlist.Track
	context  []playlist.Track
	index    int
	detached bool
}

func (m Model) capturePlaybackTrack(track playlist.Track, ticket uint64) *playbackTrack {
	tracks, index := track.PlaybackContext()
	if index < 0 && m.playlist != nil {
		tracks, index = m.playlist.Tracks(), m.playlist.Index()
		if index < 0 || index >= len(tracks) || tracks[index].Path != track.Path {
			index = trackIndexByPath(tracks, track.Path)
		}
	}
	return &playbackTrack{ticket: ticket, track: track, context: cloneTracks(tracks), index: index}
}

// displayedPlaybackTrack is presentation state: a requested source is shown as
// loading while the active source continues to play. Never pair it with engine
// position, seeks, or reports; use activePlaybackTrack or playbackSnapshot.
func (m Model) displayedPlaybackTrack() (playlist.Track, int) {
	if m.pending != nil {
		return m.pending.track, 0
	}
	if track, index := m.activePlaybackTrack(); index >= 0 {
		return track, index
	}
	if m.playlist == nil {
		return playlist.Track{}, -1
	}
	return m.playlist.Current()
}

// ICY metadata belongs to audible playback, never to a loading replacement.
func (m Model) presentationStreamTitle() string {
	if m.pending != nil {
		return ""
	}
	return m.streamTitle
}

// activePlaybackTrack is the only track that may be paired with engine state
// for progress, resume, scrobbles, or external playback notifications.
func (m Model) activePlaybackTrack() (playlist.Track, int) {
	if m.playing != nil && m.player != nil && m.player.IsPlaying() {
		return m.playing.track, 0
	}
	return playlist.Track{}, -1
}

// selectPlaybackIndex settles any gapless promotion before changing selection.
func (m *Model) selectPlaybackIndex(index int) {
	m.clearPreload()
	m.playlist.SetIndex(index)
}

func (m Model) playbackTicket() uint64 {
	if m.playing == nil {
		return 0
	}
	return m.playing.ticket
}

// playbackSnapshot pairs metadata with one atomic engine sample. A gapless
// promotion can occur between messages; defer reporting until it is adopted.
func (m Model) playbackSnapshot() (playlist.Track, player.PlaybackStats, bool) {
	stats := m.player.Snapshot()
	if !stats.Playing {
		return playlist.Track{}, stats, true
	}
	if m.playing == nil || m.playing.ticket != stats.Ticket {
		return playlist.Track{}, stats, false
	}
	return m.playing.track, stats, true
}

func (m Model) currentPlaybackIsLive(track playlist.Track) bool {
	if track.IsLive() {
		return true
	}
	reporter, ok := m.player.(interface{ IsLiveStream() bool })
	return ok && reporter.IsLiveStream()
}

func (m *Model) detachPlaybackTrack() {
	if m.playing != nil {
		m.playing.detached = true
	}
	if m.pending != nil {
		m.pending.detached = true
	}
	if m.preloaded != nil {
		m.preloaded.detached = true
	}
	m.playbackDetached = true
}

func (m *Model) clearPlaybackTrack() {
	m.seek = seekState{gen: m.seek.gen + 1}
	m.pending, m.playing, m.preloaded = nil, nil, nil
	m.buffering = false
	m.playbackDetached = false
}

func (m *Model) finishPlayback(stats player.PlaybackStats) {
	if m.playing == nil {
		return
	}
	if stats.Ticket != 0 && m.playing.ticket != 0 && stats.Ticket != m.playing.ticket {
		return
	}
	m.maybeScrobble(m.playing.track, stats.Position, stats.Duration, stats.Seekable)
}

// stopPlayback stops audio and clears playback state. It returns the source
// that was playing, after adopting a gapless promotion that raced the stop, so
// callers can report what finished.
func (m *Model) stopPlayback() *playbackTrack {
	stats := m.player.Stop()
	m.consumeGaplessAdvance(&stats)
	finished := m.playing
	m.finishPlayback(stats)
	m.clearPlaybackTrack()
	m.preloading = false
	return finished
}

// endQueue stops playback because nothing follows the current track and tells
// plugins which track finished, so they can tell the end of the queue apart
// from a manual stop. Only a source that started playing is reported: a
// request still loading, a failed start, or an empty player only stops.
func (m *Model) endQueue() {
	if finished := m.stopPlayback(); finished != nil {
		m.emitPlugin(luaplugin.EventQueueEnd, trackToMap(finished.track))
	}
}

func (m *Model) applyPreparedSource(msg sourcePreparedMsg) tea.Cmd {
	if m.pending == nil || msg.ticket != m.pending.ticket {
		return nil
	}
	source := m.pending
	m.pending = nil
	m.buffering = false
	if msg.err != nil {
		active, _ := m.activePlaybackTrack()
		if m.player.Drained() && !m.currentPlaybackIsLive(active) {
			m.stopPlayback()
		}
		if errors.Is(msg.err, player.ErrRevoked) {
			return nil
		}
		if errors.Is(msg.err, playlist.ErrNeedsAuth) {
			m.provSignIn = true
			m.err = nil
		} else {
			m.err = msg.err
			if source.track.Stream || playlist.IsYTDL(source.track.Path) {
				m.status.Errorf(statusTTLLong, "Couldn't play %s — track is gated, restricted, or unavailable.", source.track.DisplayName())
			}
		}
		if m.playing != nil {
			m.playbackDetached = m.playing.detached
		}
		return m.preloadNext()
	}
	finished, committed := m.player.CommitStart(msg.ticket)
	if !committed {
		return nil
	}
	source.track = msg.track
	m.consumeGaplessAdvance(&finished)
	m.finishPlayback(finished)
	m.err = nil
	stats := m.player.Snapshot()
	m.activatePlaybackTrack(source, stats)
	var resume tea.Cmd
	if playlist.IsYTDL(source.track.Path) {
		resume = m.applyResume()
	} else {
		m.clearResume(source.track)
	}
	m.nowPlaying(source.track, stats.Position, stats.Seekable)
	m.backfillLoadedPlaylistDuration(source.track)
	return tea.Batch(resume, m.preloadNext())
}

// consumeGaplessAdvance adopts the exact source promoted by the engine. At a
// stop/replacement boundary, final supplies that source's last position rather
// than reading state from an engine that has already moved on.
func (m *Model) consumeGaplessAdvance(final *player.PlaybackStats) bool {
	if m.player == nil {
		return false
	}
	advance, ok := m.player.TakeAdvance()
	if !ok {
		return false
	}
	if m.preloaded == nil || m.preloaded.ticket != advance.Ticket {
		return true
	}
	source := m.preloaded
	m.preloaded = nil
	m.preloading = false
	m.finishPlayback(advance.Finished)
	if !source.detached && m.playlist != nil {
		if m.playbackDetached {
			m.playbackDetached = false
		} else {
			m.playlist.Next()
			m.normalizeQueueOverlay()
		}
		m.plCursor = m.playlist.Index()
		m.adjustScroll()
	}
	stats := player.PlaybackStats{}
	if final != nil {
		stats = *final
	} else {
		stats.Position, stats.Duration = m.player.PositionAndDuration()
		stats.Seekable = m.player.Seekable()
	}
	m.err = nil
	m.activatePlaybackTrack(source, stats)
	m.nowPlaying(source.track, stats.Position, stats.Seekable)
	return true
}

func (m *Model) clearPreload() {
	if m.preloaded == nil {
		return
	}
	// Quiesce the registered preload before deciding whether it was discarded
	// or had already become active. Its identity must survive that decision.
	m.player.ClearPreload()
	m.consumeGaplessAdvance(nil)
	m.preloaded = nil
	m.preloading = false
}

// prepareNext captures the exact queue entry before any asynchronous work.
func (m *Model) prepareNext(track playlist.Track) tea.Cmd {
	ticket, _ := m.player.BeginPreload()
	m.preloaded = m.capturePlaybackTrack(track, ticket)
	m.preloading = true
	return prepareSourceCmd(m.player, ticket, track, nil)
}
