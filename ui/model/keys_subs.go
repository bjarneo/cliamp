package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/fuzzy"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// subscriptionProvider returns the first provider that lists subscriptions
// locally, preferring the active one.
func (m Model) subscriptionProvider() provider.SubscriptionLister {
	if sl, ok := m.provider.(provider.SubscriptionLister); ok {
		return sl
	}
	for _, pe := range m.providers {
		if pe.Provider == nil {
			continue
		}
		if sl, ok := pe.Provider.(provider.SubscriptionLister); ok {
			return sl
		}
	}
	return nil
}

// hasSubscriptions reports whether any provider has shows to list.
func (m Model) hasSubscriptions() bool {
	sl := m.subscriptionProvider()
	return sl != nil && len(sl.Subscriptions()) > 0
}

// openSubsOverlay loads the subscription list and shows the overlay.
func (m *Model) openSubsOverlay() {
	sl := m.subscriptionProvider()
	if sl == nil {
		m.status.Warning("No provider keeps subscriptions.", statusTTLDefault)
		return
	}
	shows := sl.Subscriptions()
	if len(shows) == 0 {
		m.status.Warning("No subscribed shows. Press f on a show to subscribe.", statusTTLDefault)
		return
	}
	m.subs = subsOverlay{visible: true, shows: shows}
}

// subsVisibleShows returns the show indices the filter admits, in display order.
func (m Model) subsVisibleShows() []int {
	if m.subs.filter == "" {
		idx := make([]int, len(m.subs.shows))
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	return m.subs.filtered
}

// updateSubsFilter rebuilds the filtered index set from the current query.
func (m *Model) updateSubsFilter() {
	m.subs.filtered = nil
	if m.subs.filter == "" {
		m.subs.cursor, m.subs.scroll = 0, 0
		return
	}
	for i, s := range m.subs.shows {
		if _, ok := fuzzy.Match(m.subs.filter, s.Name); ok {
			m.subs.filtered = append(m.subs.filtered, i)
			continue
		}
		if s.Author != "" {
			if _, ok := fuzzy.Match(m.subs.filter, s.Author); ok {
				m.subs.filtered = append(m.subs.filtered, i)
			}
		}
	}
	m.subs.cursor, m.subs.scroll = 0, 0
}

// selectedSubscription returns the highlighted show.
func (m Model) selectedSubscription() (provider.SubscriptionInfo, bool) {
	visible := m.subsVisibleShows()
	if m.subs.cursor < 0 || m.subs.cursor >= len(visible) {
		return provider.SubscriptionInfo{}, false
	}
	return m.subs.shows[visible[m.subs.cursor]], true
}

func (m *Model) subsMoveCursor(delta int) {
	count := len(m.subsVisibleShows())
	if count == 0 {
		return
	}
	m.subs.cursor = (m.subs.cursor + delta + count) % count
	m.subsMaybeAdjustScroll()
}

func (m *Model) subsMaybeAdjustScroll() {
	clampScroll(&m.subs.cursor, &m.subs.scroll, len(m.subsVisibleShows()), m.effectivePlaylistVisible())
}

// handleSubsKey processes key presses while the subscriptions overlay is open.
func (m *Model) handleSubsKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.subs.filtering {
		return m.handleSubsFilterKey(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		m.subs.visible = false
		return m.quit()
	case "ctrl+k", "?":
		m.openKeymap()
	case "ctrl+x":
		m.toggleExpandedView()
		m.subsMaybeAdjustScroll()
	case "up", "k":
		m.subsMoveCursor(-1)
	case "down", "j":
		m.subsMoveCursor(1)
	case "/":
		m.subs.filtering = true
		m.subs.filter = ""
		m.updateSubsFilter()
	case "enter":
		return m.loadSubscription(subsLoadPlay)
	case "a":
		return m.loadSubscription(subsLoadAppend)
	case "q":
		return m.loadSubscription(subsLoadQueue)
	case "l":
		return m.loadSubscription(subsLoadLatest)
	case "L":
		return m.loadLatestFromAllSubscriptions()
	case "esc", "F":
		m.subs.visible = false
	}
	return nil
}

// handleSubsFilterKey edits the filter query.
func (m *Model) handleSubsFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.subs.filtering = false
		m.subs.filter = ""
		m.updateSubsFilter()
	case tea.KeyEnter:
		m.subs.filtering = false
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("subs-filter", &m.subs.filter, " ")
			m.updateSubsFilter()
		} else if m.editText("subs-filter", &m.subs.filter, msg) {
			m.updateSubsFilter()
		}
	}
	return nil
}

// addSubscriptionEpisodes appends episodes to the playlist and applies the
// queueing the mode asks for. It returns the playlist index the batch starts
// at, or -1 when there was nothing to add.
func (m *Model) addSubscriptionEpisodes(tracks []playlist.Track, mode subsLoadMode, showName string) int {
	if len(tracks) == 0 {
		m.subs.err = "No playable episodes in " + showName
		return -1
	}
	start := m.playlist.Len()
	m.playlist.Add(tracks...)
	m.loadedPlaylist = ""
	m.addToHeaderState(tracks)

	switch mode {
	case subsLoadQueue:
		for i := range tracks {
			m.playlist.Queue(start + i)
		}
		m.status.Showf(statusTTLDefault, "Queued %d episode(s) from %s", len(tracks), showName)
	case subsLoadLatest:
		m.playlist.Queue(start)
		m.status.Showf(statusTTLDefault, "Queued %s", tracks[0].DisplayName())
	default:
		m.status.Showf(statusTTLDefault, "Added %d episode(s) from %s", len(tracks), showName)
	}
	return start
}

// appendSubscriptionTracks adds fetched episodes to the playlist and returns
// the command the caller must run.
func (m *Model) appendSubscriptionTracks(tracks []playlist.Track, mode subsLoadMode, showName string) tea.Cmd {
	start := m.addSubscriptionEpisodes(tracks, mode, showName)
	if start < 0 {
		return nil
	}
	if mode == subsLoadPlay {
		m.subs.visible = false
		m.playlist.SetIndex(start)
		m.plCursor = start
		m.adjustScroll()
		cmd := m.playCurrentTrack()
		m.notifyPlayback()
		return cmd
	}
	m.normalizeQueueOverlay()
	return m.rearmPreload()
}

// selectedProviderShow returns the ID and name of the show highlighted in the
// provider list, and false when the row is a section entry, a browse entry, or
// a provider that has no shows.
func (m Model) selectedProviderShow() (id, name string, ok bool) {
	if m.provLoading || m.provCursor < 0 || m.provCursor >= len(m.providerLists) {
		return "", "", false
	}
	if m.selectedProviderListIsBrowseEntry() {
		return "", "", false
	}
	entry := m.providerLists[m.provCursor]
	if sl, ok := m.provider.(provider.SectionedList); ok && !sl.IsFavoritableID(entry.ID) {
		return "", "", false
	}
	if _, ok := m.provider.(provider.AlbumTrackLoader); !ok {
		return "", "", false
	}
	return entry.ID, entry.Name, true
}

// loadLatestFromProviderList queues the newest episode of the show highlighted
// in the provider list.
func (m *Model) loadLatestFromProviderList() tea.Cmd {
	id, name, ok := m.selectedProviderShow()
	if !ok {
		return nil
	}
	return m.loadShowEpisodes(id, name, subsLoadLatest)
}
