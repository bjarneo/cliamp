package model

import (
	"context"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type catalogTestProvider struct {
	commandsTestProvider
	mu             sync.Mutex
	generation     uint64
	results        []playlist.PlaylistInfo
	started        chan struct{}
	release        chan struct{}
	clearCalls     int
	playlistsCalls int
}

func (p *catalogTestProvider) SearchCatalog(string) (int, error) {
	p.mu.Lock()
	gen := p.generation
	p.mu.Unlock()
	if p.started != nil {
		close(p.started)
		<-p.release
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if gen != p.generation {
		return 0, nil
	}
	p.results = []playlist.PlaylistInfo{{ID: "s:late", Name: "Late result"}}
	return len(p.results), nil
}

func (p *catalogTestProvider) ClearSearch() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	p.clearCalls++
	p.results = nil
}

func (p *catalogTestProvider) IsSearching() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.results != nil
}

func (p *catalogTestProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.playlistsCalls++
	if p.results != nil {
		return p.results, nil
	}
	return p.commandsTestProvider.Playlists()
}

func (*catalogTestProvider) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return nil, nil
}

func (*catalogTestProvider) LoadCatalogPage(int, int) (int, error) { return 0, nil }

type sectionedCatalogTestProvider struct{ *catalogTestProvider }

func (sectionedCatalogTestProvider) IDPrefix(id string) string {
	prefix, _, _ := strings.Cut(id, ":")
	return prefix
}

func (sectionedCatalogTestProvider) IsFavoritableID(id string) bool {
	return strings.HasPrefix(id, "s:") || strings.HasPrefix(id, "c:")
}

func (sectionedCatalogTestProvider) SectionTitle(string) string { return "Shows" }

func TestProviderSearchRenderingUsesCatalogCapability(t *testing.T) {
	p := &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Catalog"}}
	sectioned := sectionedCatalogTestProvider{p}
	sectionedOnly := struct {
		playlist.Provider
		provider.SectionedList
	}{commandsTestProvider{name: "Sectioned"}, sectioned}
	for _, prov := range []playlist.Provider{p, sectioned, sectionedOnly} {
		for _, query := range []string{"", "science"} {
			for _, loading := range []bool{false, true} {
				m := Model{provider: prov, plVisible: 6, provLoading: loading,
					provSearch: provSearchState{active: true, query: query}}
				body := stripAnsi(m.renderProviderList())
				want := "Type to filter"
				if query != "" {
					want = "No matches"
				}
				if _, ok := prov.(provider.CatalogSearcher); ok {
					want = "Enter to search"
				}
				if !strings.Contains(body, "/ "+query+"_") || !strings.Contains(body, want) || strings.Contains(body, "station") {
					t.Fatalf("%T query=%q loading=%t: body = %q, want visible input and %q", prov, query, loading, body, want)
				}
			}
		}
	}
	m := Model{provider: p, plVisible: 6}
	if body := stripAnsi(m.renderProviderList()); !strings.Contains(body, " / ") || strings.Contains(body, "Ctrl+F") {
		t.Fatalf("empty catalog hint = %q, want slash search", body)
	}
	m.catalogBatch.loading = true
	if body := stripAnsi(m.renderProviderList()); !strings.Contains(body, "Loading more entries") || strings.Contains(body, "station") {
		t.Fatalf("catalog loading body = %q", body)
	}
}

func TestCatalogSearchCancelBeforeCompletion(t *testing.T) {
	for _, cancel := range []string{"pane escape", "input escape", "empty query"} {
		t.Run(cancel, func(t *testing.T) {
			p := &catalogTestProvider{
				commandsTestProvider: commandsTestProvider{name: "Catalog", lists: []playlist.PlaylistInfo{{ID: "c:keep", Name: "Keep"}}},
				started:              make(chan struct{}), release: make(chan struct{}),
			}
			release := sync.OnceFunc(func() { close(p.release) })
			t.Cleanup(release)
			m := keybindingTestModel()
			m.provider, m.providerLists, m.focus = p, p.lists, focusProvider
			m.catalogBatch.loading = true
			m.handleKey(tea.KeyPressMsg{Text: "/"})
			m.handlePaste("science")
			cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil || !m.provSearch.loading || m.catalogBatch.loading {
				t.Fatal("search did not take over the catalog request")
			}
			gen := m.requests.catalog
			result := make(chan tea.Msg, 1)
			go func() { result <- cmd() }()
			<-p.started
			if next := m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown}); next != nil || m.requests.catalog != gen {
				t.Fatal("scrolling superseded the pending search with a catalog page")
			}
			key := tea.KeyPressMsg{Code: tea.KeyEscape}
			if cancel != "pane escape" {
				m.handleKey(tea.KeyPressMsg{Text: "/"})
				if cancel == "empty query" {
					key.Code = tea.KeyEnter
				}
			}
			restore := m.handleKey(key)
			if restore == nil || p.playlistsCalls != 0 {
				t.Fatal("cancel before results must restore playlists asynchronously")
			}
			if p.clearCalls != 1 || m.requests.catalog == gen || !m.provLoading || m.provSearch.loading || m.catalogBatch.loading {
				t.Fatalf("cancel state: clears=%d search=%+v batch=%+v", p.clearCalls, m.provSearch, m.catalogBatch)
			}
			release()
			updated, next := m.Update(<-result)
			m = updated.(Model)
			if next != nil || p.IsSearching() || m.status.text != "" || len(m.providerLists) != 1 || m.providerLists[0].ID != "c:keep" {
				t.Fatalf("late search changed catalog: searching=%t status=%q lists=%+v", p.IsSearching(), m.status.text, m.providerLists)
			}
			updated, next = m.Update(restore())
			m = updated.(Model)
			if next == nil || !m.catalogBatch.loading || m.provLoading {
				t.Fatal("cancel did not restart unfinished catalog loading")
			}
		})
	}
}

