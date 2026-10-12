package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/bandcamp"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
)

// netSearchAllLimit is the most results that one source of an all-sources
// search shows.
const netSearchAllLimit = 10

// netSearchAllTimeout bounds an all-sources search. A source that has not
// answered by then reports a timeout, so one slow server cannot hold the
// search open.
const netSearchAllTimeout = 30 * time.Second

// netSearchAllSkip holds the provider keys that an all-sources search leaves
// out. The YouTube and SoundCloud providers search the same catalogs as the
// built-in sources. Podcasts finds shows, not tracks, and Local filters the
// saved playlists.
var netSearchAllSkip = map[string]bool{
	"yt": true, "youtube": true, "ytmusic": true, "soundcloud": true, "podcast": true, "local": true,
}

// netSearchAllSource is one source of an all-sources search.
type netSearchAllSource struct {
	name   string
	search func(ctx context.Context, query string) ([]playlist.Track, error)
}

// netSearchAllBuiltins are the sources that an all-sources search always
// queries, before the providers. Tests replace them.
var netSearchAllBuiltins = []netSearchAllSource{
	{name: "YouTube", search: ytdlSearch("ytsearch")},
	{name: "SoundCloud", search: ytdlSearch("scsearch")},
	{name: "Bandcamp", search: func(ctx context.Context, query string) ([]playlist.Track, error) {
		return bandcamp.SearchTracks(ctx, query, netSearchAllLimit)
	}},
}

// ytdlSearch returns a search through a yt-dlp search prefix such as
// "ytsearch". The cookies configured for the site apply.
func ytdlSearch(prefix string) func(context.Context, string) ([]playlist.Track, error) {
	return func(ctx context.Context, query string) ([]playlist.Track, error) {
		return resolve.ResolveYTDLBatchContext(ctx, fmt.Sprintf("%s%d:%s", prefix, netSearchAllLimit, query), 0, 0, "")
	}
}

// next returns the mode that Tab switches to: YouTube, SoundCloud, all
// sources, then YouTube again.
func (mode netSearchMode) next() netSearchMode {
	if mode == netSearchAll {
		return netSearchYouTube
	}
	return mode + 1
}

// netSearchAllSources lists the sources of an all-sources search in display
// order: the built-in sources, then each configured provider with a track
// search, in provider order.
func (m Model) netSearchAllSources() []netSearchAllSource {
	sources := append([]netSearchAllSource(nil), netSearchAllBuiltins...)
	for _, e := range m.providers {
		if e.Provider == nil || netSearchAllSkip[e.Key] {
			continue
		}
		s, ok := e.Provider.(provider.Searcher)
		if !ok {
			continue
		}
		sources = append(sources, netSearchAllSource{name: e.Name, search: func(ctx context.Context, query string) ([]playlist.Track, error) {
			return s.SearchTracks(ctx, query, netSearchAllLimit)
		}})
	}
	return sources
}

// netSearchSourceResultsMsg carries the answer of one source of an
// all-sources search.
type netSearchSourceResultsMsg struct {
	tracks  []playlist.Track
	err     error
	request string
	source  int
	gen     uint64
}

func fetchNetSearchSourceCmd(ctx context.Context, src netSearchAllSource, request, query string, source int, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := src.search(ctx, query)
		return netSearchSourceResultsMsg{tracks: tracks, err: err, request: request, source: source, gen: gen}
	}
}

// startNetSearchAll searches every source at once. Each source answers with
// its own message, so results show as soon as the first source has some.
func (m *Model) startNetSearchAll(query string) tea.Cmd {
	sources := m.netSearchAllSources()
	m.netSearch.loading = true
	m.netSearch.err = ""
	m.netSearch.request = "all:" + query
	m.netSearch.results = nil
	m.netSearch.resultSection = nil
	m.netSearch.sections = make([]netSearchSection, len(sources))
	for i, src := range sources {
		m.netSearch.sections[i].name = src.name
	}
	m.netSearch.pending = len(sources)
	m.netSearch.cursorMoved = false
	ctx := m.newNetSearchRequestContext(netSearchAllTimeout)
	gen := nextRequest(&m.requests.netSearch)
	cmds := make([]tea.Cmd, len(sources))
	for i, src := range sources {
		cmds[i] = fetchNetSearchSourceCmd(ctx, src, m.netSearch.request, query, i, gen)
	}
	return tea.Batch(cmds...)
}

