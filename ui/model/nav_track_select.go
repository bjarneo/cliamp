package model

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// navTrackListFileStyle reports whether the nav track list should behave like
// the local file browser (Space to select, Enter to add). ShowLister providers
// such as Podcasts use this mode.
func (m Model) navTrackListFileStyle() bool {
	if m.navBrowser.screen != navBrowseScreenTracks {
		return false
	}
	_, ok := m.navBrowser.prov.(provider.ShowLister)
	return ok
}

func navTrackSelectKey(t playlist.Track) string {
	if t.Path != "" {
		return t.Path
	}
	return t.Title
}

func (m *Model) navClearTrackSelection() {
	if m.navBrowser.selected == nil {
		m.navBrowser.selected = make(map[string]bool)
		return
	}
	clear(m.navBrowser.selected)
}

// navCloseAfterTrackPick closes the overlay after the user confirms a track
// pick, like fbConfirm for the file browser.
func (m *Model) navCloseAfterTrackPick() {
	m.navClearTrackSelection()
	m.navBrowser.visible = false
	m.navBrowser.directTrackJump = false
}

func (m Model) navTrackAtCursor() (playlist.Track, bool) {
	idx := m.selectedNavRawIndex(len(m.navBrowser.tracks))
	if idx < 0 || idx >= len(m.navBrowser.tracks) {
		return playlist.Track{}, false
	}
	return m.navBrowser.tracks[idx], true
}

func (m Model) navSelectedTracksInOrder() []playlist.Track {
	if len(m.navBrowser.selected) == 0 {
		return nil
	}
	out := make([]playlist.Track, 0, len(m.navBrowser.selected))
	for _, t := range m.navBrowser.tracks {
		if m.navBrowser.selected[navTrackSelectKey(t)] {
			out = append(out, t)
		}
	}
	if m.resumeSaver != nil {
		out = playlist.WithPlaybackContext(out)
	}
	return out
}

// navConfirmSelectedTracks appends or replaces the queue with the current nav
// track selection, focuses the main playlist view, and starts playback when
// nothing is playing.
func (m *Model) navConfirmSelectedTracks(replace, toPlaylist bool) tea.Cmd {
	tracks := m.navSelectedTracksInOrder()
	if len(tracks) == 0 {
		return nil
	}
	m.navCloseAfterTrackPick()
	if toPlaylist {
		m.openPlaylistPicker(tracks, fmt.Sprintf("%d tracks selected", len(tracks)))
		return nil
	}
	var cmd tea.Cmd
	if replace {
		m.stopPlayback()
		m.player.ClearPreload()
		m.retireTracksPaging()
		m.replacePlaylist(tracks)
		m.clearLoadedPlaylist()
		m.setHeaderStateFromTracks(tracks)
		m.plCursor = 0
		m.plScroll = 0
		m.playlist.SetIndex(0)
		m.status.Successf(statusTTLDefault, "Replaced queue with %d track(s)", len(tracks))
		cmd = m.playCurrentTrack()
	} else {
		startIdx := m.appendTracks(tracks...)
		m.playlist.SetIndex(startIdx)
		m.plCursor = startIdx
		m.adjustScroll()
		m.status.Successf(statusTTLDefault, "Added %d track(s)", len(tracks))
		if !m.player.IsPlaying() {
			cmd = m.playCurrentTrack()
		} else {
			cmd = m.rearmStalePreload()
		}
	}
	m.focus = focusPlaylist
	m.applyHeightMode()
	m.adjustScroll()
	return cmd
}

