package spotify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
)

// devModeSearchLimit is the largest per-request limit /v1/search accepts for an
// app in Spotify's Development Mode. SearchTracks uses it for every request so
// personal client IDs never need a rejected probe before pagination starts.
const devModeSearchLimit = 10

func isInvalidLimit(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.status == http.StatusBadRequest && strings.Contains(apiErr.message, "Invalid limit")
}

// spotifySearchPage is one page of /v1/search results.
type spotifySearchPage struct {
	Albums struct {
		Items []*spotifyAlbumItem `json:"items"`
	} `json:"albums"`
	Tracks struct {
		Items []*spotifyItem `json:"items"`
	} `json:"tracks"`
	Episodes struct {
		Items []*spotifyItem `json:"items"`
	} `json:"episodes"`
}

// searchPage runs a single /v1/search request.
//
// No market parameter: when the request carries a user OAuth token, Spotify
// implicitly scopes results to the account's country.
func (p *SpotifyProvider) searchPage(ctx context.Context, query string, limit, offset int) (*spotifySearchPage, error) {
	q := url.Values{
		"q":     {query},
		"type":  {"album,track,episode"},
		"limit": {strconv.Itoa(limit)},
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}

	resp, err := p.webAPI(ctx, "GET", "/v1/search", q)
	if err != nil {
		return nil, err
	}

	var page spotifySearchPage
	if err := decodeBody(resp, &page); err != nil {
		return nil, fmt.Errorf("spotify: parse search: %w", err)
	}
	return &page, nil
}

// searchPaged collects up to limit results in pages of devModeSearchLimit, for
// apps that cannot ask for more in one go. Any page failure aborts the search so
// callers never mistake partial results for a complete response.
func (p *SpotifyProvider) searchPaged(ctx context.Context, query string, limit int) (*spotifySearchPage, error) {
	combined := &spotifySearchPage{}
	for offset := 0; offset < limit; offset += devModeSearchLimit {
		size := min(devModeSearchLimit, limit-offset)
		page, err := p.searchPage(ctx, query, size, offset)
		if err != nil {
			return nil, fmt.Errorf("page at offset %d: %w", offset, err)
		}
		combined.Albums.Items = append(combined.Albums.Items, page.Albums.Items...)
		combined.Tracks.Items = append(combined.Tracks.Items, page.Tracks.Items...)
		combined.Episodes.Items = append(combined.Episodes.Items, page.Episodes.Items...)
		// Every result kind exhausted, so further pages are empty.
		if len(page.Albums.Items) < size && len(page.Tracks.Items) < size && len(page.Episodes.Items) < size {
			break
		}
	}
	return combined, nil
}

// SearchTracks searches Spotify for albums, tracks and podcast episodes,
// returning up to limit results of each. Episodes (e.g. podcasts) are routed
// through their spotify:episode: URI so they play correctly.
// limit is clamped to Spotify's accepted range of 1..50.
//
// Album hits lead the results as album placeholders (playlist.Track.IsAlbum),
// because a query is usually an artist or record name and the album is the
// more useful answer than whichever of its tracks Spotify ranks highest. They
// are not playable as-is: the caller expands the chosen one with AlbumTracks.
//
// Apps in Development Mode cap /v1/search at devModeSearchLimit results per
// request, so larger result sets always use offset pagination.
func (p *SpotifyProvider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	if limit < 1 {
		limit = 1
	} else if limit > 50 {
		limit = 50
	}

	var result *spotifySearchPage
	var err error
	if allowsFullSearchPage(p.clientID) {
		// Preserve one-request searches for the built-in clients, which are not
		// in Development Mode. Fall back to Development Mode pages if Spotify
		// applies the cap to them later. A 429 is deliberately not a trigger:
		// paging would issue more requests, and webAPI already backs off.
		result, err = p.searchPage(ctx, query, limit, 0)
		if err != nil && isInvalidLimit(err) && limit > devModeSearchLimit {
			result, err = p.searchPaged(ctx, query, limit)
		}
	} else {
		result, err = p.searchPaged(ctx, query, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("spotify: search: %w", err)
	}

	var tracks []playlist.Track
	for _, a := range result.Albums.Items {
		if a == nil || a.ID == "" {
			continue // skip null/unavailable results
		}
		tracks = append(tracks, albumFromItem(a))
	}
	for _, items := range [][]*spotifyItem{result.Tracks.Items, result.Episodes.Items} {
		for _, t := range items {
			if t == nil || t.ID == "" {
				continue // skip null/unavailable results
			}
			tracks = append(tracks, trackFromItem(t))
		}
	}
	return tracks, nil
}