// handleNetSearchSourceResults files the answer of one source under its
// section. The first answer with results opens the result list with the
// cursor on the first result, and the cursor stays there while more sources
// answer. Once the user has moved it, a later answer keeps the cursor on the
// track that it was on.
func (m *Model) handleNetSearchSourceResults(msg netSearchSourceResultsMsg) {
	if msg.gen != m.requests.netSearch || !m.netSearch.active || msg.request != m.netSearch.request ||
		msg.source < 0 || msg.source >= len(m.netSearch.sections) || m.netSearch.sections[msg.source].done {
		return
	}
	sec := &m.netSearch.sections[msg.source]
	sec.done = true
	sec.err = msg.err
	sec.tracks = playableResults(msg.tracks)
	m.netSearch.pending--
	if m.netSearch.pending == 0 {
		m.cancelNetSearchRequest()
	}

	selSection, selPath := -1, ""
	if c := m.netSearch.cursor; m.netSearch.screen == netSearchResults && c >= 0 && c < len(m.netSearch.results) {
		selSection, selPath = m.netSearch.resultSection[c], m.netSearch.results[c].Path
	}
	m.rebuildNetSearchAllResults()

	switch {
	case len(m.netSearch.results) == 0:
		if m.netSearch.pending == 0 {
			// The input screen lists why under the error, see
			// netSearchAllProblems.
			m.netSearch.loading = false
			m.netSearch.err = "No results found"
		}
	case m.netSearch.screen == netSearchInput:
		m.netSearch.loading = false
		m.netSearch.screen = netSearchResults
		m.netSearch.cursor = 0
		m.netSearch.scroll = 0
	case m.netSearch.cursorMoved:
		for i, t := range m.netSearch.results {
			if m.netSearch.resultSection[i] == selSection && t.Path == selPath {
				m.netSearch.cursor = i
				break
			}
		}
	}
	m.applyHeightMode()
	m.clampActiveScrollState()
}

// rebuildNetSearchAllResults lists the tracks of every section in section
// order, so results and resultSection line up for the cursor and the view.
func (m *Model) rebuildNetSearchAllResults() {
	var results []playlist.Track
	var sections []int
	for i, sec := range m.netSearch.sections {
		for _, t := range sec.tracks {
			results = append(results, t)
			sections = append(sections, i)
		}
	}
	m.netSearch.results = results
	m.netSearch.resultSection = sections
}

// playableResults drops album placeholders, which need the album expansion of
// the provider search, and keeps at most netSearchAllLimit tracks.
func playableResults(tracks []playlist.Track) []playlist.Track {
	out := make([]playlist.Track, 0, min(len(tracks), netSearchAllLimit))
	for _, t := range tracks {
		if len(out) == netSearchAllLimit {
			break
		}
		if !t.IsAlbum() {
			out = append(out, t)
		}
	}
	return out
}

// netSearchResultSection names the section of result i, with its result
// count, for the section separators.
func (m *Model) netSearchResultSection(i int) string {
	if i < 0 || i >= len(m.netSearch.resultSection) {
		return ""
	}
	sec := m.netSearch.sections[m.netSearch.resultSection[i]]
	return fmt.Sprintf("%s (%d)", sec.name, len(sec.tracks))
}

// netSearchErrWidth bounds the error of one source in the one-line note, so
// a long yt-dlp error leaves room for the other sources.
const netSearchErrWidth = 40

