package model

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/luaplugin"
)

// actionPaneState is the "!" pane: every plugin action registered for this
// key, filtered by the text the user types.
type actionPaneState struct {
	visible  bool
	entries  []luaplugin.Action
	filtered []luaplugin.Action
	cursor   int
	scroll   int
	query    string
	height   int
}

// openActionPane collects the actions registered for "!" and shows them.
func (m *Model) openActionPane() {
	if m.luaMgr == nil {
		m.status.Warning("No plugin actions. Install a plugin that registers p:action.", statusTTLDefault)
		return
	}
	entries := m.luaMgr.Actions("!")
	providerName := ""
	if m.provider != nil {
		providerName = m.provider.Name()
	}
	available := make([]luaplugin.Action, 0, len(entries))
	for _, action := range entries {
		if action.Provider == "" || strings.EqualFold(action.Provider, providerName) {
			available = append(available, action)
		}
	}
	entries = available
	if len(entries) == 0 {
		m.status.Warning("No plugin actions for !. Install a plugin that registers p:action.", statusTTLDefault)
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Plugin != entries[j].Plugin {
			return entries[i].Plugin < entries[j].Plugin
		}
		return entries[i].Label < entries[j].Label
	})
	m.actionPane = actionPaneState{
		visible:  true,
		entries:  entries,
		filtered: entries,
		height:   m.height,
	}
}

// handleActionPaneKey owns the keys while the action pane is open.
func (m *Model) handleActionPaneKey(msg tea.KeyPressMsg) tea.Cmd {
	pane := &m.actionPane
	switch msg.String() {
	case "esc":
		m.actionPane = actionPaneState{}
		return nil
	case "up":
		if pane.cursor > 0 {
			pane.cursor--
		} else {
			pane.cursor = max(0, len(pane.filtered)-1)
		}
		pane.adjustScroll()
		return nil
	case "down":
		if pane.cursor < len(pane.filtered)-1 {
			pane.cursor++
		} else {
			pane.cursor = 0
		}
		pane.adjustScroll()
		return nil
	case "enter":
		if len(pane.filtered) == 0 {
			return nil
		}
		action := pane.filtered[pane.cursor]
		m.actionPane = actionPaneState{}
		if action.Prompt != "" {
			m.openLuaInput(action.Prompt, "", "", func(text string) {
				action.Run(text)
			}, nil)
			return nil
		}
		action.Run("")
		return nil
	default:
		if m.editText("action-pane", &pane.query, msg) {
			m.filterActions()
		}
	}
	return nil
}

func (p *actionPaneState) adjustScroll() {
	visible := max(1, p.visibleRows())
	if p.cursor < p.scroll {
		p.scroll = p.cursor
	}
	if p.cursor >= p.scroll+visible {
		p.scroll = p.cursor - visible + 1
	}
}

func (p *actionPaneState) visibleRows() int {
	if p.height <= 0 {
		return 10
	}
	const chrome = 9 // heading, query, footer, blanks and borders
	return max(1, min(10, p.height-chrome))
}

// filterActions keeps the actions whose label, description or plugin contains
// the query.
func (m *Model) filterActions() {
	pane := &m.actionPane
	query := strings.ToLower(strings.TrimSpace(pane.query))
	if query == "" {
		pane.filtered = pane.entries
	} else {
		filtered := make([]luaplugin.Action, 0, len(pane.entries))
		for _, action := range pane.entries {
			text := strings.ToLower(action.Label + " " + action.Description + " " + action.Plugin)
			if strings.Contains(text, query) {
				filtered = append(filtered, action)
			}
		}
		pane.filtered = filtered
	}
	pane.cursor = 0
	pane.scroll = 0
}

// renderActionPane draws the action list as a centered box.
func (m Model) renderActionPane() string {
	pane := m.actionPane
	if !pane.visible {
		return ""
	}
	width := max(40, m.layout.frameWidth/2)
	width = min(width, max(28, m.layout.frameWidth-4))

	var b strings.Builder
	b.WriteString("Actions")
	b.WriteString("\n\n")
	b.WriteString("> " + truncate(pane.query, width-4))
	b.WriteString("\n\n")
	if len(pane.filtered) == 0 {
		b.WriteString(dimStyle.Render("  no matches"))
	} else {
		visible := min(m.actionPane.visibleRows(), len(pane.filtered))
		start := pane.scroll
		if start > len(pane.filtered)-visible {
			start = max(0, len(pane.filtered)-visible)
		}
		end := min(start+visible, len(pane.filtered))
		for i := start; i < end; i++ {
			action := pane.filtered[i]
			prefix := "  "
			if i == pane.cursor {
				prefix = "▸ "
			}
			line := prefix + action.Label
			if action.Plugin != "" && action.Plugin != action.Label {
				line += dimStyle.Render("  " + action.Plugin)
			}
			b.WriteString(truncate(line, width-4))
			if i < end-1 {
				b.WriteString("\n")
			}
		}
		if end < len(pane.filtered) {
			b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  ↓ %d more", len(pane.filtered)-end)))
		}
	}
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  type to filter   enter run   esc cancel"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(width).
		Render(b.String())
}
