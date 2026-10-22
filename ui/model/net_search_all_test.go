package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// trackSearchTestProvider answers every track search with tracks and err.
type trackSearchTestProvider struct {
	commandsTestProvider
	tracks []playlist.Track
	err    error
}

func (p trackSearchTestProvider) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return p.tracks, p.err
}

func searchTestProvider(name string, tracks ...playlist.Track) trackSearchTestProvider {
	return trackSearchTestProvider{commandsTestProvider: commandsTestProvider{name: name}, tracks: tracks}
}

func namedTracks(prefix string, n int) []playlist.Track {
	tracks := make([]playlist.Track, n)
	for i := range tracks {
		tracks[i] = playlist.Track{Path: fmt.Sprintf("https://%s.example/%d", prefix, i), Title: fmt.Sprintf("%s %d", prefix, i), Artist: prefix}
	}
	return tracks
}

// useTestBuiltins replaces the built-in sources of an all-sources search for
// one test.
func useTestBuiltins(t *testing.T, sources ...netSearchAllSource) {
	t.Helper()
	orig := netSearchAllBuiltins
	netSearchAllBuiltins = sources
	t.Cleanup(func() { netSearchAllBuiltins = orig })
}

func idleSource(name string) netSearchAllSource {
	return netSearchAllSource{name: name, search: func(context.Context, string) ([]playlist.Track, error) { return nil, nil }}
}

// allSearchTestModel returns a model whose net search runs an all-sources
// search for "blue" over YouTube, SoundCloud and one Navidrome server. The
// commands of the sources are not run: tests answer with sourceAnswer.
func allSearchTestModel(t *testing.T) Model {
	t.Helper()
	useTestBuiltins(t, idleSource("YouTube"), idleSource("SoundCloud"))
	m := keybindingTestModel()
	m.plVisible = 12
	m.providers = append(m.providers, provider.Entry{Key: "navidrome", Name: "Navidrome", Provider: searchTestProvider("Navidrome")})
	m.openProviderSearchWith(commandsTestProvider{name: "Radio"})
	m.netSearch.mode = netSearchAll
	m.netSearch.query = "blue"
	if cmd := m.handleNetSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("Enter started no search")
	}
	t.Cleanup(m.netSearch.cancel)
	return m
}

func sourceAnswer(m Model, source int, tracks []playlist.Track, err error) netSearchSourceResultsMsg {
	return netSearchSourceResultsMsg{tracks: tracks, err: err, request: m.netSearch.request, source: source, gen: m.requests.netSearch}
}

