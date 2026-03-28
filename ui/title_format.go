package ui

import (
	"strings"

	"cliamp/config"
	"cliamp/playlist"
)

const (
	baseTerminalTitle          = "cliamp"
	defaultTerminalTitleFormat = config.DefaultTerminalTitleFormat
	defaultTerminalTitleIntro  = config.DefaultTerminalTitleIntro
)

type TerminalTitleConfig struct {
	Format string
	Intro  string
}

type terminalTitleRenderer struct {
	format     terminalTitleFormat
	intro      string
	introRunes []rune
	set        bool
}

type terminalTitleFormat struct {
	parts []terminalTitlePart
	set   bool
}

type terminalTitlePart struct {
	literal string
	token   string
	group   []terminalTitlePart
}

type terminalTitleValues struct {
	app         string
	state       string
	stateIcon   string
	metadata    string
	title       string
	artist      string
	album       string
	path        string
	streamTitle string
}

var defaultCompiledTerminalTitleFormat = compileTerminalTitleFormat(defaultTerminalTitleFormat)

var defaultTerminalTitleRenderer = terminalTitleRenderer{
	format:     defaultCompiledTerminalTitleFormat,
	intro:      defaultTerminalTitleIntro,
	introRunes: []rune(defaultTerminalTitleIntro),
	set:        true,
}

func newTerminalTitleRenderer(cfg TerminalTitleConfig) terminalTitleRenderer {
	return terminalTitleRenderer{
		format:     compileTerminalTitleFormat(cfg.Format),
		intro:      cfg.Intro,
		introRunes: []rune(cfg.Intro),
		set:        true,
	}
}

func (r terminalTitleRenderer) withDefaults() terminalTitleRenderer {
	if !r.set {
		return defaultTerminalTitleRenderer
	}
	return r
}

func (r terminalTitleRenderer) introEnabled() bool {
	return len(r.introRunes) > 0
}

func (r terminalTitleRenderer) render(values terminalTitleValues) string {
	return r.withDefaults().format.render(values)
}

func (r terminalTitleRenderer) initialTitle() string {
	r = r.withDefaults()
	if r.introEnabled() {
		return titleIntroFrame(titleIntroInitialOffset(titleIntroViewportDefault), titleIntroViewportDefault, r.introRunes)
	}
	return r.render(terminalTitleStateValues(false, false))
}

func compileTerminalTitleFormat(src string) terminalTitleFormat {
	return terminalTitleFormat{
		parts: parseTerminalTitleParts(src, true),
		set:   true,
	}
}

func parseTerminalTitleParts(src string, allowGroups bool) []terminalTitlePart {
	var parts []terminalTitlePart
	var literal strings.Builder

	flushLiteral := func() {
		if literal.Len() == 0 {
			return
		}
		parts = append(parts, terminalTitlePart{literal: literal.String()})
		literal.Reset()
	}

	for i := 0; i < len(src); {
		switch src[i] {
		case '%':
			end := strings.IndexByte(src[i+1:], '%')
			if end < 0 {
				literal.WriteString(src[i:])
				i = len(src)
				continue
			}
			end += i + 1
			raw := src[i : end+1]
			token := normalizeTerminalTitleToken(src[i+1 : end])
			if token == "" {
				literal.WriteString(raw)
			} else {
				flushLiteral()
				parts = append(parts, terminalTitlePart{token: token})
			}
			i = end + 1
		case '[':
			if !allowGroups {
				literal.WriteByte(src[i])
				i++
				continue
			}
			end := strings.IndexByte(src[i+1:], ']')
			if end < 0 {
				literal.WriteString(src[i:])
				i = len(src)
				continue
			}
			end += i + 1
			flushLiteral()
			parts = append(parts, terminalTitlePart{
				group: parseTerminalTitleParts(src[i+1:end], false),
			})
			i = end + 1
		default:
			literal.WriteByte(src[i])
			i++
		}
	}

	flushLiteral()
	return parts
}

func normalizeTerminalTitleToken(token string) string {
	switch strings.ToLower(token) {
	case "app", "state", "state_icon", "metadata", "title", "artist", "album", "path", "stream_title":
		return strings.ToLower(token)
	default:
		return ""
	}
}

func (f terminalTitleFormat) render(values terminalTitleValues) string {
	if !f.set {
		f = defaultCompiledTerminalTitleFormat
	}
	var b strings.Builder
	for _, part := range f.parts {
		b.WriteString(part.render(values))
	}
	return b.String()
}

func (p terminalTitlePart) render(values terminalTitleValues) string {
	switch {
	case p.token != "":
		return values.value(p.token)
	case p.group != nil:
		rendered, ok := renderTerminalTitleGroup(p.group, values)
		if !ok {
			return ""
		}
		return rendered
	default:
		return p.literal
	}
}

func renderTerminalTitleGroup(parts []terminalTitlePart, values terminalTitleValues) (string, bool) {
	var b strings.Builder
	for _, part := range parts {
		switch {
		case part.token != "":
			value := values.value(part.token)
			if value == "" {
				return "", false
			}
			b.WriteString(value)
		case part.group != nil:
			rendered, ok := renderTerminalTitleGroup(part.group, values)
			if !ok {
				return "", false
			}
			b.WriteString(rendered)
		default:
			b.WriteString(part.literal)
		}
	}
	return b.String(), true
}

func (v terminalTitleValues) value(token string) string {
	switch token {
	case "app":
		return v.app
	case "state":
		return v.state
	case "state_icon":
		return v.stateIcon
	case "metadata":
		return v.metadata
	case "title":
		return v.title
	case "artist":
		return v.artist
	case "album":
		return v.album
	case "path":
		return v.path
	case "stream_title":
		return v.streamTitle
	default:
		return ""
	}
}

func terminalTitleStateValues(playing, paused bool) terminalTitleValues {
	values := terminalTitleValues{app: baseTerminalTitle}
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

	values.album = track.Album
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
