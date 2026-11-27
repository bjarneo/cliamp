package model

import (
	"strings"
	"unicode"

	"github.com/bjarneo/cliamp/playlist"
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
	stateIcon   string
	metadata    string
	title       string
	artist      string
	path        string
	streamTitle string
}

var defaultTerminalTitleIntroRunes = []rune(defaultTerminalTitleIntro)

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
	if viewport <= 0 {
		return ""
	}
	maxOffset := titleIntroMaxOffset(viewport, len(introRunes))
	switch {
	case offset < 0:
		offset = 0
	case offset > maxOffset:
		offset = maxOffset
	}

	end := offset + viewport
	leadSpaces := max(0, min(end, viewport)-offset)
	introStart := max(0, offset-viewport)
	introEnd := min(len(introRunes), max(0, end-viewport))
	introCount := max(0, introEnd-introStart)
	trailSpaces := viewport - leadSpaces - introCount

	var b strings.Builder
	b.Grow(viewport + introCount)
	for range leadSpaces {
		b.WriteByte(' ')
	}
	for i := introStart; i < introEnd; i++ {
		b.WriteRune(introRunes[i])
	}
	for range trailSpaces {
		b.WriteByte(' ')
	}
	return b.String()
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

func (m *Model) advanceTerminalTitle() {
	advanceTerminalTitleState(&m.termTitle, m.width)
}

func (m Model) cachedWindowTitle() string {
	if m.titleCache == nil {
		return currentTerminalTitle(m.termTitle, m.width, m.terminalTitleValues())
	}

	key := titleCacheKey{
		width:       m.width,
		introActive: m.termTitle.introActive,
		introOffset: m.termTitle.introOffset,
	}
	var values terminalTitleValues
	if !m.termTitle.introActive || len(defaultTerminalTitleIntroRunes) == 0 {
		playing := m.isPlaying()
		paused := m.isPaused()
		switch {
		case paused:
			key.status = 2
		case playing:
			key.status = 1
		}
		var track playlist.Track
		idx := -1
		if key.status != 0 && m.playlist != nil {
			track, idx = m.currentPlaybackTrack()
			if idx >= 0 {
				key.trackPath = track.Path
				key.title = track.Title
				key.artist = track.Artist
				key.stream = track.Stream
				if track.Stream {
					key.streamTitle = m.streamTitle
				}
			}
		}
		if m.titleCache.valid && m.titleCache.key == key {
			return m.titleCache.value
		}
		if idx >= 0 {
			values = terminalTitleValuesForTrack(track, m.streamTitle, playing, paused)
		} else {
			values = terminalTitleStateValues(playing, paused)
		}
	} else if m.titleCache.valid && m.titleCache.key == key {
		return m.titleCache.value
	}

	title := currentTerminalTitle(m.termTitle, m.width, values)
	m.titleCache.key = key
	m.titleCache.value = title
	m.titleCache.valid = true
	return title
}

func (m Model) terminalTitleValues() terminalTitleValues {
	if m.playlist == nil {
		return terminalTitleStateValues(m.isPlaying(), m.isPaused())
	}
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		return terminalTitleStateValues(m.isPlaying(), m.isPaused())
	}
	return terminalTitleValuesForTrack(track, m.streamTitle, m.isPlaying(), m.isPaused())
}

func terminalTitleStateValues(playing, paused bool) terminalTitleValues {
	values := terminalTitleValues{}
	switch {
	case playing && !paused:
		values.stateIcon = "▶"
	case paused:
		values.stateIcon = "⏸"
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

	if track.Stream {
		values.streamTitle = streamTitle
	}
	values.artist, values.title = streamDisplay(track, streamTitle)

	values.metadata = terminalTitleMetadata(values.title, values.artist, values.path)
	return values
}
