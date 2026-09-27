package model

// immersive_keys.go owns every keypress while immersive mode is active. The
// handler is reached before every other screen handler, so bindings here can
// reuse familiar letters without touching normal-mode dispatch. Playback verbs
// (space, </>, z, r, volume) forward to the same helpers the main screen uses.

import (
	"context"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// handleImmersiveKey processes a key press inside immersive mode.
func (m *Model) handleImmersiveKey(msg tea.KeyPressMsg) tea.Cmd {
	// The full-screen visualizer still layers on top; delegate while it is up.
	if m.fullVis {
		return m.handleFullVisualizerKey(msg)
	}
	if m.immersive.searching || m.immersive.railFiltering {
		return m.handleImmersiveInputKey(msg)
	}

	switch msg.String() {
	case "I":
		m.exitImmersive()
		return nil
	case "esc":
		return m.immersiveEscape()
	case "backspace":
		m.immersiveGoBack()
		return nil
	case "tab":
		m.immersive.focus = immersivePane((int(m.immersive.focus) + 1) % int(immPaneCount))
		return nil
	case "shift+tab":
		m.immersive.focus = immersivePane((int(m.immersive.focus) + int(immPaneCount) - 1) % int(immPaneCount))
		return nil
	case "home":
		m.immersive.focus = immPaneCenter
		m.immersiveGoBack()
		m.immersive.back = nil
		return nil

	// — playback bar —
	case "space":
		cmd := m.togglePlayPause()
		m.notifyPlayback()
		return cmd
	case ">", ".":
		refresh := m.scrobbleCurrent()
		cmd := m.nextTrack()
		m.notifyPlayback()
		return tea.Batch(refresh, cmd)
	case "<", ",":
		refresh := m.scrobbleCurrent()
		cmd := m.prevTrack()
		m.notifyPlayback()
		return tea.Batch(refresh, cmd)
	case "z":
		m.playlist.ToggleShuffle()
		m.saveConfigKey("shuffle", boolString(m.playlist.Shuffled()))
		return m.rearmPreload()
	case "r":
		const repeatModes = playlist.RepeatOne + 1
		m.playlist.SetRepeat((m.playlist.Repeat() + 1) % repeatModes)
		m.saveConfigKey("repeat", repeatLabel(m.playlist.Repeat()))
		return nil
	case "+", "=":
		m.player.SetVolume(m.player.Volume() + 1)
		return nil
	case "-":
		m.player.SetVolume(m.player.Volume() - 1)
		return nil
	case "shift+left":
		return m.doSeek(-m.seekStepLarge)
	case "shift+right":
		return m.doSeek(m.seekStepLarge)

	// — frame —
	case "c":
		m.immersive.railCollapsed = !m.immersive.railCollapsed
		return nil
	case "q":
		m.immersive.rightTab = immersiveRightTab((int(m.immersive.rightTab) + 1) % int(immTabCount))
		if m.immersive.rightTab == immTabNowPlaying {
			return m.maybeFetchNowPlayingArtist()
		}
		return nil
	case "g":
		m.immersive.focus = immPaneCenter
		m.immersiveGoBack()
		m.immersive.back = nil
		m.immersive.view = immViewHome
		m.immersive.zone = zoneRolo
		return nil
	case "V":
		m.fullVis = true
		return nil

	// — rail pills / ordering —
	case "1":
		m.immersive.railSection = immSectionPlaylists
		m.immersive.railCursor, m.immersive.railScroll = 0, 0
		return nil
	case "2":
		m.immersive.railSection = immSectionAlbums
		m.immersive.railCursor, m.immersive.railScroll = 0, 0
		return nil
	case "3":
		m.immersive.railSection = immSectionArtists
		m.immersive.railCursor, m.immersive.railScroll = 0, 0
		return nil
	case "s":
		m.immersive.railSort = immRailSort((int(m.immersive.railSort) + 1) % int(immRailSortCount))
		return nil
	case "f":
		m.immersive.railFiltering = true
		m.immersive.railFilter = ""
		return nil

	// — search —
	case "/", "ctrl+f":
		if _, ok := m.immersive.prov.(provider.Searcher); ok {
			m.immersive.searching = true
			m.immersive.searchQuery = ""
		}
		return nil

	// — center modes —
	case "o":
		m.immersive.roloMode = !m.immersive.roloMode
		m.immersive.roloSpin = 0
		if m.immersive.roloMode {
			if m.immersive.zone == zoneTable {
				m.immersive.roloCursor = m.immersive.trackCursor
			}
			m.immersive.zone = zoneRolo
			m.immersive.focus = immPaneCenter
		} else if m.immersive.view == immViewHome {
			m.immersive.zone = zoneGrid
		} else {
			m.immersive.zone = zoneTable
		}
		return nil
	case "t":
		if m.immersiveTableActive() {
			m.immersive.trackSort = immersiveTrackSort((int(m.immersive.trackSort) + 1) % int(immSortTrackCount))
			m.immersive.trackScroll = 0
		}
		return nil
	case "p":
		return m.playImmersiveContext(m.immersive.trackCursor)
	case "n":
		return m.immersiveToggleLike()
	case "a":
		return m.immersiveQueueAppend()

	case "enter":
		return m.immersiveActivate()

	// — navigation —
	case "j", "down":
		m.immersiveMove(1, 0)
		return nil
	case "k", "up":
		m.immersiveMove(-1, 0)
		return nil
	case "h", "left":
		m.immersiveMove(0, -1)
		return nil
	case "l", "right":
		m.immersiveMove(0, 1)
		return nil
	case "pgdown":
		m.immersiveMove(m.immersivePageStep(), 0)
		return nil
	case "pgup":
		m.immersiveMove(-m.immersivePageStep(), 0)
		return nil
	case "shift+home":
		m.immersiveCursorHome()
		return nil
	}
	return nil
}

// immersiveEscape peels the innermost state: a running filter, the big
// rolodex, one back-stack level, then the mode itself.
func (m *Model) immersiveEscape() tea.Cmd {
	switch {
	case m.immersive.roloMode:
		m.immersive.roloMode = false
		m.immersive.roloSpin = 0
		if m.immersive.view != immViewHome {
			m.immersive.zone = zoneTable
		}
	case len(m.immersive.back) > 0 || m.immersive.view != immViewHome:
		m.immersiveGoBack()
	default:
		m.exitImmersive()
	}
	return nil
}

// immersiveTableActive reports whether the center pane is on a track table.
func (m Model) immersiveTableActive() bool {
	switch m.immersive.view {
	case immViewPlaylist, immViewAlbum, immViewSearch, immViewArtist:
		return !m.immersive.roloMode
	}
	return false
}

// immersiveMove applies a (vertical, horizontal) step to the focused pane.
func (m *Model) immersiveMove(dy, dx int) {
	switch m.immersive.focus {
	case immPaneRail:
		rows := m.railRows()
		m.immersive.railCursor = clampInt(m.immersive.railCursor+dy, 0, max(0, len(rows)-1))
		m.immersive.railScroll = clampedScroll(m.immersive.railScroll, m.immersive.railCursor, len(rows), m.immersiveRailBudget())
	case immPaneRight:
		total := m.playlist.QueueLen()
		m.immersive.rightCursor = clampInt(m.immersive.rightCursor+dy, 0, max(0, total-1))
		m.immersive.rightScroll = clampedScroll(m.immersive.rightScroll, m.immersive.rightCursor, total, m.immersiveRightBudget())
	default:
		m.immersiveMoveCenter(dy, dx)
	}
}

// immersiveMoveCenter routes a step inside the center pane: the rolodex eats
// horizontal moves, the grid and table eat vertical ones.
func (m *Model) immersiveMoveCenter(dy, dx int) {
	if m.immersive.roloMode {
		if dx != 0 {
			m.roloSpinKick(dx)
		}
		if dy != 0 {
			m.roloSpinKick(dy) // j/k also roll while the wheel owns the pane
		}
		return
	}
	switch m.immersive.zone {
	case zoneRolo:
		if dx != 0 {
			m.roloSpinKick(dx)
			return
		}
		if dy > 0 && m.immersive.view == immViewHome {
			m.immersive.zone = zoneGrid
			return
		}
	case zoneGrid:
		m.immersiveMoveGrid(dy, dx)
	case zoneTable:
		n := len(m.sortedTracks())
		m.immersive.trackCursor = clampInt(m.immersive.trackCursor+dy, 0, max(0, n-1))
		m.immersive.trackScroll = clampedScroll(m.immersive.trackScroll, m.immersive.trackCursor, n, m.immersiveTableBudget())
		if dx < 0 && m.immersive.view == immViewHome {
			m.immersive.zone = zoneGrid // columns to the left hop back to cards
		}
	}
}

// immersiveMoveGrid moves the card-grid cursor in 2D. Column count comes from
// the current center width so keys and rendering share the same math.
func (m *Model) immersiveMoveGrid(dy, dx int) {
	cols := m.immersiveGridCols()
	items := len(m.roloItems())
	if items == 0 {
		return
	}
	cur := m.immersive.gridCursor
	row, col := cur/cols, cur%cols
	rows := (items + cols - 1) / cols
	switch {
	case dy < 0 && row == 0:
		m.immersive.zone = zoneRolo
		return
	case dy < 0:
		row--
	case dy > 0:
		row++
	case dx < 0:
		col--
	case dx > 0:
		col++
	}
	row = clampInt(row, 0, max(0, rows-1))
	col = clampInt(col, 0, cols-1)
	m.immersive.gridCursor = min(row*cols+col, items-1)
}

// immersiveActivate is Enter: rail rows and cards open their context, table
// rows play in place, queue rows jump the live queue.
func (m *Model) immersiveActivate() tea.Cmd {
	switch m.immersive.focus {
	case immPaneRail:
		rows := m.railRows()
		if m.immersive.railCursor < len(rows) {
			m.immersive.focus = immPaneCenter
			return m.openImmersiveRow(rows[m.immersive.railCursor])
		}
	case immPaneRight:
		if m.immersive.rightTab == immTabQueue {
			return m.immersiveQueueJump()
		}
	default:
		if m.immersive.roloMode || m.immersive.zone == zoneRolo {
			items := m.roloItems()
			if m.immersive.roloCursor < len(items) {
				return m.activateRoloItem(items[m.immersive.roloCursor])
			}
			return nil
		}
		if m.immersive.zone == zoneGrid {
			items := m.roloItems()
			if m.immersive.gridCursor < len(items) {
				return m.activateRoloItem(items[m.immersive.gridCursor])
			}
			return nil
		}
		return m.playImmersiveTrack(m.immersive.trackCursor)
	}
	return nil
}

// activateRoloItem opens a deck item: tracks play, collections open.
func (m *Model) activateRoloItem(item roloItem) tea.Cmd {
	if item.kind == roloKindTrack {
		idx := indexOfRoloTrack(m.sortedTracks(), item.id)
		if idx >= 0 {
			m.immersive.trackCursor = idx
			return m.playImmersiveTrack(idx)
		}
		return nil
	}
	return m.openImmersiveRow(immRailRow{kind: item.kind, id: item.id, name: item.title, sub: item.sub})
}

// indexOfRoloTrack resolves a roloItem track id (its position in the track
// list) back to a table index.
func indexOfRoloTrack(tracks []playlist.Track, id string) int {
	for i := range tracks {
		if strconv.Itoa(i) == id {
			return i
		}
	}
	return -1
}

// immersiveQueueJump plays the queue row under the right-rail cursor.
func (m *Model) immersiveQueueJump() tea.Cmd {
	entries := m.playlist.QueueEntries()
	if m.immersive.rightCursor < 0 || m.immersive.rightCursor >= len(entries) {
		return nil
	}
	idx := entries[m.immersive.rightCursor].TrackIndex
	if idx < 0 || idx >= m.playlist.Len() {
		return nil
	}
	m.plCursor = idx
	m.playlist.SetIndex(idx)
	m.playlist.Dequeue(idx)
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}

// immersiveQueueAppend adds the focused table/rolodex track to the live queue.
func (m *Model) immersiveQueueAppend() tea.Cmd {
	if !m.immersiveTableActive() && !m.immersive.roloMode {
		return nil
	}
	var track playlist.Track
	if m.immersive.roloMode {
		items := m.roloItems()
		if m.immersive.roloCursor >= len(items) || items[m.immersive.roloCursor].kind != roloKindTrack {
			return nil
		}
		idx := indexOfRoloTrack(m.sortedTracks(), items[m.immersive.roloCursor].id)
		if idx < 0 {
			return nil
		}
		track = m.sortedTracks()[idx]
	} else {
		tracks := m.sortedTracks()
		if m.immersive.trackCursor >= len(tracks) {
			return nil
		}
		track = tracks[m.immersive.trackCursor]
	}
	if track.Path == "" {
		return nil
	}
	m.playlist.Add(track)
	m.playlist.Queue(m.playlist.Len() - 1)
	m.status.Showf(statusTTLShort, "Queued %s", trackViewName(track))
	return m.rearmPreload()
}

// immersiveToggleLike hearts the focused track (table/rolo) or, lacking a
// track context, the playing track — mirroring `n` on the playlist pane.
func (m *Model) immersiveToggleLike() tea.Cmd {
	if m.favMgr == nil {
		return nil
	}
	var track playlist.Track
	var ok bool
	if m.immersiveTableActive() {
		tracks := m.sortedTracks()
		if m.immersive.trackCursor < len(tracks) {
			track, ok = tracks[m.immersive.trackCursor], true
		}
	} else {
		track, _ = m.currentPlaybackTrack()
		ok = track.Path != ""
	}
	if !ok {
		return nil
	}
	added, err := m.favMgr.ToggleFavorite(track)
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Favorite failed: %s", err)
		return nil
	}
	m.refreshFavSet()
	if added {
		m.status.Show("Added to favorites "+favAddedMark(), statusTTLShort)
	} else {
		m.status.Show("Removed from favorites "+favRemovedMark(), statusTTLShort)
	}
	return m.fetchProviderPlaylists()
}

