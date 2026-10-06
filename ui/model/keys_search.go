package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// openProviderSearch opens the active provider's native search overlay if it
// implements provider.Searcher; otherwise it falls back to the YouTube net
// search overlay.
func (m *Model) openProviderSearch() {
	m.openProviderSearchWith(m.provider)
}

// openProviderSearchWith opens a search overlay against the given provider.
// Falls back to YouTube net search when prov doesn't implement Searcher.
func (m *Model) openProviderSearchWith(prov playlist.Provider) {
	if _, ok := prov.(provider.Searcher); ok {
		m.cancelSearchOverlayRequest()
		nextRequest(&m.requests.searchOverlay)
		nextRequest(&m.requests.searchOverlayLists)
		nextRequest(&m.requests.searchOverlayMutation)
		m.searchOverlay = searchOverlayState{
			prov:    prov,
			visible: true,
			screen:  searchOverlayInput,
		}
		return
	}
	nextRequest(&m.requests.netSearch)
	m.netSearch = netSearchState{
		active: true,
		screen: netSearchInput,
		from:   providerName(prov),
	}
	m.prevFocus = m.focus
	m.focus = focusNetSearch
}

func (m *Model) provSearchMaybeAdjustScroll() {
	visible := m.providerScrollStep() - 1 // -1 for query line
	if visible < 1 {
		visible = 1
	}
	count := len(m.provSearch.results)
	clampScroll(&m.provSearch.cursor, &m.provSearch.scroll, count, visible)
}

// handleProvSearchKey processes key presses while filtering the provider playlist list.
// For the radio provider, Enter fires an API search; for others, Enter loads the
// selected result. Esc cancels and restores the normal catalog view.
func (m *Model) handleProvSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.provSearchMaybeAdjustScroll()
		return nil
	case "ctrl+n":
		if m.provSearch.cursor < len(m.provSearch.results)-1 {
			m.provSearch.cursor++
		} else if len(m.provSearch.results) > 0 {
			m.provSearch.cursor = 0
		}
		m.provSearchMaybeAdjustScroll()
		return nil
	case "ctrl+p":
		if m.provSearch.cursor > 0 {
			m.provSearch.cursor--
		} else if len(m.provSearch.results) > 0 {
			m.provSearch.cursor = len(m.provSearch.results) - 1
		}
		m.provSearchMaybeAdjustScroll()
		return nil
	case "ctrl+u":
		m.editText("provider-search", &m.provSearch.query, msg)
		m.updateProvSearch()
		return nil
	case "ctrl+d":
		step := m.providerScrollStep() - 1
		if step < 1 {
			step = 1
		}
		m.provSearch.cursor += step
		if m.provSearch.cursor >= len(m.provSearch.results) {
			m.provSearch.cursor = max(0, len(m.provSearch.results)-1)
		}
		m.provSearchMaybeAdjustScroll()
		return nil
	}

	// Catalog search: API-based search (no live client-side filtering).
	if cs, ok := m.provider.(provider.CatalogSearcher); ok {
		return m.handleCatalogSearchKey(msg, cs)
	}

	switch msg.Code {
	case tea.KeyEscape:
		m.provSearch.active = false
	case tea.KeyEnter:
		if len(m.provSearch.results) > 0 && !m.provPane.loading {
			idx := m.provSearch.results[m.provSearch.cursor]
			m.provPane.cursor = idx
			m.providerMaybeAdjustScroll()
			m.provSearch.active = false
			return m.openProviderList(idx)
		}
	case tea.KeyUp:
		if m.provSearch.cursor > 0 {
			m.provSearch.cursor--
		} else if len(m.provSearch.results) > 0 {
			m.provSearch.cursor = len(m.provSearch.results) - 1
		}
		m.provSearchMaybeAdjustScroll()

	case tea.KeyDown:
		if m.provSearch.cursor < len(m.provSearch.results)-1 {
			m.provSearch.cursor++
		} else if len(m.provSearch.results) > 0 {
			m.provSearch.cursor = 0
		}
		m.provSearchMaybeAdjustScroll()
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("provider-search", &m.provSearch.query, " ")
			m.updateProvSearch()
		} else if m.editText("provider-search", &m.provSearch.query, msg) {
			m.updateProvSearch()
		}
	}
	return nil
}

