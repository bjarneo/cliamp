package mass

import (
	"context"
	"fmt"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
)

// searchLimit is how many results of each kind a search returns.
const searchLimit = 100

// searchTracksID is the playlist id of the tracks a search found.
const searchTracksID = pathPrefix + "search/tracks"

// librarySearch holds the results of the last SearchCatalog call.
type librarySearch struct {
	lists  []playlist.PlaylistInfo
	tracks []playlist.Track
}

// SearchCatalog searches the whole Music Assistant library on the server, not
// only the rows loaded in cliamp. While the results stand, the provider pane
// lists the matching tracks as one row, then the matching artists, albums and
// playlists. Implements provider.CatalogSearcher.
func (p *Provider) SearchCatalog(query string) (int, error) {
	p.mu.Lock()
	p.searchGen++
	gen := p.searchGen
	p.mu.Unlock()

	var res struct {
		Artists   []massTrack    `json:"artists"`
		Albums    []massAlbum    `json:"albums"`
		Tracks    []massTrack    `json:"tracks"`
		Playlists []massPlaylist `json:"playlists"`
	}
	if err := p.rpcRequest(context.Background(), cmdSearch, map[string]any{
		"search_query": strings.TrimSpace(query),
		"media_types":  []string{"artist", "album", "track", "playlist"},
		"limit":        searchLimit,
		// The library only: with the connected services included, a common
		// word returns a hundred same-named artists.
		"library_only": true,
	}, &res); err != nil {
		return 0, err
	}

	s := &librarySearch{}
	for _, t := range res.Tracks {
		if t.Name != "" && t.URI != "" {
			s.tracks = append(s.tracks, p.trackFromMass(t))
		}
	}
	if n := len(s.tracks); n > 0 {
		s.lists = append(s.lists, playlist.PlaylistInfo{
			ID: searchTracksID, Name: fmt.Sprintf("All %d matching tracks", n), TrackCount: n, Section: "Tracks",
		})
	}
	for _, a := range res.Artists {
		if a.Name != "" && a.URI != "" {
			s.lists = append(s.lists, playlist.PlaylistInfo{ID: a.URI, Name: a.Name, Section: "Artists"})
		}
	}
	for _, a := range res.Albums {
		if a.Name == "" || a.URI == "" {
			continue
		}
		name := a.Name
		if artist := a.artistStr(); artist != "" {
			name += " — " + artist
		}
		s.lists = append(s.lists, playlist.PlaylistInfo{ID: a.URI, Name: name, TrackCount: a.TrackCount, Section: "Albums"})
	}
	for _, pl := range res.Playlists {
		if pl.Name != "" && pl.URI != "" {
			s.lists = append(s.lists, playlist.PlaylistInfo{ID: pl.URI, Name: pl.Name, TrackCount: pl.TrackCount, Section: "Playlists"})
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if gen != p.searchGen {
		return 0, nil // cleared or replaced while the request ran
	}
	p.search = s
	return len(s.lists), nil
}

// ClearSearch returns the provider pane to the library. Implements
// provider.CatalogSearcher.
func (p *Provider) ClearSearch() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.searchGen++
	p.search = nil
}

// IsSearching reports whether search results replace the library. Implements
// provider.CatalogSearcher.
func (p *Provider) IsSearching() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.search != nil
}

// searchLists returns the result rows while a search stands.
func (p *Provider) searchLists() ([]playlist.PlaylistInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.search == nil {
		return nil, false
	}
	return p.search.lists, true
}

// searchTracks returns the tracks the last search found.
func (p *Provider) searchTracks() ([]playlist.Track, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.search == nil {
		return nil, fmt.Errorf("mass: no search results")
	}
	return p.search.tracks, nil
}
