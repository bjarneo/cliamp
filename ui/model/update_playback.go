package model

import (
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

func (m *Model) scheduleReconnect(now time.Time) {
	if !m.reconnect.at.IsZero() || m.reconnect.attempts >= 5 {
		return
	}
	delay := time.Second << m.reconnect.attempts
	m.reconnect.at = now.Add(delay)
	m.reconnect.attempts++
	if err := m.player.StreamErr(); err != nil {
		m.reconnect.notice = fmt.Errorf("reconnecting in %s (cause: %v)", delay, err)
	} else {
		m.reconnect.notice = fmt.Errorf("reconnecting in %s", delay)
	}
	m.err = m.reconnect.notice
}

// handleSeekTick finishes an async seek. A newer target that arrived while
// the seek ran is committed next.
func (m *Model) handleSeekTick(msg seekTickMsg) tea.Cmd {
	// Async seek completed. A completion from a previous track says nothing
	// about the current one, so it must not clear its state or report on it.
	if msg.gen != m.seek.gen {
		return nil
	}
	m.seek.inFlight = false
	if m.seek.pending {
		// Commit the newer target even when this seek failed: the failure
		// belongs to a position the user has already moved on from.
		if msg.resume {
			// The chained seek carries no resume marker, so spend it here
			// or a later restart seeks back to the resume position.
			m.resume.path = ""
			m.resume.secs = 0
		}
		// A newer target arrived while this seek was running; land on it
		// rather than reporting this now-stale position as final.
		return m.commitPendingSeek()
	}
	m.seek.pending = false
	// Only clear seekActive if no new seek keypresses arrived during loading.
	if m.seek.timer <= 0 {
		m.seek.active = false
	}
	// Grace period: suppress reconnect for a few ticks after seek completes.
	m.seek.grace = 10
	m.seek.graceFor = 0
	if msg.resume {
		// A failed resume must not be retried every time the track is opened
		// during this session. The original pipeline remains playable.
		m.resume.path = ""
		m.resume.secs = 0
	}
	if msg.err != nil {
		// A failed rewind plays on as the same play.
		m.seek.rewind = false
		if msg.resume {
			m.status.Warningf(statusTTLLong, "Couldn't resume this track; playing from the previous position: %s", msg.err)
		} else {
			m.status.Warningf(statusTTLMedium, "Seek failed; playback continues from the previous position: %s", msg.err)
		}
		return m.preloadNext()
	}
	if msg.resume {
		m.status.Showf(statusTTLDefault, "Resumed at %s", formatJumpClock(msg.target))
	}
	m.finishSeek()
	return m.preloadNext()
}

// handleYTDLUnpauseReconnect ends the seek state after a yt-dlp stream
// reconnects on unpause, and unpauses the track. A track that started since
// then owns the seek state, and a stop keeps the engine stopped.
func (m *Model) handleYTDLUnpauseReconnect(msg ytdlUnpauseReconnectMsg) {
	if msg.seekGen != m.seek.gen {
		return
	}
	m.seek.active = false
	m.seek.timer = 0
	m.seek.timerFor = 0
	m.seek.grace = 10
	m.seek.graceFor = 0
	if msg.gen != m.requests.stream {
		return
	}
	if msg.err != nil {
		m.err = msg.err
		return
	}
	m.err = nil
	if m.player.IsPaused() {
		m.togglePlayerPause()
	}
	m.pausedAt = time.Time{}
}

// handleStreamPlayed finishes the start of a stream. It retries a drained
// live stream, reports a failure or applies the resume position.
func (m *Model) handleStreamPlayed(msg streamPlayedMsg) tea.Cmd {
	track, _ := m.currentPlaybackTrack()
	if msg.gen != m.requests.stream || msg.path != track.Path {
		return nil
	}
	m.buffering = false
	ytdlLiveDrain := m.reconnect.ytdlLiveDrain
	m.reconnect.ytdlLiveDrain = false
	if msg.err != nil && ytdlLiveDrain {
		// The drained live stream did not restart. The cause may be a
		// network outage or the end of the broadcast, so retry with
		// backoff before giving up on it and advancing.
		m.player.Stop()
		if m.reconnect.attempts < ytdlLiveDrainRestarts {
			m.scheduleReconnect(time.Now())
			m.reconnect.ytdlLiveDrain = true
			return nil
		}
		m.reconnect.attempts = 0
		return m.nextTrack()
	}
	var resumeCmd, backfillCmd tea.Cmd
	if errors.Is(msg.err, playlist.ErrNeedsAuth) {
		// The provider session went stale, for example after Spotify
		// rejected the stream keys. Ask for sign-in, not a raw error.
		m.provPane.signIn = true
		m.err = nil
		m.status.Warningf(statusTTLLong, "Sign-in required to play %s.", track.DisplayName())
	} else if msg.err != nil {
		m.err = msg.err
		applog.Warn("play %q: %v", msg.path, msg.err)
		// A local file is never gated, so m.err alone reports its failure.
		if track, idx := m.currentPlaybackTrack(); idx >= 0 && !player.UsesLocalFFmpeg(msg.path) {
			m.status.Errorf(statusTTLLong, "Couldn't play %s — track is gated, restricted, or unavailable.", track.DisplayName())
		}
	} else {
		m.err = nil
		m.reconnect.attempts = 0
		m.reconnect.at = time.Time{}
		resumeCmd = m.applyResume()
		m.nowPlaying(track)
		// A local ffmpeg format starts here too, and its decoded
		// duration can fill the loaded playlist.
		backfillCmd = m.backfillLoadedPlaylistDuration(track)
	}
	preloadCmd := m.preloadNext()
	return tea.Batch(resumeCmd, preloadCmd, backfillCmd)
}

// handleTrackSaved reports the result of a track save or download.
func (m *Model) handleTrackSaved(msg trackSavedMsg) {
	if msg.download {
		m.save.finishDownload()
	}
	switch {
	case msg.err == nil:
		m.status.Showf(statusTTLMedium, "Saved to %s", msg.path)
	case msg.download:
		m.status.Errorf(statusTTLMedium, "Download failed: %s", msg.err)
	default:
		m.status.Errorf(statusTTLShort, "Save failed: %s", msg.err)
	}
}