// handleCatalogSearchKey handles search input for providers with catalog search.
// Types a query, Enter fires API search, Esc cancels/clears.
func (m *Model) handleCatalogSearchKey(msg tea.KeyPressMsg, cs provider.CatalogSearcher) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.provSearch.active = false
		return m.restoreCatalog(cs)
	case tea.KeyEnter:
		m.provSearch.active = false
		if m.provSearch.query == "" {
			return m.restoreCatalog(cs)
		}
		m.provPane.loading = true
		m.provSearch.loading = true
		m.catalogBatch.loading = false
		nextRequest(&m.requests.provider)
		return fetchCatalogSearchCmd(cs, m.provider.Name(), m.provSearch.query, nextRequest(&m.requests.catalog))
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("provider-search", &m.provSearch.query, " ")
		} else {
			m.editText("provider-search", &m.provSearch.query, msg)
		}
	}
	return nil
}

// providerCatalogSearching reports whether the provider pane shows catalog
// search results, or waits for them. Esc then clears the search.
func (m Model) providerCatalogSearching() bool {
	cs, ok := m.provider.(provider.CatalogSearcher)
	return ok && (m.provSearch.loading || cs.IsSearching())
}

// restoreCatalog clears search results and restores the normal catalog view.
func (m *Model) restoreCatalog(cs provider.CatalogSearcher) tea.Cmd {
	nextRequest(&m.requests.catalog)
	// Clear before results arrive so providers can invalidate in-flight work.
	cs.ClearSearch()
	m.provSearch.loading = false
	m.provPane.loading = true
	m.catalogBatch.loading = false
	m.provPane.cursor = 0
	m.provPane.scroll = 0
	return m.fetchProviderPlaylists()
}

func (m *Model) updateProvSearch() {
	m.provSearch.results = nil
	m.provSearch.cursor = 0
	m.provSearch.scroll = 0
	if m.provSearch.query == "" {
		return
	}
	q := strings.ToLower(m.provSearch.query)
	for i, pl := range m.provPane.lists {
		if strings.Contains(strings.ToLower(pl.Name), q) {
			m.provSearch.results = append(m.provSearch.results, i)
		}
	}
}

func (m *Model) searchMaybeAdjustScroll(visible int) {
	clampScroll(&m.search.cursor, &m.search.scroll, len(m.search.results), visible)
}

func (m *Model) handleSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	// Ctrl combos do not conflict with text input, so they run first.
	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.searchMaybeAdjustScroll(m.effectivePlaylistVisible())
		return nil
	case "ctrl+n":
		if m.search.cursor < len(m.search.results)-1 {
			m.search.cursor++
		} else if len(m.search.results) > 0 {
			m.search.cursor = 0
		}
		m.searchMaybeAdjustScroll(m.effectivePlaylistVisible())
		return nil
	case "ctrl+p":
		if m.search.cursor > 0 {
			m.search.cursor--
		} else if len(m.search.results) > 0 {
			m.search.cursor = len(m.search.results) - 1
		}
		m.searchMaybeAdjustScroll(m.effectivePlaylistVisible())
		return nil
	case "ctrl+u":
		m.editText("playlist-search", &m.search.query, msg)
		m.updateSearch()
		return nil
	case "ctrl+d":
		step := m.effectivePlaylistVisible()
		if step < 1 {
			step = 1
		}
		m.search.cursor += step
		if m.search.cursor >= len(m.search.results) {
			m.search.cursor = max(0, len(m.search.results)-1)
		}
		m.searchMaybeAdjustScroll(m.effectivePlaylistVisible())
		return nil
	}

	switch msg.Code {
	case tea.KeyEscape:
		m.search.active = false
		m.focus = m.prevFocus
		m.closeSearchLayout()

	case tea.KeyEnter:
		var cmd tea.Cmd
		if len(m.search.results) > 0 {
			cmd = m.playIndex(m.search.results[m.search.cursor])
		}
		m.search.active = false
		m.focus = focusPlaylist
		m.closeSearchLayout()
		return cmd

	case tea.KeyTab:
		// Toggle queue for selected search result.
		if len(m.search.results) > 0 && m.search.cursor < len(m.search.results) {
			idx := m.search.results[m.search.cursor]
			if !m.playlist.Dequeue(idx) {
				m.playlist.Queue(idx)
			}
			m.normalizeQueueOverlay()
			return m.rearmStalePreload()
		}

	case tea.KeyUp:
		if m.search.cursor > 0 {
			m.search.cursor--
		} else if len(m.search.results) > 0 {
			m.search.cursor = len(m.search.results) - 1
		}
		m.searchMaybeAdjustScroll(m.effectivePlaylistVisible())

	case tea.KeyDown:
		if m.search.cursor < len(m.search.results)-1 {
			m.search.cursor++
		} else if len(m.search.results) > 0 {
			m.search.cursor = 0
		}
		m.searchMaybeAdjustScroll(m.effectivePlaylistVisible())

	default:
		if m.editText("playlist-search", &m.search.query, msg) {
			m.updateSearch()
		}
	}

	return nil
}

