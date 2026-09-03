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
			return m, m.commitPendingSeek()
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
			return m, m.preloadNext()
		}
		if msg.resume {
			m.status.Showf(statusTTLDefault, "Resumed at %s", formatJumpClock(msg.target))
		}
		m.finishSeek()
		return m, m.preloadNext()

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
				m.player.Stop()
				m.clearPlaybackTrack()
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
			m.notifyAll()
		}
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
		// connection while the first preloadStreamCmd goroutine is still running.
		if m.player.IsPlaying() && !m.player.IsPaused() && !m.buffering && !m.preloading && !m.player.HasPreload() {
			if cmd := m.preloadNext(); cmd != nil {
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
		return m, m.openDefaultProviderBrowser()

	case playlistsLoadedMsg:
		if msg.gen != m.requests.provider || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provLoading = false
		if msg.err != nil {
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
		m.providerLists = providerListsWithBrowse(m.provider, msg.playlists)
		// Start loading catalog when the provider supports lazy catalog loading.
		if loader, ok := m.provider.(provider.CatalogLoader); ok && !m.catalogBatch.loading && !m.catalogBatch.done {
			m.catalogBatch.loading = true
			return m, m.fetchCatalogBatch(loader)
		}
		return m, nil

	case tracksLoadedMsg:
		if msg.gen != m.requests.tracks || !m.isActiveProvider(msg.providerName) {
			return m, nil
		}
		m.provLoading = false
		if msg.err != nil {
			if errors.Is(msg.err, playlist.ErrNeedsAuth) {
				m.provSignIn = true
				m.err = nil
				return m, nil
			}
			m.err = msg.err
			return m, nil
		}
		m.replacePlayerPlaylist(msg.tracks)
		if msg.playlistExact && m.localProvider != nil && msg.providerName == m.localProvider.Name() && msg.playlistID != history.PlaylistName {
			m.loadedPlaylist = msg.playlistID
		}
		m.applyTracksResume(msg)
		m.adjustScroll()
		m.notifyAll()
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
			m.replacePlayerPlaylist(msg.tracks)
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
			m.status.Error("Catalog load failed", statusTTLDefault)
			return m, nil
		}
		if msg.added == 0 {
			m.catalogBatch.done = true
			return m, nil
		}
		if lists, err := m.provider.Playlists(); err == nil {
			m.providerLists = providerListsWithBrowse(m.provider, lists)
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
		if msg.err != nil {
			m.status.Error("Search failed", statusTTLDefault)
		} else {
			if lists, err := m.provider.Playlists(); err == nil {
				m.providerLists = providerListsWithBrowse(m.provider, lists)
			}
			m.provCursor = 0
			m.provScroll = 0
			if msg.count == 0 {
				m.status.Warning("No stations found", statusTTLDefault)
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
		m.replacePlaylist(msg.tracks)
		m.loadedPlaylist = ""
		m.setHeaderStateFromTracks(msg.tracks)
		m.plCursor = 0
		m.plScroll = 0
		m.applyHeightMode()
		m.adjustScroll()
		m.status.Showf(statusTTLDefault, "Loaded %d episode(s)", len(msg.tracks))
		playCmd := m.playCurrentTrack()
		m.notifyAll()
		return m, playCmd

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
			return m, m.refreshPaneAfterLocalWrite()
		}
		if msg.toPlaylist {
			m.openPlaylistPicker(msg.tracks, fmt.Sprintf("%d tracks selected", len(msg.tracks)))
			return m, nil
		}
		if msg.replace {
			m.player.Stop()
			m.player.ClearPreload()
			m.resetYTDLBatch()
			m.replacePlaylist(msg.tracks)
			m.loadedPlaylist = ""
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
		}
		m.notifyAll()
		return m, tea.Batch(resumeCmd, m.preloadNext())

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
			return m, m.appendAlbum(album, tracks)
		case spotAlbumQueueNext:
			return m, m.queueAlbumNext(album, tracks)
		default:
			return m, m.playAlbumImmediate(album, tracks)
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
		return m, m.fetchProviderPlaylists()

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
		return m, m.seekAbsolute(msg.Position)

	case playback.SetVolumeMsg:
		m.player.SetVolume(msg.VolumeDB)
		m.notifyAll()
		return m, nil

	case playback.StopMsg:
		m.player.Stop()
		m.clearPlaybackTrack()
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
		return m, m.handlePluginQueue(msg)

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
		m.replacePlaylist(tracks)
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
		return m, m.handleIPCQueue(msg)

	case ipc.LibraryRequestMsg:
		return m, m.handleIPCLibrary(msg)

	case ipcProviderLoadResult:
		return m, m.handleIPCProviderLoad(msg)

	case ipc.LyricsRequestMsg:
		return m, m.handleIPCLyrics(msg)

	case ipc.HistoryRequestMsg:
		return m, m.handleIPCHistory(msg)

	case ipc.URLRequestMsg:
		return m, m.handleIPCURL(msg)

	case ipcURLLoadResult:
		return m, m.handleIPCURLResult(msg)

	case ipc.SaveRequestMsg:
		return m, m.handleIPCSave(msg)

	case V2RequestMsg:
		return m, m.handleV2Request(msg)

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
