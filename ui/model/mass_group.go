package model

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/mass"
)

// massGroupState is the speaker picker: the Music Assistant players this
// computer can play in sync with.
type massGroupState struct {
	visible bool
	rows    []mass.Player
	localID string
	cursor  int
	scroll  int
	loading bool
	err     error
}

type massPlayersMsg struct {
	rows    []mass.Player
	localID string
	err     error
	status  string
}

func (m *Model) openMassGroup() tea.Cmd {
	if m.massLink == nil {
		m.status.Warning("Music Assistant is not set up; see docs/music-assistant.md", statusTTLDefault)
		return nil
	}
	m.massGroup = massGroupState{visible: true, loading: true}
	return m.massLoadPlayers("")
}

// massLoadPlayers fetches the player list, after running change first when
// it is set. status is shown when the list arrives.
func (m *Model) massLoadPlayers(status string, change ...func(context.Context, MusicAssistant, string) error) tea.Cmd {
	link := m.massLink
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), massCommandTimeout)
		defer cancel()
		id, err := link.localPlayerID(ctx)
		if err != nil {
			return massPlayersMsg{err: err}
		}
		for _, fn := range change {
			if err := fn(ctx, link.ma, id); err != nil {
				return massPlayersMsg{err: err}
			}
		}
		rows, err := link.ma.Players(ctx)
		return massPlayersMsg{rows: rows, localID: id, err: err, status: status}
	}
}

func (m *Model) applyMassPlayers(msg massPlayersMsg) {
	m.massGroup.loading = false
	m.massGroup.err = msg.err
	if msg.err != nil {
		return
	}
	// This computer first, then Music Assistant's order.
	rows := slices.Clone(msg.rows)
	if i := slices.IndexFunc(rows, func(p mass.Player) bool { return p.ID == msg.localID }); i > 0 {
		local := rows[i]
		copy(rows[1:i+1], rows[:i])
		rows[0] = local
	}
	m.massGroup.rows = rows
	m.massGroup.localID = msg.localID
	m.massGroup.cursor = min(m.massGroup.cursor, max(0, len(msg.rows)-1))
	if msg.status != "" {
		m.status.Show(msg.status, statusTTLDefault)
	}
}

func (m *Model) handleMassGroupKey(msg tea.KeyPressMsg) tea.Cmd {
	g := &m.massGroup
	switch msg.String() {
	case "up", "k":
		g.cursor = max(0, g.cursor-1)
	case "down", "j":
		g.cursor = min(max(0, len(g.rows)-1), g.cursor+1)
	case "esc", "q", "D":
		g.visible = false
	case "space":
		return m.massToggleSpeaker()
	case "u":
		if g.localID == "" || g.loading {
			return nil
		}
		g.loading = true
		return m.massLoadPlayers("Left the group", func(ctx context.Context, ma MusicAssistant, id string) error {
			return ma.Leave(ctx, id)
		})
	}
	return nil
}

// inLocalGroup reports whether pl plays in sync with this computer.
func (g *massGroupState) inLocalGroup(pl mass.Player) bool {
	if pl.SyncedTo == g.localID {
		return true
	}
	for _, row := range g.rows {
		if row.ID == g.localID {
			return slices.Contains(row.Members, pl.ID)
		}
	}
	return false
}

// massToggleSpeaker adds the highlighted speaker to this computer's group, or
// takes it out if it is already in.
func (m *Model) massToggleSpeaker() tea.Cmd {
	g := &m.massGroup
	if g.loading || g.cursor >= len(g.rows) {
		return nil
	}
	row := g.rows[g.cursor]
	switch {
	case row.ID == g.localID:
		return nil
	case !row.Available:
		m.status.Warning(row.Name+" is offline", statusTTLDefault)
		return nil
	}
	g.loading = true
	if g.inLocalGroup(row) {
		return m.massLoadPlayers("Removed "+row.Name, func(ctx context.Context, ma MusicAssistant, _ string) error {
			return ma.Leave(ctx, row.ID)
		})
	}
	return m.massLoadPlayers("Added "+row.Name, func(ctx context.Context, ma MusicAssistant, id string) error {
		return ma.Join(ctx, id, row.ID)
	})
}

func (m Model) massGroupHeader() string {
	if m.massGroup.loading {
		return sepHeader("Speakers", m.layout.panelWidth)
	}
	return sepHeaderN("Speakers", m.massGroup.cursor+1, len(m.massGroup.rows), m.layout.panelWidth)
}

func (m Model) renderMassGroup() string {
	g := &m.massGroup
	budget := m.effectivePlaylistVisible()
	switch {
	case g.loading && len(g.rows) == 0:
		return bodyLines([]string{loadingLine("Loading players…")}, budget)
	case g.err != nil:
		return bodyMessage(g.err.Error(), budget)
	case len(g.rows) == 0:
		return bodyMessage("Music Assistant has no players.", budget)
	}
	items := make([]string, len(g.rows))
	for i, pl := range g.rows {
		mark := "[ ]"
		switch {
		case pl.ID == g.localID:
			mark = " ★ "
		case g.inLocalGroup(pl):
			mark = "[x]"
		}
		line := mark + " " + pl.Name
		switch {
		case !pl.Available:
			line += "  (offline)"
		case pl.ID != g.localID && pl.SyncedTo != "" && pl.SyncedTo != g.localID:
			line += "  (in another group)"
		}
		items[i] = line
	}
	return windowList(items, g.cursor, g.scroll, budget)
}
