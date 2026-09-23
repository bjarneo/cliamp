package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

func (m *Model) scheduleReconnect(now time.Time) {
	if !m.reconnect.at.IsZero() || m.reconnect.attempts >= 5 {
		return
	}
	delay := time.Second << m.reconnect.attempts
	m.reconnect.at = now.Add(delay)
	m.reconnect.attempts++
	m.err = fmt.Errorf("reconnecting in %s", delay)
}

// Update handles messages: key presses, ticks, and window resizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasScreen := m.activeScreen()
	wasVisualizerVisible := m.visualizerVisible()
	wasMode := ui.VisNone
	if m.vis != nil {
		wasMode = m.vis.Mode
	}
	wasPlaying := false
	wasPaused := false
	if m.player != nil {
		wasPlaying = m.player.IsPlaying()
		wasPaused = m.player.IsPaused()
	}
	defer func() {
		m.maybeRequestVisualizerRefresh(msg, wasScreen, wasVisualizerVisible, wasMode, wasPlaying, wasPaused)
		m.emitPluginEvents()
		m.publishIPCRuntimeState()
	}()

	switch msg := msg.(type) {
	case tea.PasteMsg:
		cmd := m.handlePaste(msg.Content)
		return m, cmd

	case tea.KeyPressMsg:
		cmd := m.handleKey(msg)
		if m.quitting {
			return m, tea.Quit
		}
		m.applyHeightMode()
		m.adjustScroll()
		return m, cmd

	case autoPlayMsg:
		if m.playlist.Len() > 0 && !m.player.IsPlaying() {
			cmd := m.playCurrentTrack()
			m.notifyAll()
			return m, cmd
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recomputeLayout()
		m.normalizeMainFocus()
		m.clampActiveScrollState()
		return m, nil

	case seekTickMsg:
		// Async seek completed. A completion from a previous track says nothing
		// about the current one, so it must not clear its state or report on it.
		if msg.gen != m.seek.gen {
			return m, nil
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
			cmd := m.commitPendingSeek()
			return m, cmd
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
			if msg.resume {
				m.status.Warningf(statusTTLLong, "Couldn't resume this show; playing from the previous position: %s", msg.err)
			} else {
				m.status.Warningf(statusTTLMedium, "Seek failed; playback continues from the previous position: %s", msg.err)
			}
			m.notifyAll()
			cmd := m.preloadNext()
			return m, cmd
		}
		if msg.resume {
			m.status.Showf(statusTTLDefault, "Resumed at %s", formatJumpClock(msg.target))
		}
		m.finishSeek()
		cmd := m.preloadNext()
		return m, cmd

	case ytdlUnpauseReconnectMsg:
		m.seek.active = false
		m.seek.timer = 0
		m.seek.timerFor = 0
		m.seek.grace = 10
		m.seek.graceFor = 0
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			m.pausedAt = time.Time{}
		}
		m.notifyAll()
		return m, nil

	case tickMsg:
		now := time.Time(msg)
		dt := m.tickDelta(now)

		// Cache expensive player state once per tick so View() render
		// functions don't re-acquire speaker.Lock() multiple times.
		// PositionAndDuration() batches both reads under one speaker lock.
		if !m.buffering {
			if m.seek.active {
				m.cachedPos = m.seek.targetPos
				m.cachedDur = m.player.Duration()
			} else {
				m.cachedPos, m.cachedDur = m.player.PositionAndDuration()
				// Piped SSH streams report 0 duration — use metadata fallback.
				if m.cachedDur == 0 {
					if track, _ := m.currentPlaybackTrack(); track.DurationSecs > 0 && strings.HasPrefix(track.Path, "ssh://") {
						m.cachedDur = time.Duration(track.DurationSecs) * time.Second
					}
				}
			}
		} else {
			track, _ := m.currentPlaybackTrack()
			m.cachedDur = time.Duration(track.DurationSecs) * time.Second
			m.cachedPos = 0
		}
		m.tickVisualizer(now)
		m.tickProgressReport(now)
		// Process debounced yt-dlp seek.
		var seekCmd tea.Cmd
		if cmd := m.tickSeek(dt); cmd != nil {
			seekCmd = cmd
		}
		// Expire temporary status messages.
		wasStatus := m.status.text != ""
		if !m.status.expiresAt.IsZero() && !now.Before(m.status.expiresAt) {
			m.status.Clear()
		}
		// Drain app log buffer and expire old entries.
		wasLogs := len(m.logLines)
		m.tickLogLines(now)
		if (wasStatus && m.status.text == "") || len(m.logLines) != wasLogs {
			m.applyHeightMode()
			m.adjustScroll()
		}
		m.tickPendingSpeedSave(dt)
		m.tickPendingEQSave(dt)
		if m.pendingSeekActive && !m.pendingSeekExpiresAt.IsZero() && !now.Before(m.pendingSeekExpiresAt) {
			m.pendingSeekActive = false
			m.pendingSeekExpiresAt = time.Time{}
		}
		// Decrement seek grace period.
		advanceTickUnits(&m.seek.grace, &m.seek.graceFor, dt, ui.TickFast)
		// Surface stream errors (e.g., connection drops) and auto-reconnect streams.
		// Suppress during yt-dlp seek and grace period — killing the old pipeline
		// triggers a transient error that can persist for a few ticks.
		if err := m.player.StreamErr(); err != nil && !m.seek.active && m.seek.grace == 0 {
			track, idx := m.currentPlaybackTrack()
			isStream := idx >= 0 && (track.Stream || playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path))
			if isStream && m.reconnect.attempts < 5 {
				m.scheduleReconnect(now)
			} else {
				m.err = err
				m.reconnect.at = time.Time{}
			}
		}
		var lyricCmd tea.Cmd
		// Poll ICY stream title for live radio display.
		if title := m.player.StreamTitle(); title != "" && title != m.streamTitle {
			m.streamTitle = title
			m.resetTitleScroll()
			m.applyHeightMode()
			m.adjustScroll()
			m.notifyAll()
			// Auto-fetch lyrics when the stream song changes and lyrics overlay is open.
			if m.lyrics.visible && !m.lyrics.loading {
				if artist, song, ok := strings.Cut(title, " - "); ok {
					q := artist + "\n" + song
					if q != m.lyrics.query {
						m.lyrics.query = q
						m.lyrics.loading = true
						m.lyrics.lines = nil
						m.lyrics.err = nil
						m.lyrics.scroll = 0
						lyricCmd = fetchLyricsCmd(artist, song, q, nextRequest(&m.requests.lyrics))
					}
				}
			}
		}
		m.network.sampleFor += dt
		if m.network.sampleFor >= time.Second {
			downloaded, _ := m.player.StreamBytes()
			if downloaded > 0 || m.player.IsPlaying() {
				m.notifyAll()
			}
			delta := downloaded - m.network.lastBytes
			if delta > 0 {
				// Exponential moving average for smooth display.
				instant := float64(delta) / m.network.sampleFor.Seconds() // bytes/sec
				if m.network.speed == 0 {
					m.network.speed = instant
				} else {
					m.network.speed = m.network.speed*0.6 + instant*0.4
				}
			} else if downloaded == 0 {
				m.network.speed = 0
			}
			m.network.lastBytes = downloaded
			m.network.sampleFor = 0
		}
		// Fire scheduled reconnect when the timer expires.
		if !m.reconnect.at.IsZero() && now.After(m.reconnect.at) {
			m.reconnect.at = time.Time{}
			track, idx := m.currentPlaybackTrack()
			m.player.Stop()
			if idx >= 0 {
				// Preserve any seek/lyric commands already queued this tick
				// rather than dropping them on the early return.
				batch := []tea.Cmd{m.playTrack(track), tickCmdAt(ui.TickFast)}
				if seekCmd != nil {
					batch = append(batch, seekCmd)
				}
				if lyricCmd != nil {
					batch = append(batch, lyricCmd)
				}
				return m, tea.Batch(batch...)
			}
		}
		var cmds []tea.Cmd
		if seekCmd != nil {
			cmds = append(cmds, seekCmd)
		}
		if lyricCmd != nil {
			cmds = append(cmds, lyricCmd)
		}
		// Check gapless transition (audio already playing next track)
		gaplessAdvanced := m.player.GaplessAdvanced()
		if gaplessAdvanced {
			// Capture the track that just finished before advancing the playlist.
			// For gapless, the track played fully (100% ≥ 50%), so elapsed = duration.
			// The player stashed the finished pipeline's real duration at swap
			// time; metadata is only a fallback for tracks without it.
			finishedTrack, _ := m.currentPlaybackTrack()
			fullDur := m.player.LastPlayedDuration()
			if fullDur <= 0 {
				fullDur = time.Duration(finishedTrack.DurationSecs) * time.Second
			}
			if refresh := m.maybeScrobble(finishedTrack, fullDur, fullDur); refresh != nil {
				cmds = append(cmds, refresh)
			}

			var newTrack playlist.Track
			var ok bool
			if m.playbackDetached {
				var idx int
				newTrack, idx = m.playlist.Current()
				ok = idx >= 0
				m.playbackDetached = false
			} else {
				newTrack, ok = m.playlist.Next()
				m.normalizeQueueOverlay()
			}
			if !ok {
				m.endQueue()
				m.notifyAll()
				cmds = append(cmds, tickCmdAt(m.tickInterval()))
				return m, tea.Batch(cmds...)
			}
			m.plCursor = m.playlist.Index()
			m.adjustScroll()
			var gaplessLyricCmd tea.Cmd
			newTrack, gaplessLyricCmd = m.beginPlaybackTrack(newTrack)
			if gaplessLyricCmd != nil {
				cmds = append(cmds, gaplessLyricCmd)
			}
			// The preload that just fired is consumed — clear the in-flight flag
			// so the next track can be preloaded.
			m.preloading = false
			// A stream decoder error at the track boundary (e.g., server closing
			// the connection when the preload HTTP request opens) is expected and
			// not a user-visible problem. Clear any pending error so the red
			// message doesn't flash at every track transition.
			m.err = nil
			// Gapless advances without calling playTrack(), so emit now-playing here.
			m.nowPlaying(newTrack)
			cmds = append(cmds, m.preloadNext())
			if cmd := m.smartMaybeFetch(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			m.notifyAll()
		}
		m.tickResumeSave(now)
		// Check if gapless drained (end of playlist, no preloaded next).
		// Skip if already buffering a yt-dlp download to avoid advancing
		// the playlist on every tick while waiting for the resolve.
		if !gaplessAdvanced && m.player.IsPlaying() && !m.player.IsPaused() && m.player.Drained() && !m.buffering && m.reconnect.at.IsZero() {
			finishedTrack, idx := m.currentPlaybackTrack()
			if idx >= 0 && m.currentPlaybackIsLive(finishedTrack) {
				// A live stream has no natural end. A clean decoder EOF is a
				// disconnect, so retry this station instead of advancing.
				m.scheduleReconnect(now)
			} else {
				// Track drained to end — always ≥ 50%. The player is still on
				// the finished track here, so its live duration is authoritative
				// even when playlist metadata (DurationSecs) is unknown.
				drainDur := m.player.Duration()
				if drainDur <= 0 {
					drainDur = time.Duration(finishedTrack.DurationSecs) * time.Second
				}
				if refresh := m.maybeScrobble(finishedTrack, drainDur, drainDur); refresh != nil {
					cmds = append(cmds, refresh)
				}

				// Stop the player before dispatching the async nextTrack command.
				// This clears the gapless streamer so the finished track cannot
				// replay while waiting for a yt-dlp pipe chain to spin up.
				m.player.Stop()
				cmds = append(cmds, m.nextTrack())
			}
			m.notifyAll()
		}
		m.advanceTitleScroll(now)
		// Retry deferred stream preload: preloadNext() returns nil (defers) when
		// the current stream has >streamPreloadLeadTime remaining. Poll every tick
		// until we're within the window and the preload gets armed.
		// Guard with !m.preloading so we don't fire a second concurrent HTTP
		// connection while the first preloadStreamCmd goroutine is still running,
		// and with !m.tracksPaging because each page of a paged load remixes the
		// upcoming order, so anything armed now would be stale by the next one.
		if m.player.IsPlaying() && !m.player.IsPaused() && !m.buffering && !m.preloading && !m.tracksPaging && !m.player.HasPreload() {
			if cmd := m.preloadNext(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// Smart Shuffle: while playing, top up the queue when the upcoming
		// smart cushion runs low (guarded dispatch; see smart.go).
		if m.player.IsPlaying() && !m.player.IsPaused() {
			if cmd := m.smartMaybeFetch(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		m.advanceTerminalTitle()
		cmds = append(cmds, tickCmdAt(m.tickInterval()))
		return m, tea.Batch(cmds...)

	case openDefaultProviderBrowserMsg:
		if !m.openDefaultProviderOnce {
			return m, nil
		}
		m.openDefaultProviderOnce = false
		cmd := m.openDefaultProviderBrowser()
		return m, cmd

	case radioListsRefreshMsg:
		if msg.gen != m.requests.provider || !m.isActiveProvider("Radio") {
			return m, nil
		}
		cmd := m.refreshRadioLists()
		return m, cmd

	case playlistsLoadedMsg:
		if msg.gen != m.requests.provider || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provLoading = m.provSearch.loading
		if msg.err != nil {
			m.provListFixup = provListFixupState{}
			if errors.Is(msg.err, playlist.ErrNeedsAuth) {
				m.provSignIn = true
				m.err = nil
				return m, nil
			}
			if len(msg.playlists) == 0 {
				m.err = msg.err
				return m, nil
			}
			m.err = nil
			m.status.Warningf(statusTTLLong, "%s", msg.err)
		}
		m.replaceProviderLists(msg.playlists)
		cmd := m.startCatalogLoading()
		return m, cmd

	case tracksLoadedMsg:
		if msg.gen != m.requests.tracks || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provLoading = false
		m.tracksPaging = msg.err == nil && msg.next > 0
		if msg.err != nil {
			if errors.Is(msg.err, playlist.ErrNeedsAuth) {
				m.provSignIn = true
				m.err = nil
				return m, nil
			}
			if errors.Is(msg.err, playlist.ErrListChanged) {
				// The list moved under a paged read, so what is on screen is a
				// partial view of a list that no longer exists. Say so and let it
				// expire: reopening starts a clean load, and a persistent error
				// would sit in front of every later status message.
				m.status.Warningf(statusTTLDefault, "Playlist changed while loading — reopen current playlist to reload")
				return m, nil
			}
			m.err = msg.err
			return m, nil
		}
		if msg.offset > 0 {
			m.playlist.Add(msg.tracks...)
			m.normalizeQueueOverlay()
			m.addToHeaderState(msg.tracks)
			// Add mixes the page into the upcoming shuffle order, so an armed
			// preload may no longer be the next track. The gapless swap runs on
			// the audio thread and the model then names the new track from
			// playlist.Next(), so a stale preload would play one track while the
			// UI, scrobble and now-playing announced another. Drop it and let the
			// tick loop re-arm against the order this page produced.
			if m.player.HasPreload() || m.preloading {
				m.player.ClearPreload()
				m.preloading = false
			}
		} else {
			m.replacePlayerPlaylist(msg.tracks)
			if msg.playlistExact && m.localProvider != nil && msg.providerName == m.localProvider.Name() && msg.playlistID != history.PlaylistName {
				m.loadedPlaylist = msg.playlistID
			}
		}
		if msg.next > 0 {
			m.adjustScroll()
			m.notifyAll()
			if pager, ok := m.provider.(provider.TrackPager); ok {
				return m, fetchTracksPageCmd(pager, msg.providerName, msg.playlistID, msg.next, msg.gen)
			}
		}
		if msg.offset > 0 {
			msg.tracks = m.playlist.Tracks()
		}
		m.applyTracksResume(msg)
		// Remote provider load: the queue mirrors the playlist in load order,
		// enabling remote writes (x remove) up to its length. On continuation
		// pages msg.tracks is the accumulated list, so the mirror tracks each
		// page as it lands.
		if m.loadedPlaylist != "" {
			m.providerQueueLen = 0
			m.providerQueueLastPath = ""
		} else {
			m.providerQueueLen = len(msg.tracks)
			m.providerQueueLastPath = ""
			if len(msg.tracks) > 0 {
				m.providerQueueLastPath = msg.tracks[len(msg.tracks)-1].Path
			}
		}
		m.adjustScroll()
		m.notifyAll()

	case smartRecommendsMsg:
		m.handleSmartRecommends(msg)
		return m, nil

	case navArtistsLoadedMsg:
		if !m.isCurrentNavRequest(msg.gen) {
			return m, nil
		}
		m.navBrowser.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Artist load failed: %s", msg.err)
			return m, nil
		}
		m.navBrowser.artists = msg.artists
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		return m, nil

	case navAlbumsLoadedMsg:
		if !m.isCurrentNavRequest(msg.gen) {
			return m, nil
		}
		m.navBrowser.albumLoading = false
		m.navBrowser.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Album load failed: %s", msg.err)
			return m, nil
		}
		if msg.offset == 0 {
			// Fresh load (new sort or drill-in): replace the list.
			m.navBrowser.albums = msg.albums
			m.navBrowser.albumDone = false
		} else {
			// Lazy-load page: append.
			m.navBrowser.albums = append(m.navBrowser.albums, msg.albums...)
		}
		if msg.isLast {
			m.navBrowser.albumDone = true
		}
		if msg.offset == 0 {
			m.navBrowser.cursor = 0
			m.navBrowser.scroll = 0
		}
		if m.navBrowser.search != "" {
			m.navUpdateSearch()
		}
		// If we just loaded the first page and it was a full menu → list transition,
		// also clear the general loading flag.
		return m, nil

	case navGenresLoadedMsg:
		if !m.isCurrentNavRequest(msg.gen) {
			return m, nil
		}
		m.navBrowser.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Genre load failed: %s", msg.err)
			return m, nil
		}
		m.navBrowser.genres = msg.genres
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		return m, nil

	case navTracksLoadedMsg:
		if !m.isCurrentNavRequest(msg.gen) {
			return m, nil
		}
		m.navBrowser.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Track load failed: %s", msg.err)
			return m, nil
		}
		if m.navBrowser.openInPlaylist {
			if len(msg.tracks) == 0 {
				m.status.Warning("No tracks found", statusTTLDefault)
				return m, nil
			}
			m.retireTracksPaging()
			m.replacePlayerPlaylist(msg.tracks)
			m.activeProviderPlaylistID = ""
			if pr, ok := m.navBrowser.prov.(playlist.RefreshablePlaylist); ok &&
				m.isActiveProvider(m.navBrowser.prov.Name()) && pr.CanRefreshPlaylist(m.navBrowser.selAlbum.ID) {
				m.activeProviderPlaylistID = m.navBrowser.selAlbum.ID
			}
			m.navBrowser.visible = false
			m.status.Successf(statusTTLDefault, "Replaced queue with %d tracks", len(msg.tracks))
			m.notifyAll()
			return m, nil
		}
		m.navBrowser.tracks = msg.tracks
		m.setHeaderStateFromTracks(m.navBrowser.tracks)
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		m.navBrowser.screen = navBrowseScreenTracks
		return m, nil

	case catalogBatchMsg:
		if msg.gen != m.requests.catalog || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.catalogBatch.loading = false
		if msg.err != nil {
			m.catalogBatch.done = true
			m.status.Errorf(statusTTLDefault, "Catalog load failed: %s", msg.err)
			return m, nil
		}
		if msg.added == 0 {
			m.catalogBatch.done = true
			return m, nil
		}
		if err := m.refreshProviderListsNow(); err != nil {
			m.err = err
		}
		m.catalogBatch.offset += msg.added
		if msg.added < catalogBatchSize {
			m.catalogBatch.done = true
		}
		return m, nil

	case catalogSearchMsg:
		if msg.gen != m.requests.catalog || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provLoading = false
		m.provSearch.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Search failed: %s", msg.err)
		} else {
			if err := m.refreshProviderListsNow(); err != nil {
				m.err = err
			}
			m.provCursor = 0
			m.provScroll = 0
			if msg.count == 0 {
				m.status.Warning("No results found", statusTTLDefault)
			}
		}
		return m, nil

	case ytdlBatchMsg:
		// Discard stale responses from a previous batch session.
		if msg.gen != m.ytdlBatch.gen {
			return m, nil
		}
		m.ytdlBatch.loading = false
		if msg.err != nil {
			m.ytdlBatch.done = true
			m.status.Errorf(statusTTLBatch, "Radio batch load failed: %v", msg.err)
			return m, nil
		}
		if len(msg.tracks) == 0 {
			m.ytdlBatch.done = true
			return m, nil
		}
		m.playlist.Add(msg.tracks...)
		m.loadedPlaylist = ""
		m.addToHeaderState(msg.tracks)
		m.ytdlBatch.offset += len(msg.tracks)
		if len(msg.tracks) < ytdlBatchSize {
			m.ytdlBatch.done = true
			return m, nil
		}
		// Immediately fetch the next batch.
		m.ytdlBatch.loading = true
		return m, fetchYTDLBatchCmd(m.ytdlBatch.gen, m.ytdlBatch.url, m.ytdlBatch.offset, ytdlBatchSize)

	case feedTrackResolvedMsg:
		m.feedLoading = false
		if len(msg.tracks) == 0 {
			m.status.Warning("No episodes found in feed.", statusTTLDefault)
			return m, nil
		}
		m.retireTracksPaging()
		m.replacePlaylist(msg.tracks)
		m.loadedPlaylist = ""
		m.resetProviderQueueMirror()
		m.setHeaderStateFromTracks(msg.tracks)
		m.plCursor = 0
		m.plScroll = 0
		m.applyHeightMode()
		m.adjustScroll()
		m.status.Showf(statusTTLDefault, "Loaded %d episode(s)", len(msg.tracks))
		playCmd := m.playCurrentTrack()
		m.notifyAll()
		return m, playCmd

	case subsEpisodesMsg:
		return m, m.handleSubsEpisodes(msg)

	case subsLatestAllMsg:
		return m, m.handleSubsLatestAll(msg)

	case feedsLoadedMsg:
		m.feedLoading = false
		if len(msg.tracks) > 0 {
			m.playlist.Add(msg.tracks...)
			m.loadedPlaylist = ""
			m.addToHeaderState(msg.tracks)
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
				m.notifyAll()
				if batchCmd != nil {
					return m, tea.Batch(playCmd, batchCmd)
				}
				return m, playCmd
			}
			if batchCmd != nil {
				return m, batchCmd
			}
		}
		return m, nil

	case netSearchResultsMsg:
		if msg.gen != m.requests.netSearch || !m.netSearch.active || msg.query != m.netSearch.request {
			return m, nil
		}
		m.netSearch.loading = false
		m.netSearch.cursor = 0
		m.netSearch.scroll = 0
		if msg.err != nil {
			m.netSearch.err = msg.err.Error()
			return m, nil
		}
		m.netSearch.results = msg.tracks
		m.netSearch.cursor = 0
		m.netSearch.screen = netSearchResults
		if len(msg.tracks) == 0 {
			m.netSearch.err = "No results found"
		}
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case lyricsLoadedMsg:
		if msg.gen != m.requests.lyrics || !m.lyrics.visible || msg.query != m.lyrics.query {
			return m, nil
		}
		m.lyrics.loading = false
		m.lyrics.err = msg.err
		m.lyrics.scroll = 0
		if msg.err == nil {
			m.lyrics.lines = msg.lines
		}
		return m, nil

	case fbTracksResolvedMsg:
		if len(msg.tracks) == 0 {
			m.status.Warning("No audio files found", statusTTLDefault)
			return m, nil
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
			cmd := m.refreshPaneAfterLocalWrite()
			return m, cmd
		}
		if msg.toPlaylist {
			return m, m.openPlaylistPicker(msg.tracks, fmt.Sprintf("%d tracks selected", len(msg.tracks)))
		}
		if msg.replace {
			m.player.Stop()
			m.player.ClearPreload()
			m.resetYTDLBatch()
			m.retireTracksPaging()
			m.replacePlaylist(msg.tracks)
			m.loadedPlaylist = ""
			m.resetProviderQueueMirror()
			m.setHeaderStateFromTracks(msg.tracks)
			m.plCursor = 0
			m.plScroll = 0
		} else {
			m.playlist.Add(msg.tracks...)
			m.loadedPlaylist = ""
			m.addToHeaderState(msg.tracks)
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
			cmd := m.playCurrentTrack()
			m.notifyAll()
			return m, cmd
		}
		return m, nil

	case streamPlayedMsg:
		track, _ := m.currentPlaybackTrack()
		if msg.gen != m.requests.stream || msg.path != track.Path {
			return m, nil
		}
		m.buffering = false
		var resumeCmd tea.Cmd
		if msg.err != nil {
			m.err = msg.err
			if track, idx := m.currentPlaybackTrack(); idx >= 0 {
				m.status.Errorf(statusTTLLong, "Couldn't play %s — track is gated, restricted, or unavailable.", track.DisplayName())
			}
		} else {
			m.err = nil
			m.reconnect.attempts = 0
			m.reconnect.at = time.Time{}
			resumeCmd = m.applyResume()
			m.nowPlaying(track)
		}
		m.notifyAll()
		preloadCmd := m.preloadNext()
		return m, tea.Batch(resumeCmd, preloadCmd)

	case streamPreloadedMsg:
		if msg.gen != m.requests.preload {
			return m, nil
		}
		m.preloading = false
		return m, nil

	case ytdlSavedMsg:
		m.save.finishDownload()
		if msg.err != nil {
			m.status.Errorf(statusTTLMedium, "Download failed: %s", msg.err)
		} else {
			m.status.Showf(statusTTLMedium, "Saved to %s", msg.path)
		}
		return m, nil

	case ytdlResolvedMsg:
		m.buffering = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Update the track with the downloaded local file and metadata.
		m.playlist.SetTrack(msg.index, msg.track)
		// Play the local file (seekable).
		cmd := m.playTrack(msg.track)
		m.notifyAll()
		return m, cmd

	case error:
		if errors.Is(msg, playlist.ErrNeedsAuth) {
			m.provLoading = false
			m.provSignIn = true
			m.err = nil
			return m, nil
		}
		m.err = msg
		m.provLoading = false
		m.feedLoading = false
		m.buffering = false
		return m, nil

	case spotSearchResultsMsg:
		if !m.isCurrentSpotRequest(msg.gen, msg.providerName) || m.spotSearch.query != msg.query {
			return m, nil
		}
		m.cancelSpotRequest()
		m.spotSearch.loading = false
		m.spotSearch.cursor = 0
		m.spotSearch.scroll = 0
		if msg.err != nil {
			m.setSpotSearchError(msg.err.Error())
			return m, nil
		}
		m.spotSearch.results = msg.tracks
		m.spotSearch.cursor = 0
		m.spotSearch.screen = spotSearchResults
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case spotAlbumTracksMsg:
		if msg.gen != m.requests.spotAlbum {
			return m, nil
		}
		m.cancelSpotRequest()
		m.spotSearch.albumLoading = false
		if msg.err != nil {
			m.setSpotSearchError(msg.err.Error())
			return m, nil
		}
		if len(msg.tracks) == 0 {
			m.setSpotSearchError("That album has no tracks available here.")
			return m, nil
		}
		album := msg.album
		tracks := msg.tracks
		m.closeSpotSearch()
		switch msg.action {
		case spotAlbumAppend:
			cmd := m.appendAlbum(album, tracks)
			return m, cmd
		case spotAlbumQueueNext:
			cmd := m.queueAlbumNext(album, tracks)
			return m, cmd
		default:
			cmd := m.playAlbumImmediate(album, tracks)
			return m, cmd
		}

	case spotPlaylistsMsg:
		if !m.isCurrentSpotListRequest(msg.gen, msg.providerName) {
			return m, nil
		}
		m.spotSearch.loading = false
		m.spotSearch.cursor = 0
		m.spotSearch.scroll = 0
		if msg.err != nil {
			m.setSpotSearchError(msg.err.Error())
			return m, nil
		}
		m.spotSearch.playlists = msg.playlists
		m.spotSearch.cursor = 0
		m.spotSearch.screen = spotSearchPlaylist
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case spotAddedMsg:
		if !m.isCurrentSpotMutation(msg.gen, msg.providerName) {
			return m, nil
		}
		m.cancelSpotRequest()
		m.spotSearch.loading = false
		if msg.err != nil {
			m.setSpotSearchError("Add failed: " + msg.err.Error())
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Added to %q", msg.name)
		m.closeSpotSearch()
		return m, nil

	case spotCreatedMsg:
		if !m.isCurrentSpotMutation(msg.gen, msg.providerName) {
			return m, nil
		}
		m.cancelSpotRequest()
		m.spotSearch.loading = false
		if msg.err != nil {
			m.setSpotSearchError("Create failed: " + msg.err.Error())
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Created %q & added track", msg.name)
		m.closeSpotSearch()
		return m, nil

	case spotSearchAllMsg:
		if !m.isCurrentSpotRequest(msg.gen, msg.providerName) || m.spotSearch.query != msg.query {
			return m, nil
		}
		m.cancelSpotRequest()
		m.spotSearch.loading = false
		m.spotSearch.cursor = 0
		m.spotSearch.scroll = 0
		m.spotSearch.tab = spotTabTracks
		m.spotSearch.drill = nil
		if msg.err != nil {
			m.spotSearch.err = msg.err.Error()
			return m, nil
		}
		m.spotSearch.resultsAll = msg.results
		m.spotSearch.screen = spotSearchResults
		if m.spotSearch.resultsAllCount() == 0 {
			m.spotSearch.err = "No results found"
		}
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case spotDrillLoadedMsg:
		if !m.isCurrentSpotRequest(msg.gen, msg.providerName) || len(m.spotSearch.drill) == 0 {
			return m, nil
		}
		lvl := &m.spotSearch.drill[len(m.spotSearch.drill)-1]
		if lvl.crumb != msg.crumb {
			return m, nil
		}
		lvl.loading = false
		if msg.err != nil {
			m.spotSearch.drill = m.spotSearch.drill[:len(m.spotSearch.drill)-1]
			m.spotSearch.err = "Load failed: " + msg.err.Error()
			return m, nil
		}
		lvl.albums = msg.albums
		lvl.tracks = msg.tracks
		lvl.cursor = 0
		lvl.scroll = 0
		if m.spotDrillCount(*lvl) == 0 {
			m.spotSearch.err = "No tracks found"
		}
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case artistDetailMsg:
		if !m.isCurrentArtistRequest(msg.gen, msg.providerName) || msg.artistID != m.artist.info.ID {
			return m, nil
		}
		m.cancelArtistRequest()
		m.artist.loading = false
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "Artist load failed: %s", msg.err)
			m.closeArtistScreen()
			return m, nil
		}
		m.artist.detail = msg.detail
		m.artist.cursor = 0
		m.artist.scroll = 0
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case artistAlbumTracksMsg:
		if !m.isCurrentArtistRequest(msg.gen, msg.providerName) || msg.artistID != m.artist.info.ID || len(m.artist.drill) == 0 {
			return m, nil
		}
		lvl := &m.artist.drill[len(m.artist.drill)-1]
		if lvl.crumb != msg.crumb {
			return m, nil
		}
		lvl.loading = false
		if msg.err != nil {
			m.artist.drill = m.artist.drill[:len(m.artist.drill)-1]
			m.status.Showf(statusTTLDefault, "Album load failed: %s", msg.err)
			return m, nil
		}
		lvl.tracks = msg.tracks
		lvl.cursor = 0
		lvl.scroll = 0
		if len(lvl.tracks) == 0 {
			m.status.Show("No tracks found", statusTTLDefault)
		}
		m.applyHeightMode()
		m.clampActiveScrollState()
		return m, nil

	case homeListsMsg:
		if !m.isCurrentHomeSectionRequest(msg.gen, msg.providerName, &m.requests.homeLists) {
			return m, nil
		}
		m.handleHomeLists(msg)
		return m, nil

	case homeAlbumsMsg:
		if !m.isCurrentHomeSectionRequest(msg.gen, msg.providerName, &m.requests.homeAlbums) {
			return m, nil
		}
		m.handleHomeAlbums(msg)
		return m, nil

	case homeArtistsMsg:
		if !m.isCurrentHomeSectionRequest(msg.gen, msg.providerName, &m.requests.homeArtists) {
			return m, nil
		}
		m.handleHomeArtists(msg)
		return m, nil

	case homeContentMsg:
		if !m.isCurrentHomeContentRequest(msg.gen, msg.providerName, msg.kind, msg.id) {
			return m, nil
		}
		return m, m.handleHomeContent(msg)

	case homePageMsg:
		if !m.isCurrentHomeContentRequest(msg.gen, msg.providerName, homeContentPlaylist, msg.playlistID) {
			return m, nil
		}
		return m, m.handleHomePage(msg)

	case homeCreatedMsg:
		if !m.isCurrentHomeSectionRequest(msg.gen, msg.providerName, &m.requests.homeCreate) {
			return m, nil
		}
		m.home.creating = false
		if msg.err != nil {
			m.home.inputErr = "Create failed: " + msg.err.Error()
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Created %q", msg.name)
		m.home.screen = homeScreenLibrary
		m.home.newName = ""
		m.home.inputErr = ""
		m.home.fixupID = msg.playlistID
		m.home.loadingLists = true
		return m, fetchHomeListsCmd(m.home.prov, msg.providerName, nextRequest(&m.requests.homeLists))

	case trackLikeToggledMsg:
		if msg.gen != m.requests.like {
			return m, nil
		}
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "Like failed: %s", msg.err)
			return m, nil
		}
		if msg.liked {
			m.status.Success("Added to liked tracks", statusTTLDefault)
		} else {
			m.status.Success("Removed from liked tracks", statusTTLDefault)
		}
		return m, nil

	case playlistUnfollowedMsg:
		if msg.gen != m.requests.provMutation || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "Delete failed: %s", msg.err)
			return m, nil
		}
		if msg.owned {
			m.status.Showf(statusTTLDefault, "Deleted %q", msg.name)
		} else {
			m.status.Showf(statusTTLDefault, "Unfollowed %q", msg.name)
		}
		if msg.playlistID == m.activeProviderPlaylistID {
			m.resetProviderQueueMirror()
		}
		// Refresh the list off the Update goroutine; the resulting
		// playlistsLoadedMsg clamps the cursor to the shrunken list.
		m.provListFixup = provListFixupState{clampCursor: true}
		return m, m.fetchProviderPlaylists()

	case playlistRenamedMsg:
		if msg.gen != m.requests.provMutation || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "Rename failed: %s", msg.err)
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Renamed to %q", msg.newName)
		// Refresh the list off the Update goroutine; the resulting
		// playlistsLoadedMsg reselects the renamed row.
		m.provListFixup = provListFixupState{selectID: msg.playlistID}
		return m, m.fetchProviderPlaylists()

	case remoteTrackRemovedMsg:
		// Removes are matched against the queue mirror (playlist + position +
		// track) rather than only the mutation generation: a second remove
		// issued while one is in flight bumps the generation and would
		// otherwise discard the first completion after the remote removal
		// already succeeded.
		if !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		if msg.err != nil {
			if msg.gen == m.requests.provMutation {
				m.status.Showf(statusTTLDefault, "Remove failed: %s", msg.err)
			}
			return m, nil
		}
		if msg.gen == m.requests.provMutation {
			m.status.Successf(statusTTLDefault, "Removed from playlist: %s", msg.trackName)
		}
		wasActive := msg.position == m.playlist.Index()
		if tracks := m.playlist.Tracks(); msg.playlistID == m.activeProviderPlaylistID &&
			msg.position >= 0 && msg.position < len(tracks) && tracks[msg.position].Path == msg.trackPath {
			m.playlist.Remove(msg.position)
			if m.providerQueueLen > msg.position {
				m.providerQueueLen--
			}
			if tracks := m.playlist.Tracks(); m.providerQueueLen > 0 && m.providerQueueLen <= len(tracks) {
				m.providerQueueLastPath = tracks[m.providerQueueLen-1].Path
			} else {
				m.providerQueueLastPath = ""
			}
			if newLen := m.playlist.Len(); newLen == 0 {
				m.plCursor = 0
			} else if m.plCursor >= newLen {
				m.plCursor = newLen - 1
			}
			if wasActive {
				m.player.Stop()
				m.player.ClearPreload()
				m.clearPlaybackTrack()
			}
			m.adjustScroll()
		} else if msg.gen == m.requests.provMutation {
			// Freshest completion failed the mirror check: the queue no longer
			// matches the remote playlist, so disarm remote removes.
			m.providerQueueLen = 0
			m.providerQueueLastPath = ""
		}
		for i := range m.providerLists {
			if m.providerLists[i].ID == msg.playlistID && m.providerLists[i].TrackCount > 0 {
				m.providerLists[i].TrackCount--
			}
		}
		return m, nil

	case artistFollowedMsg:
		if msg.gen != m.requests.follow {
			return m, nil
		}
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "Follow failed: %s", msg.err)
			return m, nil
		}
		m.setFollowed(followKey("artist", msg.providerName, msg.artistID), msg.follow)
		if msg.follow {
			m.status.Showf(statusTTLDefault, "Following %s", msg.artistName)
		} else {
			m.status.Showf(statusTTLDefault, "Unfollowed %s", msg.artistName)
		}
		// Refresh the nav artist list when it is showing this provider's artists.
		if ab, ok := m.navArtistBrowserFor(msg.providerName); ok {
			return m, fetchNavArtistsCmd(ab, m.nextNavRequest())
		}
		return m, nil

	case playlistFollowedMsg:
		if msg.gen != m.requests.follow {
			return m, nil
		}
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "Follow failed: %s", msg.err)
			return m, nil
		}
		m.setFollowed(followKey("playlist", msg.providerName, msg.playlistID), msg.follow)
		if msg.follow {
			m.status.Showf(statusTTLDefault, "Followed playlist %q", msg.playlistName)
		} else {
			m.status.Showf(statusTTLDefault, "Unfollowed playlist %q", msg.playlistName)
		}
		if m.provider != nil && m.provider.Name() == msg.providerName {
			return m, m.fetchProviderPlaylists()
		}
		return m, nil

	case plPickerRemoteMsg:
		if !m.plPicker.visible || m.plPicker.remoteName != msg.providerName || !m.plPicker.remoteLoading {
			return m, nil
		}
		m.plPicker.remoteLoading = false
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "%s playlists unavailable: %s", msg.providerName, msg.err)
			return m, nil
		}
		m.plPicker.remote = filterRemotePickerPlaylists(msg.playlists)
		m.plPickerMaybeAdjustScroll(m.plPickerVisible())
		return m, nil

	case pickerRemoteWriteMsg:
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "Add failed: %s", msg.err)
			return m, nil
		}
		switch {
		case msg.created && msg.added > 0:
			m.status.Showf(statusTTLBatch, "Created %q & added %d tracks", msg.name, msg.added)
		case msg.created:
			m.status.Showf(statusTTLDefault, "Created %q", msg.name)
		case msg.added > 0 && msg.skipped > 0:
			m.status.Showf(statusTTLBatch, "Added %d to %q, skipped %d duplicates", msg.added, msg.name, msg.skipped)
		case msg.added > 0:
			m.status.Showf(statusTTLDefault, "Added %d to %q", msg.added, msg.name)
		default:
			m.status.Showf(statusTTLDefault, "Nothing added to %q", msg.name)
		}
		if m.provider != nil && m.provider.Name() == msg.providerName {
			return m, m.fetchProviderPlaylists()
		}
		return m, nil

	case provAuthDoneMsg:
		if msg.gen != m.requests.auth || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provAuthURL = ""
		if msg.err != nil {
			m.err = msg.err
			m.provLoading = false
			m.provSignIn = false
			return m, nil
		}
		m.provSignIn = false
		m.provLoading = true
		cmd := m.fetchProviderPlaylists()
		return m, cmd

	case ProvAuthURLMsg:
		if !m.provLoading || !m.isActiveProvider(msg.ProviderName) {
			return m, nil
		}
		m.provAuthURL = msg.URL
		return m, nil

	case devicesListedMsg:
		m.devicePicker.loading = false
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Device list failed: %s", msg.err)
			m.devicePicker.visible = false
		} else {
			m.devicePicker.devices = msg.devices
		}
		return m, nil

	case deviceSwitchedMsg:
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "Switch failed: %s", msg.err)
		} else {
			m.status.Showf(statusTTLDefault, "Audio output: %s", msg.name)
			_ = m.configSaver.Save("audio_device", msg.name)
		}
		// Invalidate cached list so the next open refreshes Active markers.
		m.devicePicker.devices = nil
		return m, nil

	case attachNotifierMsg:
		m.attachNotifier(msg.notifier)
		return m, nil

	case playback.PlayPauseMsg:
		cmd := m.togglePlayPause()
		m.notifyAll()
		return m, cmd

	case playback.PlayMsg:
		if !m.player.IsPlaying() || m.player.IsPaused() {
			cmd := m.togglePlayPause()
			m.notifyAll()
			return m, cmd
		}
		return m, nil

	case playback.PauseMsg:
		if m.player.IsPlaying() && !m.player.IsPaused() {
			m.togglePlayerPause()
			m.notifyAll()
		}
		return m, nil

	case playback.NextMsg:
		refresh := m.scrobbleCurrent()
		cmd := m.nextTrack()
		m.notifyAll()
		return m, tea.Batch(refresh, cmd)

	case playback.PrevMsg:
		refresh := m.scrobbleCurrent()
		cmd := m.prevTrack()
		m.notifyAll()
		return m, tea.Batch(refresh, cmd)

	case playback.SeekMsg:
		_ = m.player.Seek(msg.Offset)
		m.notifyAll()
		return m, nil

	case playback.SetPositionMsg:
		cmd := m.seekAbsolute(msg.Position)
		return m, cmd

	case playback.SetVolumeMsg:
		m.player.SetVolume(msg.VolumeDB)
		m.notifyAll()
		return m, nil

	case playback.StopMsg:
		m.stopPlayback()
		m.notifyAll()
		return m, nil

	case playback.QuitMsg:
		m.flushPendingSpeedSave()
		m.flushPendingEQSave()
		m.player.Close()
		m.clearPlaybackTrack()
		m.quitting = true
		return m, tea.Quit

	case SetEQPresetMsg:
		m.SetEQPreset(msg.Name, msg.Bands)
		m.scheduleEQSave()
		return m, nil

	case SetEQBandMsg:
		m.setCustomEQBand(msg.Band, msg.Gain)
		return m, nil

	case PluginQueueMsg:
		cmd := m.handlePluginQueue(msg)
		return m, cmd

	case pluginQueueAddedMsg:
		if len(msg.tracks) > 0 {
			m.playlist.Add(msg.tracks...)
			m.loadedPlaylist = ""
			m.notifyPlayback()
		}
		return m, nil

	case ShowStatusMsg:
		ttl := statusTTLDefault
		if msg.Duration > 0 {
			ttl = statusTTL(msg.Duration)
		}
		m.status.Show(msg.Text, ttl)
		return m, nil

	case ipc.LoadMsg:
		tracks, err := m.localProvider.Tracks(msg.Playlist)
		if err != nil {
			if msg.Reply != nil {
				msg.Reply <- ipc.Response{OK: false, Error: fmt.Sprintf("playlist %q: %v", msg.Playlist, err)}
			}
			return m, nil
		}
		m.retireTracksPaging()
		m.replacePlaylist(tracks)
		m.resetProviderQueueMirror()
		m.setHeaderStateFromTracks(tracks)
		if msg.Playlist != history.PlaylistName {
			m.loadedPlaylist = msg.Playlist
		} else {
			m.loadedPlaylist = ""
		}
		cmd := m.playCurrentTrack()
		m.notifyAll()
		if msg.Reply != nil {
			msg.Reply <- ipc.Response{OK: true, Playlist: msg.Playlist, Total: len(tracks)}
		}
		return m, cmd
	case ipc.QueueMsg:
		t := playlist.TrackFromPath(msg.Path)
		m.playlist.Add(t)
		m.loadedPlaylist = ""
		m.addToHeaderState([]playlist.Track{t})
		m.notifyAll()
		return m, nil
	case ipc.ThemeMsg:
		// Reload themes from disk to pick up new custom themes.
		// Same pattern as openThemePicker() — LoadAll is fast (<1ms for local TOML files).
		m.themes = theme.LoadAll()
		if m.SetTheme(msg.Name) {
			// Persist immediately so the setting survives ungraceful exits.
			themeName := msg.Name
			if strings.EqualFold(themeName, "default") {
				themeName = ""
			}
			_ = m.configSaver.Save("theme", fmt.Sprintf("%q", themeName))
			if msg.Reply != nil {
				msg.Reply <- ipc.Response{OK: true}
			}
		} else {
			if msg.Reply != nil {
				msg.Reply <- ipc.Response{OK: false, Error: fmt.Sprintf("theme %q not found", msg.Name)}
			}
		}
		return m, nil
	case ipc.VisMsg:
		if m.vis == nil {
			if msg.Reply != nil {
				msg.Reply <- ipc.Response{OK: false, Error: "visualizer not available"}
			}
			return m, nil
		}
		var resp ipc.Response
		if strings.EqualFold(msg.Name, "next") {
			m.vis.CycleMode()
			m.vis.RequestRefresh()
			m.refreshChrome()
			resp = ipc.Response{OK: true, Visualizer: m.vis.ModeName()}
		} else if m.SetVisualizer(msg.Name) {
			resp = ipc.Response{OK: true, Visualizer: m.vis.ModeName()}
		} else {
			resp = ipc.Response{OK: false, Error: fmt.Sprintf("visualizer %q not found", msg.Name)}
		}
		if msg.Reply != nil {
			msg.Reply <- resp
		}
		return m, nil
	case ipc.ShuffleMsg:
		switch strings.ToLower(msg.Name) {
		case "on":
			if !m.playlist.Shuffled() {
				m.playlist.ToggleShuffle()
			}
		case "off":
			if m.playlist.Shuffled() {
				m.playlist.ToggleShuffle()
			}
		default: // "toggle" or empty
			m.playlist.ToggleShuffle()
		}
		shuffled := m.playlist.Shuffled()
		if err := m.configSaver.Save("shuffle", fmt.Sprintf("%v", shuffled)); err != nil {
			m.status.Errorf(statusTTLDefault, "Config save failed: %s", err)
		}
		cmd := m.rearmPreload()
		if msg.Reply != nil {
			msg.Reply <- ipc.Response{OK: true, Shuffle: &shuffled}
		}
		return m, cmd

	case ipc.RepeatMsg:
		switch strings.ToLower(msg.Name) {
		case "off":
			m.playlist.SetRepeat(playlist.RepeatOff)
		case "all":
			m.playlist.SetRepeat(playlist.RepeatAll)
		case "one":
			m.playlist.SetRepeat(playlist.RepeatOne)
		default: // "cycle" or empty
			m.playlist.CycleRepeat()
		}
		mode := m.playlist.Repeat()
		if err := m.configSaver.Save("repeat", fmt.Sprintf("%q", mode.String())); err != nil {
			m.status.Errorf(statusTTLDefault, "Config save failed: %s", err)
		}
		cmd := m.rearmPreload()
		if msg.Reply != nil {
			msg.Reply <- ipc.Response{OK: true, Repeat: mode.String()}
		}
		return m, cmd

	case ipc.MonoMsg:
		switch strings.ToLower(msg.Name) {
		case "on":
			if !m.player.Mono() {
				m.player.ToggleMono()
			}
		case "off":
			if m.player.Mono() {
				m.player.ToggleMono()
			}
		default: // "toggle" or empty
			m.player.ToggleMono()
		}
		mono := m.player.Mono()
		if msg.Reply != nil {
			msg.Reply <- ipc.Response{OK: true, Mono: &mono}
		}
		return m, nil

	case ipc.SpeedMsg:
		m.player.SetSpeed(msg.Speed)
		m.saveSpeed()
		if msg.Reply != nil {
			msg.Reply <- ipc.Response{OK: true, Speed: m.player.Speed()}
		}
		return m, nil

	case ipc.EQMsg:
		if msg.Band > 0 || (msg.Band == 0 && msg.Name == "") {
			// Set a specific band (0-9).
			m.setCustomEQBand(msg.Band, msg.Value)
			if msg.Reply != nil {
				msg.Reply <- ipc.Response{OK: true, EQPreset: m.EQPresetName()}
			}
		} else if msg.Name != "" {
			// Apply a preset by name.
			m.SetEQPreset(msg.Name, nil)
			m.scheduleEQSave()
			if msg.Reply != nil {
				msg.Reply <- ipc.Response{OK: true, EQPreset: m.EQPresetName()}
			}
		} else {
			if msg.Reply != nil {
				msg.Reply <- ipc.Response{OK: false, Error: "eq requires a preset name or --band"}
			}
		}
		return m, nil

	case ipc.DeviceMsg:
		if strings.EqualFold(msg.Name, "list") {
			devices, err := player.ListAudioDevices()
			if err != nil {
				if msg.Reply != nil {
					msg.Reply <- ipc.Response{OK: false, Error: fmt.Sprintf("list devices: %v", err)}
				}
				return m, nil
			}
			// Encode device list as newline-separated string in the Device field.
			var lines []string
			items := make([]ipc.DeviceInfo, 0, len(devices))
			for _, d := range devices {
				marker := "  "
				if d.Active {
					marker = "* "
				}
				lines = append(lines, fmt.Sprintf("%s%s", marker, d.Name))
				items = append(items, ipc.DeviceInfo{Name: d.Name, Active: d.Active})
			}
			if msg.Reply != nil {
				msg.Reply <- ipc.Response{OK: true, Device: strings.Join(lines, "\n"), Devices: items}
			}
			return m, nil
		}
		err := player.SwitchAudioDevice(msg.Name)
		if err != nil {
			if msg.Reply != nil {
				msg.Reply <- ipc.Response{OK: false, Error: fmt.Sprintf("switch device: %v", err)}
			}
			return m, nil
		}
		_ = m.configSaver.Save("audio_device", msg.Name)
		m.status.Showf(statusTTLDefault, "Audio output: %s", msg.Name)
		// Invalidate cached list so the next open refreshes Active markers.
		m.devicePicker.devices = nil
		if msg.Reply != nil {
			msg.Reply <- ipc.Response{OK: true, Device: msg.Name}
		}
		return m, nil

	case ipc.QueueRequestMsg:
		cmd := m.handleIPCQueue(msg)
		return m, cmd

	case ipc.LibraryRequestMsg:
		cmd := m.handleIPCLibrary(msg)
		return m, cmd

	case ipcProviderLoadResult:
		cmd := m.handleIPCProviderLoad(msg)
		return m, cmd

	case ipcFeedLoadResult:
		cmd := m.handleIPCFeedLoad(msg)
		return m, cmd

	case ipc.LyricsRequestMsg:
		cmd := m.handleIPCLyrics(msg)
		return m, cmd

	case ipc.HistoryRequestMsg:
		cmd := m.handleIPCHistory(msg)
		return m, cmd

	case ipc.URLRequestMsg:
		cmd := m.handleIPCURL(msg)
		return m, cmd

	case ipcURLLoadResult:
		cmd := m.handleIPCURLResult(msg)
		return m, cmd

	case ipc.SaveRequestMsg:
		cmd := m.handleIPCSave(msg)
		return m, cmd

	case V2RequestMsg:
		cmd := m.handleV2Request(msg)
		return m, cmd

	case ipcV2ResponseMsg:
		if msg.Response.OK {
			if msg.Operation == "device" && msg.Response.Device != "" {
				_ = m.configSaver.Save("audio_device", msg.Response.Device)
				m.devicePicker.devices = nil
			}
			m.completeV2Job(msg.Jobs, msg.JobID, msg.Response)
		} else {
			err := v2InternalError()
			err.Detail = msg.Response.Error
			m.failV2Job(msg.Jobs, msg.JobID, err)
		}
		return m, nil

	}

	return m, nil
}
