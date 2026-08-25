package model

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/provider"
)

const builtinRadioPlaylistID = "l:0"

func (m Model) canOpenRadioStats() bool {
	if m.provCursor < 0 || m.provCursor >= len(m.providerLists) ||
		m.providerLists[m.provCursor].ID != builtinRadioPlaylistID {
		return false
	}
	_, ok := m.provider.(provider.RadioStatsLoader)
	return ok
}

func (m *Model) openRadioStats() tea.Cmd {
	loader, ok := m.provider.(provider.RadioStatsLoader)
	if !ok {
		return nil
	}
	m.radioStats.visible = true
	m.radioStats.loading = true
	m.radioStats.stats = provider.RadioStats{}
	m.radioStats.err = nil
	m.radioStats.scroll = 0
	return fetchRadioStatsCmd(loader, nextRequest(&m.requests.radioStats))
}

func (m *Model) closeRadioStats() {
	nextRequest(&m.requests.radioStats)
	m.radioStats.visible = false
	m.radioStats.loading = false
	m.radioStats.err = nil
	m.radioStats.scroll = 0
}

func (m *Model) refreshRadioStats() tea.Cmd {
	loader, ok := m.provider.(provider.RadioStatsLoader)
	if !ok {
		return nil
	}
	m.radioStats.loading = true
	m.radioStats.err = nil
	return fetchRadioStatsCmd(loader, nextRequest(&m.requests.radioStats))
}

func (m *Model) handleRadioStatsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "esc":
		m.closeRadioStats()
	case "r":
		return m.refreshRadioStats()
	case "up", "k":
		if m.radioStats.scroll > 0 {
			m.radioStats.scroll--
		}
	case "down", "j":
		m.radioStats.scroll++
		m.radioStatsMaybeAdjustScroll()
	}
	return nil
}

func (m Model) radioStatsLines() []string {
	stats := m.radioStats.stats
	stationKeys := make([]string, 0, len(stats.Stations))
	liveListeners := 0
	for key, station := range stats.Stations {
		stationKeys = append(stationKeys, key)
		liveListeners += station.ActiveListeners
	}
	sort.Strings(stationKeys)

	lines := []string{
		fmt.Sprintf("  Live listeners: %d", liveListeners),
		fmt.Sprintf("  Peak listeners: %d", stats.PeakListeners),
		fmt.Sprintf("  Total sessions: %d", stats.TotalSessions),
		fmt.Sprintf("  Listen time: %.1f hours", stats.TotalListenHours),
	}
	if len(stationKeys) == 0 {
		return lines
	}

	lines = append(lines, "", dimStyle.Render("  Station          Live   Sessions"))
	for _, key := range stationKeys {
		station := stats.Stations[key]
		name := truncate(strings.ToUpper(key), 14)
		lines = append(lines, fmt.Sprintf("  %-14s %4d %10d", name, station.ActiveListeners, station.TotalSessions))
	}
	return lines
}

func (m *Model) radioStatsMaybeAdjustScroll() {
	if m.radioStats.loading || m.radioStats.err != nil {
		m.radioStats.scroll = 0
		return
	}
	m.radioStats.scroll = min(m.radioStats.scroll, max(0, len(m.radioStatsLines())-m.effectivePlaylistVisible()))
}

func (m Model) renderRadioStatsBody() string {
	budget := m.effectivePlaylistVisible()
	if budget <= 0 {
		return ""
	}
	if m.radioStats.loading {
		return bodyLines([]string{loadingLine("Loading cliamp radio stats...")}, budget)
	}
	if m.radioStats.err != nil {
		return bodyLines([]string{errorStyle.Render("  Could not load radio stats.")}, budget)
	}

	lines := m.radioStatsLines()
	start := min(m.radioStats.scroll, max(0, len(lines)-budget))
	return bodyLines(lines[start:], budget)
}
