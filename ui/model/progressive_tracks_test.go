package model

import (
	"errors"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

var errStubLoad = errors.New("stub load failure")

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

func TestPagingSuppressesPreloadUntilTheOrderSettles(t *testing.T) {
	prov := &pagerProv{name: "Pager", pages: [][]playlist.Track{
		pageOf("a", "b"), pageOf("c", "d"),
	}}
	m := newPagingModel(prov)

	updated, _ := m.Update(tracksLoadedMsg{
		tracks: prov.pages[0], playlistID: "list", providerName: "Pager", offset: 0, next: 1, gen: 1,
	})
	m = updated.(Model)
	if !m.tracksPaging {
		t.Error("tracksPaging not set while pages are still in flight")
	}

	updated, _ = m.Update(tracksLoadedMsg{
		tracks: prov.pages[1], playlistID: "list", providerName: "Pager", offset: 1, next: 0, gen: 1,
	})
	if updated.(Model).tracksPaging {
		t.Error("tracksPaging still set after the terminal page")
	}
}

func TestPagingFlagClearsOnError(t *testing.T) {
	prov := &pagerProv{name: "Pager", pages: [][]playlist.Track{pageOf("a")}}
	m := newPagingModel(prov)
	m.tracksPaging = true

	updated, _ := m.Update(tracksLoadedMsg{
		playlistID: "list", providerName: "Pager", offset: 1, gen: 1, err: errStubLoad,
	})
	if updated.(Model).tracksPaging {
		t.Error("a failed page left preloading suppressed for the session")
	}
}

// A superseded chain never delivers a terminal page, so the flag has to be
// reset where the next request is dispatched, not only where pages arrive.
func TestProviderNavResetClearsThePagingFlag(t *testing.T) {
	prov := &pagerProv{name: "Pager", pages: [][]playlist.Track{pageOf("a")}}
	m := newPagingModel(prov)
	m.tracksPaging = true

	m.resetProviderNav()

	if m.tracksPaging {
		t.Error("switching provider left preloading suppressed for the session")
	}
}

// The tick loop is the main arming path, and it only fires into an empty slot.
// While pages are landing the order keeps changing, so it must be held off;
// preloadNext marks m.preloading before returning its command, which is the
// observable side effect of the guard letting it through.
func TestTickDoesNotArmPreloadWhilePaging(t *testing.T) {
	prov := &pagerProv{name: "Pager", pages: [][]playlist.Track{pageOf("a", "b")}}
	m := newPagingModel(prov)
	m.playlist.Add(pageOf("a", "b")...)
	m.player.(*playbackFakeEngine).playing = true

	m.tracksPaging = true
	updated, _ := m.Update(tickMsg(time.Now()))
	if updated.(Model).preloading {
		t.Error("a tick armed a preload while pages were still landing")
	}

	m.tracksPaging = false
	updated, _ = m.Update(tickMsg(time.Now()))
	if !updated.(Model).preloading {
		t.Fatal("a tick did not arm a preload once paging finished; the test proves nothing")
	}
}