func TestNetSearchTabSwitchesSourceAndKeepsIt(t *testing.T) {
	m := keybindingTestModel()
	m.plVisible = 6
	m.layout.panelWidth = 80
	m.openProviderSearchWith(commandsTestProvider{name: "Radio"})
	tab := tea.KeyPressMsg{Code: tea.KeyTab}

	for _, want := range []struct {
		mode   netSearchMode
		header string
		body   string
	}{
		{netSearchSoundCloud, "[Search: SoundCloud]", "This searches SoundCloud."},
		{netSearchAll, "[Search: All sources]", "This searches all sources."},
		{netSearchYouTube, "[Search: YouTube]", "This searches YouTube."},
		{netSearchSoundCloud, "[Search: SoundCloud]", "Tab switches between YouTube, SoundCloud and all sources."},
	} {
		m.handleKey(tab)
		if m.netSearch.mode != want.mode {
			t.Fatalf("mode after Tab = %d, want %d", m.netSearch.mode, want.mode)
		}
		if header := ansi.Strip(m.netSearchHeaderLine()); !strings.Contains(header, want.header) {
			t.Fatalf("header = %q, want %q", header, want.header)
		}
		if body := ansi.Strip(m.renderNetSearchBody()); !strings.Contains(body, want.body) {
			t.Fatalf("body = %q, want %q", body, want.body)
		}
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.netSearch.active {
		t.Fatal("Esc left the net search open")
	}
	m.openProviderSearchWith(commandsTestProvider{name: "Radio"})
	if m.netSearch.mode != netSearchSoundCloud {
		t.Fatalf("reopened mode = %d, want the SoundCloud mode chosen before", m.netSearch.mode)
	}

	m.netSearch.loading = true
	m.handleKey(tab)
	if m.netSearch.mode != netSearchSoundCloud {
		t.Fatalf("Tab during a search changed the mode to %d", m.netSearch.mode)
	}
}

func TestNetSearchAllSourcesOrderAndSkips(t *testing.T) {
	m := Model{providers: []provider.Entry{
		{Key: "cliamp", Name: "cliamp radio", Provider: commandsTestProvider{name: "cliamp radio"}},
		{Key: "local", Name: "Local", Provider: searchTestProvider("Local")},
		{Key: "podcast", Name: "Podcasts", Provider: searchTestProvider("Podcasts")},
		{Key: "navidrome", Name: "Navidrome", Provider: searchTestProvider("Navidrome")},
		{Key: "spotify", Name: "Spotify"},
		{Key: "soundcloud", Name: "SoundCloud", Provider: searchTestProvider("SoundCloud")},
		{Key: "mixcloud", Name: "Mixcloud", Provider: searchTestProvider("Mixcloud")},
		{Key: "yt", Name: "YouTube (All)", Provider: searchTestProvider("YouTube (All)")},
		{Key: "youtube", Name: "YouTube", Provider: searchTestProvider("YouTube")},
		{Key: "ytmusic", Name: "YouTube Music", Provider: searchTestProvider("YouTube Music")},
	}}

	var names []string
	for _, src := range m.netSearchAllSources() {
		names = append(names, src.name)
	}
	if got, want := strings.Join(names, ", "), "YouTube, SoundCloud, Bandcamp, Navidrome, Mixcloud"; got != want {
		t.Fatalf("sources = %s, want %s", got, want)
	}
}

func TestNetSearchAllRunsEverySource(t *testing.T) {
	var queries []string
	useTestBuiltins(t, netSearchAllSource{name: "YouTube", search: func(_ context.Context, q string) ([]playlist.Track, error) {
		queries = append(queries, q)
		return namedTracks("yt", 2), nil
	}})

	m := keybindingTestModel()
	m.plVisible = 12
	m.providers = append(m.providers, provider.Entry{Key: "navidrome", Name: "Navidrome", Provider: searchTestProvider("Navidrome", namedTracks("nav", 1)...)})
	m.openProviderSearchWith(commandsTestProvider{name: "Radio"})
	m.netSearch.mode = netSearchAll
	m.netSearch.query = "  blue  "
	cmd := m.handleNetSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter started no search")
	}

	var msgs []tea.Msg
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			msgs = append(msgs, c())
		}
	} else {
		t.Fatal("an all-sources search should run one command per source")
	}
	for _, msg := range msgs {
		m.handleNetSearchSourceResults(msg.(netSearchSourceResultsMsg))
	}

	if strings.Join(queries, ",") != "blue" {
		t.Fatalf("YouTube searched %q, want the trimmed query", queries)
	}
	if m.netSearch.screen != netSearchResults || m.netSearch.pending != 0 || m.netSearch.loading {
		t.Fatalf("state = screen %d pending %d loading %v, want finished results", m.netSearch.screen, m.netSearch.pending, m.netSearch.loading)
	}
	var titles []string
	for _, tr := range m.netSearch.results {
		titles = append(titles, tr.Title)
	}
	if got, want := strings.Join(titles, ", "), "yt 0, yt 1, nav 0"; got != want {
		t.Fatalf("results = %s, want %s", got, want)
	}
	if m.netSearch.cancel != nil {
		t.Fatal("the search context should be released once every source answered")
	}
}

