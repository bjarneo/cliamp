package model

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type pagedAlbumBrowseProvider struct {
	commandsTestProvider
	albums []provider.AlbumInfo
	calls  []int
}

func (p *pagedAlbumBrowseProvider) AlbumList(_ string, offset, size int) ([]provider.AlbumInfo, error) {
	p.calls = append(p.calls, offset)
	if offset >= len(p.albums) {
		return nil, nil
	}
	end := min(len(p.albums), offset+size)
	return append([]provider.AlbumInfo(nil), p.albums[offset:end]...), nil
}

func (*pagedAlbumBrowseProvider) AlbumSortTypes() []provider.SortType { return nil }
func (*pagedAlbumBrowseProvider) DefaultAlbumSort() string            { return "" }

func TestNavAlbumSearchLoadsRemainingPages(t *testing.T) {
	albums := make([]provider.AlbumInfo, navAlbumPageSize*2+1)
	for i := range navAlbumPageSize * 2 {
		albums[i] = provider.AlbumInfo{ID: fmt.Sprintf("album-%03d", i), Name: fmt.Sprintf("Album %03d", i)}
	}
	albums[navAlbumPageSize*2] = provider.AlbumInfo{ID: "target", Name: "Needle Album"}

	prov := &pagedAlbumBrowseProvider{
		commandsTestProvider: commandsTestProvider{name: "Navidrome"},
		albums:               albums,
	}
	m := Model{navBrowser: navBrowserState{
		prov:      prov,
		visible:   true,
		mode:      navBrowseModeByAlbum,
		screen:    navBrowseScreenList,
		albums:    append([]provider.AlbumInfo(nil), albums[:navAlbumPageSize]...),
		albumDone: false,
	}}

	cmd := m.handleNavBrowserKey(tea.KeyPressMsg{Text: "/"})
	if cmd == nil {
		t.Fatal("starting album search did not request the remaining pages")
	}
	m.handlePaste("needle")
	if len(m.navBrowser.searchIdx) != 0 {
		t.Fatalf("search matched %d albums before the remaining page loaded, want 0", len(m.navBrowser.searchIdx))
	}

	next, _ := m.Update(cmd())
	m = next.(Model)
	if len(m.navBrowser.albums) != len(albums) {
		t.Fatalf("loaded albums = %d, want %d", len(m.navBrowser.albums), len(albums))
	}
	if len(m.navBrowser.searchIdx) != 1 || m.navBrowser.albums[m.navBrowser.searchIdx[0]].ID != "target" {
		t.Fatalf("search results = %v, want target album", m.navBrowser.searchIdx)
	}
	wantCalls := []int{navAlbumPageSize, navAlbumPageSize * 2}
	if len(prov.calls) != len(wantCalls) || prov.calls[0] != wantCalls[0] || prov.calls[1] != wantCalls[1] {
		t.Fatalf("album page offsets = %v, want %v", prov.calls, wantCalls)
	}
}

var _ playlist.Provider = (*pagedAlbumBrowseProvider)(nil)
var _ provider.AlbumBrowser = (*pagedAlbumBrowseProvider)(nil)
