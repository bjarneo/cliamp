package model

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// pagerProv is a stub provider that serves tracks one page at a time.
type pagerProv struct {
	name  string
	pages [][]playlist.Track
}

func (p *pagerProv) Name() string { return p.name }

func (p *pagerProv) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{{ID: "list", Name: "List"}}, nil
}

func (p *pagerProv) Tracks(string) ([]playlist.Track, error) {
	var all []playlist.Track
	for _, page := range p.pages {
		all = append(all, page...)
	}
	return all, nil
}

func (p *pagerProv) TracksPage(_ string, offset int) ([]playlist.Track, int, error) {
	idx := offset
	if idx >= len(p.pages) {
		return nil, 0, nil
	}
	next := idx + 1
	if next >= len(p.pages) {
		next = 0
	}
	return p.pages[idx], next, nil
}

func pageOf(paths ...string) []playlist.Track {
	tracks := make([]playlist.Track, 0, len(paths))
	for _, path := range paths {
		tracks = append(tracks, playlist.Track{Path: path})
	}
	return tracks
}

func newPagingModel(prov *pagerProv) Model {
	m := Model{
		player:        &playbackFakeEngine{},
		playlist:      playlist.New(),
		provider:      prov,
		providerLists: []playlist.PlaylistInfo{{ID: "list", Name: "List"}},
		provLoading:   true,
	}
	m.requests.tracks = 1
	return m
}

// The provider pane gates Enter on !provLoading, so holding it across the whole
// page chain locks the user out of re-entering a list that is still filling.
func TestFirstPageClearsProvLoading(t *testing.T) {
	prov := &pagerProv{name: "Pager", pages: [][]playlist.Track{
		pageOf("a", "b"),
		pageOf("c", "d"),
	}}
	m := newPagingModel(prov)

	updated, cmd := m.Update(tracksLoadedMsg{
		tracks: prov.pages[0], playlistID: "list", providerName: "Pager", offset: 0, next: 1, gen: 1,
	})
	m = updated.(Model)

	if m.provLoading {
		t.Error("provLoading still set after the first page; Enter stays blocked while loading")
	}
	if cmd == nil {
		t.Fatal("no command returned for the next page; the chain stopped early")
	}
	if got := m.playlist.Len(); got != 2 {
		t.Errorf("playlist has %d tracks after page 1, want 2", got)
	}
}

func TestLaterPagesAppendWithoutReplacing(t *testing.T) {
	prov := &pagerProv{name: "Pager", pages: [][]playlist.Track{
		pageOf("a", "b"),
		pageOf("c", "d"),
	}}
	m := newPagingModel(prov)

	updated, _ := m.Update(tracksLoadedMsg{
		tracks: prov.pages[0], playlistID: "list", providerName: "Pager", offset: 0, next: 1, gen: 1,
	})
	updated, cmd := updated.(Model).Update(tracksLoadedMsg{
		tracks: prov.pages[1], playlistID: "list", providerName: "Pager", offset: 1, next: 0, gen: 1,
	})
	m = updated.(Model)

	if cmd != nil {
		t.Error("a command was returned after the terminal page; the chain should stop")
	}
	if got := m.playlist.Len(); got != 4 {
		t.Errorf("playlist has %d tracks after both pages, want 4 (later pages must append)", got)
	}
	if m.provLoading {
		t.Error("provLoading set after the terminal page")
	}
}

// A superseded chain's straggler page must not append to the list the new chain
// installed. phase0_test covers the offset == 0 shape; this guards the append.
func TestStaleGenPageDoesNotAppend(t *testing.T) {
	prov := &pagerProv{name: "Pager", pages: [][]playlist.Track{
		pageOf("a", "b"),
		pageOf("c", "d"),
	}}
	m := newPagingModel(prov)

	updated, _ := m.Update(tracksLoadedMsg{
		tracks: prov.pages[0], playlistID: "list", providerName: "Pager", offset: 0, next: 1, gen: 1,
	})
	m = updated.(Model)
	m.requests.tracks = 2 // the user re-entered; chain 1 is now dead

	updated, cmd := m.Update(tracksLoadedMsg{
		tracks: prov.pages[1], playlistID: "list", providerName: "Pager", offset: 1, next: 0, gen: 1,
	})
	m = updated.(Model)

	if got := m.playlist.Len(); got != 2 {
		t.Errorf("playlist has %d tracks, want 2: a stale page appended", got)
	}
	if cmd != nil {
		t.Error("a stale page dispatched a follow-up request")
	}
}

// toggleAlbumHeadersManual pins the user's choice so later Adds do not re-run
// the cohesion heuristic over them. Rebuilding the header from the whole queue
// on each page cleared that pin, silently undoing a mid-load toggle.
func TestPagesDoNotUnpinTheAlbumHeader(t *testing.T) {
	prov := &pagerProv{name: "Pager", pages: [][]playlist.Track{
		pageOf("a", "b"),
		pageOf("c", "d"),
	}}
	m := newPagingModel(prov)

	updated, _ := m.Update(tracksLoadedMsg{
		tracks: prov.pages[0], playlistID: "list", providerName: "Pager", offset: 0, next: 1, gen: 1,
	})
	m = updated.(Model)

	m.toggleAlbumHeadersManual()
	pinned := m.showAlbumHeaders

	updated, _ = m.Update(tracksLoadedMsg{
		tracks: prov.pages[1], playlistID: "list", providerName: "Pager", offset: 1, next: 0, gen: 1,
	})
	m = updated.(Model)

	if !m.headerManual {
		t.Error("a later page cleared the manual header pin")
	}
	if m.showAlbumHeaders != pinned {
		t.Errorf("album headers = %v after a later page, want %v", m.showAlbumHeaders, pinned)
	}
}

// Add mixes a new page into the upcoming shuffle order, so a preload armed for
// the old order is no longer the next track. Gapless swaps on the audio thread
// and the model then names the track from playlist.Next(), so keeping a stale
// preload would play one track while announcing another.
func TestPageDropsAStalePreload(t *testing.T) {
	prov := &pagerProv{name: "Pager", pages: [][]playlist.Track{
		pageOf("a", "b"), pageOf("c", "d"),
	}}
	m := newPagingModel(prov)
	engine := m.player.(*playbackFakeEngine)
	engine.hasPreload = true

	updated, _ := m.Update(tracksLoadedMsg{
		tracks: prov.pages[0], playlistID: "list", providerName: "Pager", offset: 0, next: 1, gen: 1,
	})
	m = updated.(Model)
	before := engine.clearPreloadCalls
	m.preloading = true

	updated, _ = m.Update(tracksLoadedMsg{
		tracks: prov.pages[1], playlistID: "list", providerName: "Pager", offset: 1, next: 0, gen: 1,
	})
	m = updated.(Model)

	if engine.clearPreloadCalls == before {
		t.Error("appending a page left a stale preload armed")
	}
	if m.preloading {
		t.Error("preloading flag still set; the tick loop will not re-arm")
	}
}