// netSearchAllNote names the sources that still search. Once every source
// has answered, it names the sources that found nothing or failed. It is
// empty when every source has results.
func (m *Model) netSearchAllNote() string {
	if m.netSearch.mode != netSearchAll {
		return ""
	}
	var searching, empty, failed []string
	for _, sec := range m.netSearch.sections {
		switch {
		case !sec.done:
			searching = append(searching, sec.name)
		case sec.err != nil:
			failed = append(failed, sec.name+" ("+truncate(sec.errText(), netSearchErrWidth)+")")
		case len(sec.tracks) == 0:
			empty = append(empty, sec.name)
		}
	}
	if len(searching) > 0 {
		return "Searching " + strings.Join(searching, ", ") + "..."
	}
	var parts []string
	if len(empty) > 0 {
		parts = append(parts, "Nothing on "+strings.Join(empty, ", "))
	}
	if len(failed) > 0 {
		parts = append(parts, "Failed: "+strings.Join(failed, ", "))
	}
	return strings.Join(parts, ". ")
}

// netSearchAllProblems lists the sources of an all-sources search that failed,
// one line each with the whole error, then the sources that found nothing.
func (m *Model) netSearchAllProblems() []string {
	var lines, empty []string
	for _, sec := range m.netSearch.sections {
		switch {
		case !sec.done:
		case sec.err != nil:
			lines = append(lines, sec.name+": "+sec.errText())
		case len(sec.tracks) == 0:
			empty = append(empty, sec.name)
		}
	}
	if len(empty) > 0 {
		lines = append(lines, "Nothing on "+strings.Join(empty, ", "))
	}
	return lines
}

// errText returns the error of the section as one line. The source name
// precedes it on screen, so a leading "name: " that the provider added goes.
func (sec netSearchSection) errText() string {
	text := netSearchErrText(sec.err)
	prefix := sec.name + ": "
	if len(text) > len(prefix) && strings.EqualFold(text[:len(prefix)], prefix) {
		return text[len(prefix):]
	}
	return text
}

// netSearchErrText shortens the error of a source to one line. yt-dlp can
// print warnings, such as the one about an old version, before its error, so
// the text after "ERROR: " wins.
func netSearchErrText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	lines := strings.Split(err.Error(), "\n")
	for _, line := range lines {
		if _, msg, ok := strings.Cut(line, "ERROR: "); ok {
			return strings.TrimSpace(msg)
		}
	}
	return strings.TrimSpace(lines[0])
}

// netSearchResultsVisible returns the rows that the result list can use. The
// note of an all-sources search takes the last row.
func (m *Model) netSearchResultsVisible() int {
	visible := m.effectivePlaylistVisible()
	if m.netSearchAllNote() != "" {
		visible--
	}
	return max(0, visible)
}

// renderNetSearchAllResults renders the results of an all-sources search
// grouped under one separator per source, then the note.
func (m Model) renderNetSearchAllResults(budget int) string {
	note := m.netSearchAllNote()
	listBudget := budget
	if note != "" {
		listBudget = max(0, budget-1)
	}
	lines := make([]string, 0, budget)
	for row := range sectionRows(m.netSearch.results, m.netSearch.scroll, m.netSearchResultSection) {
		if len(lines) >= listBudget {
			break
		}
		if row.Index < 0 {
			if listBudget == 1 {
				continue
			}
			lines = append(lines, dimStyle.Render(labeledSeparator("", row.Section, m.layout.panelWidth)))
			continue
		}
		label := truncate(trackViewName(row.Track), m.layout.panelWidth-8)
		lines = append(lines, cursorLine(label, row.Index == m.netSearch.cursor))
	}
	lines = padLines(lines, listBudget, len(lines))
	if note != "" {
		lines = append(lines, dimStyle.Render("  "+m.fitPanel(note, 2)))
	}
	return strings.Join(lines, "\n")
}

// newNetSearchRequestContext returns the context of a new all-sources
// search, and cancels the search before it.
func (m *Model) newNetSearchRequestContext(timeout time.Duration) context.Context {
	m.cancelNetSearchRequest()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	m.netSearch.cancel = cancel
	return ctx
}

// cancelNetSearchRequest stops the sources of an all-sources search that
// still run.
func (m *Model) cancelNetSearchRequest() {
	if m.netSearch.cancel != nil {
		m.netSearch.cancel()
		m.netSearch.cancel = nil
	}
}
