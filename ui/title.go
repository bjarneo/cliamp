package ui

import (
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	titleIntroViewportMin     = 18
	titleIntroViewportDefault = 24
	titleIntroStep            = 2
	titleIntroTickDivisor     = 2
)

func InitialTerminalTitle(cfg TerminalTitleConfig) string {
	return sanitizeTerminalTitle(newTerminalTitleRenderer(cfg).initialTitle())
}

func initialTerminalTitleState(renderer terminalTitleRenderer) terminalTitleState {
	renderer = renderer.withDefaults()
	if !renderer.introEnabled() {
		return terminalTitleState{}
	}
	return terminalTitleState{
		introActive: true,
		introOffset: titleIntroInitialOffset(titleIntroViewportDefault),
	}
}

func titleIntroViewportForWidth(width int, introLen int) int {
	if width <= 0 {
		return titleIntroViewportDefault
	}
	return max(titleIntroViewportMin, min(introLen, width/3))
}

func titleIntroInitialOffset(viewport int) int {
	return min(4, viewport)
}

func titleIntroMaxOffset(viewport, introLen int) int {
	return introLen + viewport
}

func titleIntroFrame(offset, viewport int, introRunes []rune) string {
	maxOffset := titleIntroMaxOffset(viewport, len(introRunes))
	switch {
	case offset < 0:
		offset = 0
	case offset > maxOffset:
		offset = maxOffset
	}

	padded := make([]rune, 0, viewport+len(introRunes)+viewport)
	padded = append(padded, []rune(strings.Repeat(" ", viewport))...)
	padded = append(padded, introRunes...)
	padded = append(padded, []rune(strings.Repeat(" ", viewport))...)
	return string(padded[offset : offset+viewport])
}

func currentTerminalTitle(state terminalTitleState, renderer terminalTitleRenderer, width int, values terminalTitleValues) string {
	renderer = renderer.withDefaults()
	if state.introActive && renderer.introEnabled() {
		return sanitizeTerminalTitle(titleIntroFrame(state.introOffset, titleIntroViewportForWidth(width, len(renderer.introRunes)), renderer.introRunes))
	}
	return sanitizeTerminalTitle(renderer.render(values))
}

func advanceTerminalTitleState(state *terminalTitleState, renderer terminalTitleRenderer, width int) {
	renderer = renderer.withDefaults()
	if !state.introActive || !renderer.introEnabled() {
		return
	}

	state.introTick++
	if state.introTick < titleIntroTickDivisor {
		return
	}

	state.introTick = 0
	maxOffset := titleIntroMaxOffset(titleIntroViewportForWidth(width, len(renderer.introRunes)), len(renderer.introRunes))
	if state.introOffset >= maxOffset {
		state.introActive = false
		state.introOffset = maxOffset
		state.introTick = 0
		return
	}

	state.introOffset += titleIntroStep
	if state.introOffset > maxOffset {
		state.introOffset = maxOffset
	}
}

func sanitizeTerminalTitle(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	lastWasSpace := false

	for _, r := range title {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			if !lastWasSpace {
				b.WriteByte(' ')
				lastWasSpace = true
			}
		case unicode.IsControl(r) || (r >= 0x80 && r <= 0x9f):
		default:
			b.WriteRune(r)
			lastWasSpace = r == ' '
		}
	}

	return b.String()
}

func terminalTickInterval(introActive, visualizerVisible, playing, paused bool) time.Duration {
	if introActive || (visualizerVisible && playing && !paused) {
		return tickFast
	}
	return tickSlow
}

func (m *Model) terminalTitleCmd() tea.Cmd {
	title := currentTerminalTitle(m.termTitle, m.terminalTitleRenderer(), m.width, m.terminalTitleValues())
	if title == m.termTitle.last {
		return nil
	}
	m.termTitle.last = title
	return tea.SetWindowTitle(title)
}

func (m *Model) advanceTerminalTitle() {
	advanceTerminalTitleState(&m.termTitle, m.terminalTitleRenderer(), m.width)
}

func (m Model) tickInterval() time.Duration {
	return terminalTickInterval(m.termTitle.introActive, m.visualizerVisible(), m.isPlaying(), m.isPaused())
}

func (m Model) isPlaying() bool {
	return m.player != nil && m.player.IsPlaying()
}

func (m Model) isPaused() bool {
	return m.player != nil && m.player.IsPaused()
}

func (m Model) visualizerVisible() bool {
	return m.vis != nil && m.vis.Mode != VisNone && !m.isOverlayActive()
}

func (m Model) terminalTitleRenderer() terminalTitleRenderer {
	return m.termTitleRenderer.withDefaults()
}

func (m Model) terminalTitleValues() terminalTitleValues {
	if m.playlist == nil {
		return terminalTitleStateValues(m.isPlaying(), m.isPaused())
	}
	track, idx := m.playlist.Current()
	if idx < 0 {
		return terminalTitleStateValues(m.isPlaying(), m.isPaused())
	}
	return terminalTitleValuesForTrack(track, m.streamTitle, m.isPlaying(), m.isPaused())
}
