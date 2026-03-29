package ui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"cliamp/playlist"
)

const (
	baseTerminalTitle         = "cliamp"
	defaultTerminalTitleIntro = "It really whips the terminal's ass."
	titleIntroViewportMin     = 18
	titleIntroViewportDefault = 24
	titleIntroStep            = 2
	titleIntroTickDivisor     = 2
)

type terminalTitleValues struct {
	state       string
	stateIcon   string
	metadata    string
	title       string
	artist      string
	path        string
	streamTitle string
}

var defaultTerminalTitleIntroRunes = []rune(defaultTerminalTitleIntro)

func InitialTerminalTitle() string {
	return sanitizeTerminalTitle(titleIntroFrame(titleIntroInitialOffset(titleIntroViewportDefault), titleIntroViewportDefault, defaultTerminalTitleIntroRunes))
}

func initialTerminalTitleState() terminalTitleState {
	if len(defaultTerminalTitleIntroRunes) == 0 {
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

func renderTerminalTitle(values terminalTitleValues) string {
	switch {
	case values.stateIcon != "" && values.metadata != "":
		return values.stateIcon + " " + values.metadata + " | " + baseTerminalTitle
	case values.stateIcon != "":
		return values.stateIcon + " " + baseTerminalTitle
	case values.metadata != "":
		return values.metadata + " | " + baseTerminalTitle
	default:
		return baseTerminalTitle
	}
}

func currentTerminalTitle(state terminalTitleState, width int, values terminalTitleValues) string {
	if state.introActive && len(defaultTerminalTitleIntroRunes) > 0 {
		return sanitizeTerminalTitle(titleIntroFrame(state.introOffset, titleIntroViewportForWidth(width, len(defaultTerminalTitleIntroRunes)), defaultTerminalTitleIntroRunes))
	}
	return sanitizeTerminalTitle(renderTerminalTitle(values))
}

func advanceTerminalTitleState(state *terminalTitleState, width int) {
	if !state.introActive || len(defaultTerminalTitleIntroRunes) == 0 {
		return
	}

	state.introTick++
	if state.introTick < titleIntroTickDivisor {
		return
	}

	state.introTick = 0
	maxOffset := titleIntroMaxOffset(titleIntroViewportForWidth(width, len(defaultTerminalTitleIntroRunes)), len(defaultTerminalTitleIntroRunes))
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

func (m *Model) terminalTitleCmd() tea.Cmd {
	title := currentTerminalTitle(m.termTitle, m.width, m.terminalTitleValues())
	if title == m.termTitle.last {
		return nil
	}
	m.termTitle.last = title
	return tea.SetWindowTitle(title)
}

func (m *Model) advanceTerminalTitle() {
	advanceTerminalTitleState(&m.termTitle, m.width)
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

func terminalTitleStateValues(playing, paused bool) terminalTitleValues {
	values := terminalTitleValues{}
	switch {
	case playing && !paused:
		values.state = "playing"
		values.stateIcon = "▶"
	case paused:
		values.state = "paused"
		values.stateIcon = "⏸"
	default:
		values.state = "stopped"
	}
	return values
}

func terminalTitleMetadata(title, artist, path string) string {
	switch {
	case title != "" && artist != "":
		return title + " - " + artist
	case title != "":
		return title
	case artist != "":
		return artist
	case path != "":
		return path
	default:
		return ""
	}
}

func terminalTitleValuesForTrack(track playlist.Track, streamTitle string, playing, paused bool) terminalTitleValues {
	values := terminalTitleStateValues(playing, paused)
	if !playing && !paused {
		return values
	}

	values.path = track.Path

	switch {
	case track.Stream && streamTitle != "":
		values.streamTitle = streamTitle
		if artist, title, ok := strings.Cut(streamTitle, " - "); ok && artist != "" && title != "" {
			values.artist = artist
			values.title = title
		} else {
			values.title = streamTitle
		}
	default:
		values.title = track.Title
		values.artist = track.Artist
	}

	values.metadata = terminalTitleMetadata(values.title, values.artist, values.path)
	return values
}