func (m *Model) handleNavTrackListFileStyleKey(msg tea.KeyPressMsg) tea.Cmd {
	listLen := len(m.navBrowser.tracks)
	if m.navBrowser.search != "" {
		listLen = len(m.navBrowser.searchIdx)
	}

	switch msg.String() {
	case "ctrl+h":
		m.toggleAlbumHeadersManual()
		m.navMaybeAdjustScroll()
		return nil
	case "up", "k":
		if m.navBrowser.cursor > 0 {
			m.navBrowser.cursor--
		} else if listLen > 0 {
			m.navBrowser.cursor = listLen - 1
		}
		m.navMaybeAdjustScroll()
	case "down", "j":
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
		} else if listLen > 0 {
			m.navBrowser.cursor = 0
		}
		m.navMaybeAdjustScroll()
	case "space":
		t, ok := m.navTrackAtCursor()
		if !ok {
			return nil
		}
		if m.navBrowser.selected == nil {
			m.navBrowser.selected = make(map[string]bool)
		}
		key := navTrackSelectKey(t)
		if m.navBrowser.selected[key] {
			delete(m.navBrowser.selected, key)
		} else {
			m.navBrowser.selected[key] = true
		}
		if m.navBrowser.cursor < listLen-1 {
			m.navBrowser.cursor++
		}
		m.navMaybeAdjustScroll()
	case "a":
		var selectAll bool
		for _, t := range m.navBrowser.tracks {
			key := navTrackSelectKey(t)
			if selectAll || !m.navBrowser.selected[key] {
				selectAll = true
				m.navBrowser.selected[key] = true
			}
		}
		if !selectAll {
			for _, t := range m.navBrowser.tracks {
				delete(m.navBrowser.selected, navTrackSelectKey(t))
			}
		}
	case "enter":
		if len(m.navBrowser.selected) == 0 {
			t, ok := m.navTrackAtCursor()
			if !ok {
				return nil
			}
			m.navBrowser.selected[navTrackSelectKey(t)] = true
		}
		return m.navConfirmSelectedTracks(false, false)
	case "R":
		if len(m.navBrowser.selected) == 0 {
			m.status.Warning("Select tracks first (Space).", statusTTLDefault)
			return nil
		}
		if m.playlist.Len() > 0 {
			m.navBrowser.confirmReplace = true
			return nil
		}
		return m.navConfirmSelectedTracks(true, false)
	case "w":
		if len(m.navBrowser.selected) == 0 {
			t, ok := m.navTrackAtCursor()
			if !ok {
				return nil
			}
			m.navBrowser.selected[navTrackSelectKey(t)] = true
		}
		return m.navConfirmSelectedTracks(false, true)
	case "q":
		if listLen == 0 {
			return nil
		}
		t, ok := m.navTrackAtCursor()
		if !ok {
			return nil
		}
		tracks := m.navPlaybackTracks()
		// navPlaybackTracks respects the filter; match cursor track by path.
		for _, track := range tracks {
			if track.Path == t.Path && track.Title == t.Title {
				t = track
				break
			}
		}
		newIdx := m.appendTracks(t)
		m.playlist.Queue(newIdx)
		m.normalizeQueueOverlay()
		m.status.Showf(statusTTLMedium, "Queued: %s", t.DisplayName())
		if !m.player.IsPlaying() {
			return m.nextTrack()
		}
		return m.rearmStalePreload()
	case "f":
		if idx := m.selectedNavRawIndex(len(m.navBrowser.tracks)); idx >= 0 {
			return m.favoriteTrackKey(m.navBrowser.tracks[idx])
		}
	case "esc", "h", "left", "backspace":
		m.navClearSearch()
		m.navClearTrackSelection()
		if m.navBrowser.directTrackJump {
			m.cancelNavRequests()
			m.navBrowser.directTrackJump = false
			if m.navBrowser.fromProvList {
				m.navBackFromRoot()
			} else {
				m.navBrowser.visible = false
			}
			return nil
		}
		m.navBrowser.cursor = 0
		m.navBrowser.scroll = 0
		switch m.navBrowser.mode {
		case navBrowseModeByAlbum:
			m.navBrowser.screen = navBrowseScreenList
		case navBrowseModeByArtist:
			m.navBrowser.screen = navBrowseScreenList
		case navBrowseModeByArtistAlbum:
			m.navBrowser.screen = navBrowseScreenAlbums
		case navBrowseModeByGenre:
			m.navBrowser.screen = navBrowseScreenAlbums
		}
	}
	return nil
}