func TestNetSearchAllKeepsSourceOrderAndCursor(t *testing.T) {
	m := allSearchTestModel(t)
	if m.netSearch.pending != 3 || !m.netSearch.loading || m.netSearch.screen != netSearchInput {
		t.Fatalf("started search = pending %d loading %v screen %d", m.netSearch.pending, m.netSearch.loading, m.netSearch.screen)
	}

	// Navidrome answers first: its results open at once.
	m.handleNetSearchSourceResults(sourceAnswer(m, 2, namedTracks("nav", 2), nil))
	if m.netSearch.screen != netSearchResults || m.netSearch.loading {
		t.Fatalf("first answer left screen %d loading %v, want the results", m.netSearch.screen, m.netSearch.loading)
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.netSearch.results[m.netSearch.cursor].Title; got != "nav 1" {
		t.Fatalf("cursor on %q, want nav 1", got)
	}

	// YouTube answers later but still comes first, and the cursor stays.
	m.handleNetSearchSourceResults(sourceAnswer(m, 0, namedTracks("yt", 3), nil))
	var titles []string
	for _, tr := range m.netSearch.results {
		titles = append(titles, tr.Title)
	}
	if got, want := strings.Join(titles, ", "), "yt 0, yt 1, yt 2, nav 0, nav 1"; got != want {
		t.Fatalf("results = %s, want %s", got, want)
	}
	if got := m.netSearch.results[m.netSearch.cursor].Title; got != "nav 1" {
		t.Fatalf("cursor moved to %q, want it to stay on nav 1", got)
	}
	if got := ansi.Strip(m.netSearchHeaderLine()); !strings.Contains(got, "All Sources (1 searching)") {
		t.Fatalf("header = %q, want the count of searching sources", got)
	}

	m.handleNetSearchSourceResults(sourceAnswer(m, 1, nil, errors.New("yt-dlp: WARNING: Your yt-dlp version is older than 90 days\nERROR: [soundcloud:search] blocked\nmore detail")))
	if m.netSearch.pending != 0 {
		t.Fatalf("pending = %d, want 0", m.netSearch.pending)
	}
	if got, want := m.netSearchAllNote(), "Failed: SoundCloud ([soundcloud:search] blocked)"; got != want {
		t.Fatalf("note = %q, want %q", got, want)
	}
}

func TestNetSearchAllCursorStaysOnTopUntilMoved(t *testing.T) {
	m := allSearchTestModel(t)

	// Navidrome answers first and opens the results. YouTube then lands above
	// it, and the untouched cursor stays on the first row.
	m.handleNetSearchSourceResults(sourceAnswer(m, 2, namedTracks("nav", 2), nil))
	m.handleNetSearchSourceResults(sourceAnswer(m, 0, namedTracks("yt", 3), nil))
	if got := m.netSearch.results[m.netSearch.cursor].Title; m.netSearch.cursor != 0 || got != "yt 0" {
		t.Fatalf("cursor on %q at %d, want the first result yt 0", got, m.netSearch.cursor)
	}
}

func TestNetSearchErrText(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errors.New("navidrome: search: HTTP 503"), "navidrome: search: HTTP 503"},
		{errors.New("first line\nsecond line"), "first line"},
		{errors.New("yt-dlp: WARNING: old\nERROR: [youtube:search] Forbidden"), "[youtube:search] Forbidden"},
		{fmt.Errorf("yt-dlp: resolve x: %w", context.DeadlineExceeded), "timed out"},
		{playlist.ErrNeedsAuth, "sign-in required"},
	}
	for _, tt := range tests {
		if got := netSearchErrText(tt.err); got != tt.want {
			t.Errorf("netSearchErrText(%q) = %q, want %q", tt.err, got, tt.want)
		}
	}

	sec := netSearchSection{name: "Navidrome", err: errors.New("navidrome: search: HTTP 503"), done: true}
	if got, want := sec.errText(), "search: HTTP 503"; got != want {
		t.Errorf("errText = %q, want %q without the repeated source name", got, want)
	}
}

func TestNetSearchAllIgnoresStaleAnswers(t *testing.T) {
	m := allSearchTestModel(t)
	stale := sourceAnswer(m, 0, namedTracks("yt", 1), nil)
	stale.gen--
	m.handleNetSearchSourceResults(stale)
	if m.netSearch.pending != 3 || len(m.netSearch.results) != 0 {
		t.Fatalf("an answer of an older search changed pending %d results %d", m.netSearch.pending, len(m.netSearch.results))
	}

	first := sourceAnswer(m, 0, namedTracks("yt", 1), nil)
	m.handleNetSearchSourceResults(first)
	m.handleNetSearchSourceResults(first)
	if m.netSearch.pending != 2 {
		t.Fatalf("a repeated answer counted twice: pending = %d, want 2", m.netSearch.pending)
	}

	// Esc returns to the query. A source that answers after that must not
	// reopen the results.
	late := sourceAnswer(m, 2, namedTracks("nav", 1), nil)
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.netSearch.screen != netSearchInput || m.netSearch.loading || m.netSearch.cancel != nil {
		t.Fatalf("Esc left screen %d loading %v, want the idle query", m.netSearch.screen, m.netSearch.loading)
	}
	m.handleNetSearchSourceResults(late)
	if m.netSearch.screen != netSearchInput || len(m.netSearch.results) != 0 {
		t.Fatal("a late answer reopened the results after Esc")
	}
}

