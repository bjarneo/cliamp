package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cliamp/config"
	"cliamp/mpris"
	"cliamp/playlist"
	"cliamp/provider"
	"cliamp/ui"
)

// Update handles messages: key presses, ticks, and window resizes.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasScreen := m.activeScreen()
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
		m.maybeRequestVisualizerRefresh(msg, wasScreen, wasMode, wasPlaying, wasPaused)
	}()

	switch msg := msg.(type) {
	case tea.KeyMsg:
		cmd := m.handleKey(msg)
		if m.quitting {
			return m, tea.Quit
		}
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
		// Dynamic frame width: use full terminal width, or cap at 80 in compact mode.
		frameW := msg.Width
		if m.compact {
			frameW = min(frameW, 80)
		}
		ui.FrameStyle = ui.FrameStyle.Width(frameW)
		ui.PanelWidth = max(0, frameW-2*ui.PaddingH)
		if m.fullVis {
			m.vis.Rows = max(ui.DefaultVisRows, (m.height-10)*4/5)
		}
		m.plVisible = m.defaultPlVisible()
		return m, m.terminalTitleCmd()

	case seekTickMsg:
		// Async yt-dlp seek completed.
		// Only clear seekActive if no new seek keypresses arrived during loading.
		if m.seek.timer <= 0 {
			m.seek.active = false
		}
		// Grace period: suppress reconnect for a few ticks after seek completes.
		m.seek.grace = 10
		m.seek.graceFor = 0
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return m, nil

	case tickMsg:
		now := time.Time(msg)
		dt := m.tickDelta(now)

		// Cache expensive player state once per tick so View() render
		// functions don't re-acquire speaker.Lock() multiple times.
		if !m.buffering {
			m.cachedPos = m.displayPosition()
			m.cachedDur = m.player.Duration()
		} else {
			track, _ := m.playlist.Current()
			m.cachedDur = time.Duration(track.DurationSecs) * time.Second
			m.cachedPos = 0
		}
		m.tickVisualizer(now)
		// Process debounced yt-dlp seek.
		var seekCmd tea.Cmd
		if cmd := m.tickSeek(dt); cmd != nil {
			seekCmd = cmd
		}
		// Expire temporary status messages.
		if !m.status.expiresAt.IsZero() && !now.Before(m.status.expiresAt) {
			m.status.Clear()
		}
		m.tickPendingSpeedSave(dt)
		// Decrement seek grace period.
		advanceTickUnits(&m.seek.grace, &m.seek.graceFor, dt, ui.TickFast)
		// Surface stream errors (e.g., connection drops) and auto-reconnect streams.
		// Suppress during yt-dlp seek and grace period — killing the old pipeline
		// triggers a transient error that can persist for a few ticks.
		if err := m.player.StreamErr(); err != nil && !m.seek.active && m.seek.grace == 0 {
			track, idx := m.playlist.Current()
			isStream := idx >= 0 && (track.Stream || playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path))
			if isStream && m.reconnect.attempts < 5 {
				// Schedule reconnect with exponential backoff: 1s, 2s, 4s, 8s, 16s
				if m.reconnect.at.IsZero() {
					delay := time.Second << m.reconnect.attempts
					m.reconnect.at = now.Add(delay)
					m.reconnect.attempts++
					m.err = fmt.Errorf("Reconnecting in %s...", delay)
				}
			} else {
				m.err = err
				m.reconnect.at = time.Time{}
			}
		}
		var lyricCmd tea.Cmd
		// Poll ICY stream title for live radio display.
		if title := m.player.StreamTitle(); title != "" && title != m.streamTitle {
			m.streamTitle = title
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
						lyricCmd = fetchLyricsCmd(artist, song)
					}
				}
			}
		}
		m.network.sampleFor += dt
		if m.network.sampleFor >= time.Second {
			m.notifyAll()
			downloaded, _ := m.player.StreamBytes()
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
			m.player.Stop()
			if track, idx := m.playlist.Current(); idx >= 0 {
				return m, tea.Batch(m.playTrack(track), tickCmdAt(ui.TickFast))
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
		if m.player.GaplessAdvanced() {
			// Capture the track that just finished before advancing the playlist.
			// For gapless, the track played fully (100% ≥ 50%), so elapsed = duration.
			finishedTrack, _ := m.playlist.Current()
			fullDur := time.Duration(finishedTrack.DurationSecs) * time.Second
			m.maybeScrobble(finishedTrack, fullDur, fullDur)

			m.playlist.Next()
			m.plCursor = m.playlist.Index()
			m.adjustScroll()
			m.titleOff = 0
			// The preload that just fired is consumed — clear the in-flight flag
			// so the next track can be preloaded.
			m.preloading = false
			// A stream decoder error at the track boundary (e.g., server closing
			// the connection when the preload HTTP request opens) is expected and
			// not a user-visible problem. Clear any pending error so the red
			// message doesn't flash at every track transition.
			m.err = nil
			// Fire now-playing notification for the track the audio engine just
			// started. playTrack() is not called on this path, so we must notify
			// here explicitly.
			if newTrack, idx := m.playlist.Current(); idx >= 0 {
				m.nowPlaying(newTrack)
			}
			cmds = append(cmds, m.preloadNext())
			m.notifyAll()
		}
		// Check if gapless drained (end of playlist, no preloaded next).
		// Skip if already buffering a yt-dlp download to avoid advancing
		// the playlist on every tick while waiting for the resolve.
		if m.player.IsPlaying() && !m.player.IsPaused() && m.player.Drained() && !m.buffering && m.reconnect.at.IsZero() {
			// Track drained to end — always ≥ 50%.
			finishedTrack, _ := m.playlist.Current()
			drainDur := time.Duration(finishedTrack.DurationSecs) * time.Second
			m.maybeScrobble(finishedTrack, drainDur, drainDur)

			// Stop the player before dispatching the async nextTrack command.
			// This clears the gapless streamer so the finished track cannot
			// replay while waiting for a yt-dlp pipe chain to spin up.
			m.player.Stop()
			cmds = append(cmds, m.nextTrack())
			m.notifyAll()
		}
		if m.player.IsPlaying() && !m.player.IsPaused() {
			if now.Sub(m.titleLastScroll) >= 200*time.Millisecond {
				m.titleOff++
				m.titleLastScroll = now
			}
		}
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
		if cmd := m.terminalTitleCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, tickCmdAt(m.tickInterval()))
		return m, tea.Batch(cmds...)

	case []playlist.PlaylistInfo:
		m.providerLists = msg
		m.provLoading = false
		// Start loading catalog when the provider supports lazy catalog loading.
		if loader, ok := m.provider.(provider.CatalogLoader); ok && !m.catalogBatch.loading && !m.catalogBatch.done {
			m.catalogBatch.loading = true
			return m, fetchCatalogBatchCmd(loader, m.catalogBatch.offset, catalogBatchSize)
		}
		return m, nil

	case tracksLoadedMsg:
		wasPlaying := m.player.IsPlaying()
		if !wasPlaying {
			m.player.Stop()
			m.player.ClearPreload()
		}
		m.resetYTDLBatch()
		m.playlist.Replace(msg)
		m.plCursor = 0
		m.plScroll = 0
		m.focus = focusPlaylist
		m.provLoading = false
		if m.playlist.Len() > 0 && !wasPlaying {
			cmd := m.playCurrentTrack()
			m.notifyAll()
			return m, cmd
		}
		return m, nil

	case navArtistsLoadedMsg:
		m.navBrowser.artists = []provider.ArtistInfo(msg)
		m.navBrowser.loading = false
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		return m, nil

	case navAlbumsLoadedMsg:
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
		m.navBrowser.albumLoading = false
		if msg.offset == 0 {
			m.navBrowser.cursor = 0
			m.navBrowser.scroll = 0
		}
		// If we just loaded the first page and it was a full menu → list transition,
		// also clear the general loading flag.
		m.navBrowser.loading = false
		return m, nil

	case navTracksLoadedMsg:
		m.navBrowser.tracks = []playlist.Track(msg)
		m.navBrowser.loading = false
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		m.navBrowser.screen = navBrowseScreenTracks
		return m, nil

	case catalogBatchMsg:
		m.catalogBatch.loading = false
		if msg.err != nil {
			m.catalogBatch.done = true
			m.status.Show("Catalog load failed", statusTTLDefault)
			return m, nil
		}
		if msg.added == 0 {
			m.catalogBatch.done = true
			return m, nil
		}
		if lists, err := m.provider.Playlists(); err == nil {
			m.providerLists = lists
		}
		m.catalogBatch.offset += msg.added
		if msg.added < catalogBatchSize {
			m.catalogBatch.done = true
		}
		return m, nil

	case catalogSearchMsg:
		m.provLoading = false
		if msg.err != nil {
			m.status.Show("Search failed", statusTTLDefault)
		} else {
			if lists, err := m.provider.Playlists(); err == nil {
				m.providerLists = lists
			}
			m.provCursor = 0
			if msg.count == 0 {
				m.status.Show("No stations found", statusTTLDefault)
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
			m.status.Showf(statusTTLBatch, "Radio batch load failed: %v", msg.err)
			return m, nil
		}
		if len(msg.tracks) == 0 {
			m.ytdlBatch.done = true
			return m, nil
		}
		m.playlist.Add(msg.tracks...)
		m.ytdlBatch.offset += len(msg.tracks)
		if len(msg.tracks) < ytdlBatchSize {
			m.ytdlBatch.done = true
			return m, nil
		}
		// Immediately fetch the next batch.
		m.ytdlBatch.loading = true
		return m, fetchYTDLBatchCmd(m.ytdlBatch.gen, m.ytdlBatch.url, m.ytdlBatch.offset, ytdlBatchSize)

	case feedsLoadedMsg:
		m.feedLoading = false
		if len(msg.tracks) > 0 {
			m.playlist.Add(msg.tracks...)
			m.status.Showf(statusTTLDefault, "Loaded %d track(s)", len(msg.tracks))
		} else {
			m.status.Show("No tracks found at URL.", statusTTLDefault)
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

	case netSearchLoadedMsg:
		if len(msg) == 0 {
			m.status.Show("No tracks found online.", statusTTLDefault)
			return m, nil
		}
		startIdx := m.playlist.Len()
		m.playlist.Add(msg...)
		for i := startIdx; i < m.playlist.Len(); i++ {
			m.playlist.Queue(i)
		}
		m.status.Showf(statusTTLDefault, "Added to Queue: %s", msg[0].DisplayName())
		if !m.player.IsPlaying() {
			cmd := m.playCurrentTrack()
			m.notifyAll()
			return m, cmd
		}
		return m, nil

	case lyricsLoadedMsg:
		m.lyrics.loading = false
		m.lyrics.err = msg.err
		m.lyrics.scroll = 0
		if msg.err == nil {
			m.lyrics.lines = msg.lines
		}
		return m, nil

	case fbTracksResolvedMsg:
		if len(msg.tracks) == 0 {
			m.status.Show("No audio files found", statusTTLDefault)
			return m, nil
		}
		if msg.replace {
			m.player.Stop()
			m.player.ClearPreload()
			m.resetYTDLBatch()
			m.playlist.Replace(msg.tracks)
			m.plCursor = 0
			m.plScroll = 0
		} else {
			m.playlist.Add(msg.tracks...)
		}
		m.focus = focusPlaylist
		m.status.Showf(statusTTLDefault, "Added %d track(s)", len(msg.tracks))
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
		m.buffering = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			m.reconnect.attempts = 0
			m.reconnect.at = time.Time{}
			m.applyResume()
		}
		m.notifyAll()
		return m, m.preloadNext()

	case streamPreloadedMsg:
		m.preloading = false
		return m, nil

	case ytdlSavedMsg:
		m.save.finishDownload()
		if msg.err != nil {
			m.status.Showf(statusTTLMedium, "Download failed: %s", msg.err)
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
		m.spotSearch.loading = false
		if msg.err != nil {
			m.spotSearch.err = msg.err.Error()
			return m, nil
		}
		m.spotSearch.results = msg.tracks
		m.spotSearch.cursor = 0
		m.spotSearch.screen = spotSearchResults
		if len(msg.tracks) == 0 {
			m.spotSearch.err = "No results found"
		}
		return m, nil

	case spotPlaylistsMsg:
		m.spotSearch.loading = false
		if msg.err != nil {
			m.spotSearch.err = msg.err.Error()
			return m, nil
		}
		m.spotSearch.playlists = msg.playlists
		m.spotSearch.cursor = 0
		m.spotSearch.screen = spotSearchPlaylist
		return m, nil

	case spotAddedMsg:
		m.spotSearch.loading = false
		if msg.err != nil {
			m.spotSearch.err = "Add failed: " + msg.err.Error()
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Added to %q", msg.name)
		m.spotSearch.visible = false
		return m, nil

	case spotCreatedMsg:
		m.spotSearch.loading = false
		if msg.err != nil {
			m.spotSearch.err = "Create failed: " + msg.err.Error()
			return m, nil
		}
		m.status.Showf(statusTTLDefault, "Created %q & added track", msg.name)
		m.spotSearch.visible = false
		return m, nil

	case provAuthDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			m.provLoading = false
			m.provSignIn = false
			return m, nil
		}
		m.provSignIn = false
		m.provLoading = true
		return m, fetchPlaylistsCmd(m.provider)

	case devicesListedMsg:
		m.devicePicker.loading = false
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "Device list failed: %s", msg.err)
			m.devicePicker.visible = false
		} else {
			m.devicePicker.devices = msg.devices
		}
		return m, nil

	case deviceSwitchedMsg:
		if msg.err != nil {
			m.status.Showf(statusTTLDefault, "Switch failed: %s", msg.err)
		} else {
			m.status.Showf(statusTTLDefault, "Audio output: %s", msg.name)
			// Persist the selection to config.
			_ = config.Save("audio_device", fmt.Sprintf("%q", msg.name))
		}
		return m, nil

	case mpris.InitMsg:
		m.mpris = msg.Svc
		m.notifyAll()
		return m, nil

	case mpris.PlayPauseMsg:
		cmd := m.togglePlayPause()
		m.notifyAll()
		return m, cmd

	case mpris.NextMsg:
		m.scrobbleCurrent()
		cmd := m.nextTrack()
		m.notifyAll()
		return m, cmd

	case mpris.PrevMsg:
		m.scrobbleCurrent()
		cmd := m.prevTrack()
		m.notifyAll()
		return m, cmd

	case mpris.SeekMsg:
		offset := time.Duration(msg.Offset) * time.Microsecond
		m.player.Seek(offset)
		m.notifyAll()
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return m, nil

	case mpris.SetPositionMsg:
		pos := time.Duration(msg.Position) * time.Microsecond
		m.player.Seek(pos - m.player.Position())
		m.notifyAll()
		if m.mpris != nil {
			m.mpris.EmitSeeked(m.player.Position().Microseconds())
		}
		return m, nil

	case mpris.SetVolumeMsg:
		m.player.SetVolume(mpris.LinearToDb(msg.Volume))
		m.notifyAll()
		return m, nil

	case mpris.StopMsg:
		m.player.Stop()
		m.notifyAll()
		return m, nil

	case mpris.QuitMsg:
		m.flushPendingSpeedSave()
		m.player.Close()
		m.quitting = true
		return m, tea.Quit

	case SetEQPresetMsg:
		m.SetEQPreset(msg.Name, msg.Bands)
		return m, nil
	}

	return m, nil
}