func TestCatalogCancelDuringInitialLoadRestoresLists(t *testing.T) {
	p := &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Catalog", lists: []playlist.PlaylistInfo{{ID: "f:subscription"}}}}
	m := Model{provider: p, focus: focusProvider, provSearch: provSearchState{active: true}, catalogBatch: catalogBatchState{loading: true}}
	oldGeneration := m.requests.catalog
	restore := m.handleCatalogSearchKey(tea.KeyPressMsg{Code: tea.KeyEscape}, p)
	if restore == nil {
		t.Fatal("cancel did not schedule local playlist restoration")
	}
	next, cmd := m.Update(restore())
	m = next.(Model)
	if len(m.providerLists) != 1 || m.providerLists[0].ID != "f:subscription" || cmd == nil || !m.catalogBatch.loading {
		t.Fatal("cancel lost subscriptions or did not restart the catalog")
	}
	next, _ = m.Update(catalogBatchMsg{providerName: p.Name(), gen: oldGeneration, added: 100})
	m = next.(Model)
	if m.catalogBatch.offset != 0 || !m.catalogBatch.loading {
		t.Fatal("canceled initial page changed the new catalog request")
	}
}

func TestCatalogSearchEmptyResultsAndRestore(t *testing.T) {
	p := &catalogTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Catalog", lists: []playlist.PlaylistInfo{{ID: "c:keep"}}},
		results:              []playlist.PlaylistInfo{},
	}
	m := Model{provider: p, provLoading: true, provSearch: provSearchState{loading: true}}
	updated, _ := m.Update(catalogSearchMsg{providerName: p.Name()})
	m = updated.(Model)
	if m.status.text != "No results found" || m.status.kind != feedbackWarning || m.provLoading || m.provSearch.loading {
		t.Fatalf("empty search state = status:%+v search:%+v", m.status, m.provSearch)
	}
	reads := p.playlistsCalls
	cmd := m.restoreCatalog(p)
	if cmd == nil || p.playlistsCalls != reads || p.IsSearching() || !m.provLoading {
		t.Fatal("restore must clear immediately and fetch playlists asynchronously")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.provLoading || len(m.providerLists) != 1 || m.providerLists[0].ID != "c:keep" {
		t.Fatalf("restored lists = %+v", m.providerLists)
	}
}

func TestNewCatalogSearchSupersedesRestore(t *testing.T) {
	p := &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Catalog"}, results: []playlist.PlaylistInfo{}}
	m := Model{provider: p}
	stale := m.restoreCatalog(p)()
	m.provSearch.query = "science"
	cmd := m.handleCatalogSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter}, p)
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	updated, next := m.Update(stale)
	m = updated.(Model)
	if next != nil || len(m.providerLists) != 1 || m.providerLists[0].ID != "s:late" {
		t.Fatalf("stale restoration overwrote new search results: %+v", m.providerLists)
	}
}

func TestCatalogRefreshDoesNotLoadPagesDuringSearch(t *testing.T) {
	for _, state := range []string{"typing", "pending", "results"} {
		t.Run(state, func(t *testing.T) {
			p := &catalogTestProvider{commandsTestProvider: commandsTestProvider{name: "Catalog"}}
			m := Model{provider: p, provSearch: provSearchState{active: state == "typing", loading: state == "pending"}}
			if state == "results" {
				p.results = []playlist.PlaylistInfo{}
			}
			updated, cmd := m.Update(playlistsLoadedMsg{providerName: p.Name()})
			m = updated.(Model)
			if cmd != nil || m.catalogBatch.loading || m.requests.catalog != 0 {
				t.Fatal("playlist refresh started catalog pagination during search")
			}
			if m.provLoading != m.provSearch.loading {
				t.Fatal("playlist refresh cleared the pending search's loading state")
			}
		})
	}
}