func TestNetSearchAllWithoutResultsStaysOnQuery(t *testing.T) {
	m := allSearchTestModel(t)
	m.handleNetSearchSourceResults(sourceAnswer(m, 0, nil, fmt.Errorf("yt-dlp: %w", context.DeadlineExceeded)))
	m.handleNetSearchSourceResults(sourceAnswer(m, 1, nil, nil))
	m.handleNetSearchSourceResults(sourceAnswer(m, 2, []playlist.Track{albumResult("An Album")}, nil))

	if m.netSearch.screen != netSearchInput || m.netSearch.loading {
		t.Fatalf("screen %d loading %v, want the idle query", m.netSearch.screen, m.netSearch.loading)
	}
	if m.netSearch.err != "No results found" {
		t.Fatalf("err = %q, want No results found", m.netSearch.err)
	}
	body := ansi.Strip(m.renderNetSearchBody())
	for _, want := range []string{"No results found", "YouTube: timed out", "Nothing on SoundCloud, Navidrome"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want %q", body, want)
		}
	}

	// A new source choice drops the reasons of the old search.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if body := ansi.Strip(m.renderNetSearchBody()); strings.Contains(body, "timed out") {
		t.Fatalf("body after Tab = %q, want no stale reasons", body)
	}
}

func TestPlayableResultsDropsAlbumsAndCaps(t *testing.T) {
	tracks := append([]playlist.Track{albumResult("Album")}, namedTracks("t", netSearchAllLimit+3)...)
	got := playableResults(tracks)
	if len(got) != netSearchAllLimit {
		t.Fatalf("kept %d results, want %d", len(got), netSearchAllLimit)
	}
	for _, tr := range got {
		if tr.IsAlbum() {
			t.Fatalf("kept album placeholder %q", tr.Title)
		}
	}
}

func TestNetSearchAllRendersSectionsAndNote(t *testing.T) {
	m := newLayoutTestModel(60, 16)
	m.netSearch = netSearchState{
		active: true,
		screen: netSearchResults,
		mode:   netSearchAll,
		sections: []netSearchSection{
			{name: "YouTube", tracks: namedTracks("yt", 4), done: true},
			{name: "SoundCloud"},
			{name: "Navidrome", tracks: namedTracks("nav", 4), done: true},
		},
		pending: 1,
	}
	m.rebuildNetSearchAllResults()
	m.recomputeLayout()
	budget := m.effectivePlaylistVisible()
	if budget < 4 {
		t.Fatalf("body rows = %d, want at least 4", budget)
	}

	lines := strings.Split(ansi.Strip(m.renderNetSearchBody()), "\n")
	if len(lines) != budget {
		t.Fatalf("body rows = %d, want %d", len(lines), budget)
	}
	if !strings.Contains(lines[0], "── YouTube (4)") {
		t.Fatalf("first row = %q, want the YouTube separator", lines[0])
	}
	if got := lines[len(lines)-1]; !strings.Contains(got, "Searching SoundCloud...") {
		t.Fatalf("last row = %q, want the searching note", got)
	}

	// The cursor on the last result stays in view although separators and
	// the note take rows.
	m.netSearch.cursor = len(m.netSearch.results) - 1
	m.netSearchResultsMaybeAdjustScroll(m.netSearchResultsVisible())
	rows := sectionRowsToCursor(m.netSearch.results, m.netSearch.scroll, m.netSearch.cursor, m.netSearchResultSection)
	if rows > m.netSearchResultsVisible() {
		t.Fatalf("cursor sits %d rows below scroll, the list has %d rows", rows, m.netSearchResultsVisible())
	}
	body := ansi.Strip(m.renderNetSearchBody())
	if !strings.Contains(body, "> nav - nav 3") {
		t.Fatalf("body = %q, want the selected last result", body)
	}
	if !strings.Contains(body, "── Navidrome (4)") {
		t.Fatalf("body = %q, want the Navidrome separator", body)
	}
}