// handleNetSearchKey dispatches key presses to the active net search screen.
func (m *Model) handleNetSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.netSearch.screen {
	case netSearchInput:
		return m.handleNetSearchInputKey(msg)
	case netSearchResults:
		return m.handleNetSearchResultsKey(msg)
	}
	return nil
}

// handleNetSearchInputKey handles text entry on the net search overlay.
func (m *Model) handleNetSearchInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.closeNetSearch()

	case tea.KeyEnter:
		if strings.TrimSpace(m.netSearch.query) == "" {
			m.netSearch.err = "Enter a search query."
			return nil
		}
		if !m.netSearch.loading {
			prefix := "ytsearch10:"
			if m.netSearch.soundcloud {
				prefix = "scsearch10:"
			}
			m.netSearch.loading = true
			m.netSearch.err = ""
			query := prefix + strings.TrimSpace(m.netSearch.query)
			m.netSearch.request = query
			return fetchNetSearchCmd(query, nextRequest(&m.requests.netSearch))
		}

	default:
		if m.editText("net-search", &m.netSearch.query, msg) {
			m.netSearch.err = ""
		}
	}
	return nil
}

func (m *Model) netSearchResultsMaybeAdjustScroll(visible int) {
	clampScroll(&m.netSearch.cursor, &m.netSearch.scroll, len(m.netSearch.results), visible)
}

// handleNetSearchResultsKey handles navigation through net search results.
func (m *Model) handleNetSearchResultsKey(msg tea.KeyPressMsg) tea.Cmd {
	count := len(m.netSearch.results)

	switch msg.String() {
	case "ctrl+x":
		m.toggleExpandedView()
		m.netSearchResultsMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "up", "k", "ctrl+p":
		if m.netSearch.cursor > 0 {
			m.netSearch.cursor--
		} else if count > 0 {
			m.netSearch.cursor = count - 1
		}
		m.netSearchResultsMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "down", "j", "ctrl+n":
		if m.netSearch.cursor < count-1 {
			m.netSearch.cursor++
		} else if count > 0 {
			m.netSearch.cursor = 0
		}
		m.netSearchResultsMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "enter":
		if count > 0 && !m.netSearch.loading {
			track := m.netSearch.results[m.netSearch.cursor]
			m.closeNetSearch()
			return m.playTrackImmediate(track)
		}
	case "a":
		if count > 0 && !m.netSearch.loading {
			track := m.netSearch.results[m.netSearch.cursor]
			m.closeNetSearch()
			return m.appendTrack(track)
		}
	case "q":
		if count > 0 && !m.netSearch.loading {
			track := m.netSearch.results[m.netSearch.cursor]
			m.closeNetSearch()
			return m.queueTrackNext(track)
		}
	case "f":
		if count > 0 && !m.netSearch.loading {
			return m.favoriteTrackKey(m.netSearch.results[m.netSearch.cursor])
		}
	case "c":
		if track, ok := m.selectedSearchResult(); ok && m.canSongRadio(track) {
			m.closeNetSearch()
			return m.startSongRadio(track)
		}
	case "esc", "backspace":
		m.netSearch.screen = netSearchInput
		m.netSearch.results = nil
		m.netSearch.cursor = 0
		m.netSearch.scroll = 0
		m.netSearch.err = ""
	case "ctrl+u":
		step := m.effectivePlaylistVisible()
		if step < 1 {
			step = 1
		}
		if m.netSearch.cursor >= step {
			m.netSearch.cursor -= step
		} else {
			m.netSearch.cursor = 0
		}
		m.netSearchResultsMaybeAdjustScroll(m.effectivePlaylistVisible())
	case "ctrl+d":
		step := m.effectivePlaylistVisible()
		if step < 1 {
			step = 1
		}
		m.netSearch.cursor += step
		if m.netSearch.cursor >= count {
			m.netSearch.cursor = max(0, count-1)
		}
		m.netSearchResultsMaybeAdjustScroll(m.effectivePlaylistVisible())
	}
	return nil
}
