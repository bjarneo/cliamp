package model

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
)

// handleYTDLBatch appends a batch of a YouTube radio playlist to the queue
// and asks for the next batch.
func (m *Model) handleYTDLBatch(msg ytdlBatchMsg) tea.Cmd {
	// Discard stale responses from a previous batch session.
	if msg.gen != m.ytdlBatch.gen {
		return nil
	}
	m.ytdlBatch.loading = false
	if msg.err != nil {
		m.ytdlBatch.done = true
		m.status.Errorf(statusTTLBatch, "Radio batch load failed: %v", msg.err)
		return nil
	}
	if len(msg.tracks) == 0 {
		m.ytdlBatch.done = true
		return nil
	}
	m.appendTracks(msg.tracks...)
	m.ytdlBatch.offset += len(msg.tracks)
	if len(msg.tracks) < ytdlBatchSize {
		m.ytdlBatch.done = true
		return nil
	}
	// Immediately fetch the next batch.
	m.ytdlBatch.loading = true
	return fetchYTDLBatchCmd(m.ytdlBatch.gen, m.ytdlBatch.url, m.ytdlBatch.offset, ytdlBatchSize)
}

// handleFeedTrackResolved replaces the queue with the episodes of a feed
// and plays the first one. It drops the episodes when a track started or
// stopped, or another queue replace started, after the feed resolve began.
func (m *Model) handleFeedTrackResolved(msg feedTrackResolvedMsg) tea.Cmd {
	m.feedLoading = false
	if msg.gen != m.requests.stream || msg.queue != m.requests.queue {
		return nil
	}
	if msg.err != nil {
		m.err = withRetryHint(msg.err)
		return nil
	}
	if len(msg.tracks) == 0 {
		m.status.Warning("No episodes found in feed.", statusTTLDefault)
		return nil
	}
	m.retireTracksPaging()
	m.replacePlaylist(msg.tracks)
	m.clearLoadedPlaylist()
	m.setHeaderStateFromTracks(msg.tracks)
	m.plCursor = 0
	m.plScroll = 0
	m.applyHeightMode()
	m.adjustScroll()
	m.status.Showf(statusTTLDefault, "Loaded %d episode(s)", len(msg.tracks))
	return m.playCurrentTrack()
}

// handleFeedsLoaded appends the tracks that URLs resolved to. It can start
// playback and the batch load of a YouTube radio playlist.
func (m *Model) handleFeedsLoaded(msg feedsLoadedMsg) tea.Cmd {
	m.feedLoading = false
	if msg.err != nil {
		m.err = withRetryHint(msg.err)
		applog.Warn("load URLs: %v", msg.err)
		return nil
	}
	if len(msg.tracks) > 0 {
		m.appendTracks(msg.tracks...)
		m.status.Showf(statusTTLDefault, "Loaded %d track(s)", len(msg.tracks))
	} else {
		m.status.Warning("No tracks found at URL.", statusTTLDefault)
	}
	if len(msg.tracks) > 0 {
		// Set up incremental loading for YouTube Radio playlists.
		// The source URLs are carried in the message so we don't
		// need to re-scan pendingURLs (which misses interactive loads).
		batchCmd := m.initYTDLBatch(msg.urls)
		if msg.autoPlay && m.playlist.Len() > 0 && !m.player.IsPlaying() {
			playCmd := m.playCurrentTrack()
			if batchCmd != nil {
				return tea.Batch(playCmd, batchCmd)
			}
			return playCmd
		}
		if batchCmd != nil {
			return batchCmd
		}
	}
	return nil
}

// handleFBTracksResolved adds the tracks that the file browser picked to a
// playlist file, to the playlist picker or to the queue. It drops a replace
// when another queue replace started after the walk began.
func (m *Model) handleFBTracksResolved(msg fbTracksResolvedMsg) tea.Cmd {
	if msg.replace && msg.queue != m.requests.queue {
		return nil
	}
	if msg.err != nil {
		m.err = withRetryHint(msg.err)
		return nil
	}
	if len(msg.tracks) == 0 {
		m.status.Warning("No audio files found", statusTTLDefault)
		return nil
	}
	if msg.targetPlaylist != "" {
		added, skipped, err := m.writeTracksToPlaylist(msg.targetPlaylist, msg.tracks)
		if err != nil {
			m.status.Errorf(statusTTLDefault, "Add failed: %s", err)
		} else if skipped > 0 {
			m.status.Warningf(statusTTLBatch, "Added %d to %q, skipped %d duplicates", added, msg.targetPlaylist, skipped)
		} else if added > 0 {
			m.status.Showf(statusTTLDefault, "Added %d to %q", added, msg.targetPlaylist)
		} else {
			m.status.Warningf(statusTTLDefault, "Nothing added to %q", msg.targetPlaylist)
		}
		m.refreshPlaylistManagerAfterWrite(msg.targetPlaylist)
		// Track/dir counts in the provider pane come from Playlists();
		// re-pull now that the file write has landed.
		return m.refreshPaneAfterLocalWrite()
	}
	if msg.toPlaylist {
		m.openPlaylistPicker(msg.tracks, fmt.Sprintf("%d tracks selected", len(msg.tracks)))
		return nil
	}
	if msg.replace {
		m.stopPlayback()
		m.player.ClearPreload()
		m.retireTracksPaging()
		m.replacePlaylist(msg.tracks)
		m.clearLoadedPlaylist()
		m.setHeaderStateFromTracks(msg.tracks)
		m.plCursor = 0
		m.plScroll = 0
	} else {
		m.appendTracks(msg.tracks...)
	}
	m.focus = focusPlaylist
	m.applyHeightMode()
	m.adjustScroll()
	if msg.replace {
		m.status.Successf(statusTTLDefault, "Replaced queue with %d track(s)", len(msg.tracks))
	} else {
		m.status.Successf(statusTTLDefault, "Added %d track(s)", len(msg.tracks))
	}
	if !m.player.IsPlaying() && m.playlist.Len() > 0 {
		if msg.replace {
			m.playlist.SetIndex(0)
		}
		return m.playCurrentTrack()
	}
	return nil
}