// handleImmersiveInputKey drives the two text fields (top-bar search and the
// rail filter). Both are simple line editors.
func (m *Model) handleImmersiveInputKey(msg tea.KeyPressMsg) tea.Cmd {
	field := &m.immersive.searchQuery
	if m.immersive.railFiltering {
		field = &m.immersive.railFilter
	}
	switch msg.String() {
	case "esc":
		m.immersive.searching = false
		m.immersive.railFiltering = false
		return nil
	case "enter":
		if m.immersive.searching {
			m.immersive.searching = false
			return m.runImmersiveSearch()
		}
		m.immersive.railFiltering = false
		m.immersive.railCursor, m.immersive.railScroll = 0, 0
		return nil
	case "backspace":
		if *field != "" {
			*field = (*field)[:len(*field)-1]
		}
		return nil
	case "ctrl+u":
		*field = ""
		return nil
	}
	if msg.Text != "" {
		*field += msg.Text
	}
	return nil
}

// runImmersiveSearch fires the provider's track search into the center table.
func (m *Model) runImmersiveSearch() tea.Cmd {
	s, ok := m.immersive.prov.(provider.Searcher)
	if !ok || m.immersive.searchQuery == "" {
		return nil
	}
	m.pushImmersiveBack()
	m.immersive.view = immViewSearch
	m.immersive.ctxID, m.immersive.ctxName = "", "Search: "+m.immersive.searchQuery
	m.immersive.ctxSub = "Results"
	m.immersive.ctxKind = roloKindTrack
	m.immersive.searchLoading = true
	m.immersive.tracks = nil
	m.immersive.tracksLoading = true
	m.immersive.trackCursor, m.immersive.trackScroll = 0, 0
	m.immersive.zone = zoneTable
	m.immersive.focus = immPaneCenter
	return fetchImmersiveSearchCmd(context.Background(), s, m.immersive.prov.Name(), m.immersive.searchQuery, nextRequest(&m.requests.immersiveSearch))
}

// immersiveCursorHome snaps the active cursor back to the first row.
func (m *Model) immersiveCursorHome() {
	switch m.immersive.focus {
	case immPaneRail:
		m.immersive.railCursor, m.immersive.railScroll = 0, 0
	case immPaneRight:
		m.immersive.rightCursor, m.immersive.rightScroll = 0, 0
	default:
		m.immersive.trackCursor, m.immersive.trackScroll = 0, 0
		m.immersive.roloCursor = 0
		m.immersive.gridCursor = 0
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// boolString persists a bool config value the same way keys.go does.
func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func repeatLabel(mode playlist.RepeatMode) string {
	return "\"" + mode.String() + "\""
}
