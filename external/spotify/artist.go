package spotify

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface check.
var _ provider.ArtistDetailLoader = (*SpotifyProvider)(nil)

// Artist-detail flow bounds. artistDetailTimeout wraps the whole multi-call
// flow; the page sizes respect the per-endpoint maximums (10 for artist
// albums and search, 50 for album tracks); libraryContainsChunk is the URI
// cap on /v1/me/library/contains.
const (
	artistDetailTimeout  = 30 * time.Second
	artistAlbumsPageSize = 10
	artistSearchPageSize = 10
	artistSearchPages    = 3
	libraryContainsChunk = 40
	popularTrackCount    = 10
)

// spotifyArtistFull is the full Artist object from /v1/artists/{id}. genres
// and followers are deprecated fields that are still returned today
// (verified September 2026); do not assume longevity.
type spotifyArtistFull struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Genres    []string `json:"genres"`
	Followers struct {
		Total int `json:"total"`
	} `json:"followers"`
}

// artistPoolTrack is one candidate for the artist's synthesized popular
// pool: the raw item plus the album it came from (zero value for
// search-sourced candidates).
type artistPoolTrack struct {
	item  *spotifyItem
	album spotifyAlbum
}

// ArtistDetail returns the artist's profile: header info, the full
// album+singles discography, and a synthesized popular pool — the
// top-tracks endpoint was removed from the API, so popularity is rebuilt
// from the discography's tracks plus artist-filtered search results. The
// header and discography are load-bearing (their failure fails the call);
// popular-pool sources and the liked-contains marks are best-effort.
// Implements provider.ArtistDetailLoader.
func (p *SpotifyProvider) ArtistDetail(artistID string) (provider.ArtistDetail, error) {
	if err := p.ensureSession(); err != nil {
		return provider.ArtistDetail{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), artistDetailTimeout)
	defer cancel()

	header, err := p.fetchArtistHeader(ctx, artistID)
	if err != nil {
		return provider.ArtistDetail{}, fmt.Errorf("spotify: artist detail: %w", err)
	}

	albums, err := p.fetchArtistDiscography(ctx, artistID)
	if err != nil {
		return provider.ArtistDetail{}, fmt.Errorf("spotify: artist detail: %w", err)
	}
	discography := make([]provider.AlbumInfo, len(albums))
	for i, a := range albums {
		discography[i] = albumFromSpotify(a)
	}

	pool := p.artistPopularPool(ctx, artistID, header.Name, albums)
	// Popularity descending, stable: equal scores keep pool order and
	// zero-popularity candidates sink to the end in pool order.
	sort.SliceStable(pool, func(i, j int) bool {
		return pool[i].item.Popularity > pool[j].item.Popularity
	})

	tracks := make([]playlist.Track, len(pool))
	for i, pt := range pool {
		tracks[i] = popularPoolTrack(pt)
	}
	p.markPoolLiked(ctx, tracks)
	if len(tracks) > popularTrackCount {
		tracks = tracks[:popularTrackCount]
	}

	return provider.ArtistDetail{
		Info:        provider.ArtistInfo{ID: header.ID, Name: header.Name, AlbumCount: len(discography)},
		Genres:      header.Genres,
		Followers:   header.Followers.Total,
		Popular:     tracks,
		Discography: discography,
	}, nil
}

// fetchArtistHeader returns the artist's name, genres, and follower count.
func (p *SpotifyProvider) fetchArtistHeader(ctx context.Context, artistID string) (spotifyArtistFull, error) {
	resp, err := p.webAPI(ctx, "GET", "/v1/artists/"+artistID, nil)
	if err != nil {
		return spotifyArtistFull{}, fmt.Errorf("spotify: fetch artist: %w", err)
	}
	var header spotifyArtistFull
	if err := decodeBody(resp, &header); err != nil {
		return spotifyArtistFull{}, fmt.Errorf("spotify: parse artist: %w", err)
	}
	return header, nil
}

// fetchArtistDiscography returns the artist's albums and singles as raw
// SimplifiedAlbum objects (the pool needs release dates and art), paged by
// offset until the paging envelope's total is covered (10 per request — the
// endpoint's per-request max).
func (p *SpotifyProvider) fetchArtistDiscography(ctx context.Context, artistID string) ([]spotifyAlbum, error) {
	var all []spotifyAlbum
	offset := 0

	for {
		query := url.Values{
			"include_groups": {"album,single"},
			"limit":          {strconv.Itoa(artistAlbumsPageSize)},
			"offset":         {strconv.Itoa(offset)},
		}
		resp, err := p.webAPI(ctx, "GET", fmt.Sprintf("/v1/artists/%s/albums", artistID), query)
		if err != nil {
			return nil, fmt.Errorf("spotify: list artist albums: %w", err)
		}
		var result struct {
			Items []spotifyAlbum `json:"items"`
			Total int            `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse artist albums: %w", err)
		}

		all = append(all, result.Items...)

		if len(result.Items) == 0 || offset+artistAlbumsPageSize >= result.Total {
			break
		}
		offset += artistAlbumsPageSize
	}
	return all, nil
}

// artistPopularPool synthesizes the artist's popular-pool candidates: every
// track of the discography (in discography order) plus artist-filtered
// search results, deduped by URI (first occurrence wins). Each source
// failure only skips that source — the pool is best-effort by design.
func (p *SpotifyProvider) artistPopularPool(ctx context.Context, artistID, artistName string, discography []spotifyAlbum) []artistPoolTrack {
	var pool []artistPoolTrack
	seen := make(map[string]bool)

	add := func(items []artistPoolTrack) {
		for _, pt := range items {
			if pt.item == nil || pt.item.ID == "" {
				continue // skip unavailable tracks
			}
			if !poolItemMatchesArtist(pt.item, artistID, artistName) {
				continue
			}
			key := itemKey(pt.item)
			if seen[key] {
				continue
			}
			seen[key] = true
			pool = append(pool, pt)
		}
	}

	for _, album := range discography {
		tracks, err := p.fetchArtistAlbumTracks(ctx, album)
		if err != nil {
			continue // skip this album, keep going
		}
		add(tracks)
	}

	if search, err := p.fetchArtistSearchTracks(ctx, artistName); err == nil {
		add(search)
	}
	return pool
}

// fetchArtistAlbumTracks returns every track of one album, paged like
// AlbumTracks (50 per request) with the album's name, art URL, and release
// year carried on the pool candidates — simplified album-track objects lack
// that metadata. The caller's ctx bounds the fetch (unlike AlbumTracks,
// which owns its timeout).
func (p *SpotifyProvider) fetchArtistAlbumTracks(ctx context.Context, album spotifyAlbum) ([]artistPoolTrack, error) {
	var all []artistPoolTrack
	offset := 0

	for {
		query := url.Values{
			"limit":  {strconv.Itoa(spotifyTrackPageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		resp, err := p.webAPI(ctx, "GET", "/v1/albums/"+album.ID+"/tracks", query)
		if err != nil {
			return nil, fmt.Errorf("spotify: list album tracks: %w", err)
		}
		var result struct {
			Items []*spotifyItem `json:"items"`
			Total int            `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse album tracks: %w", err)
		}

		for _, item := range result.Items {
			if item == nil || item.ID == "" {
				continue // skip unavailable tracks
			}
			all = append(all, artistPoolTrack{item: item, album: album})
		}

		if len(result.Items) == 0 || offset+spotifyTrackPageSize >= result.Total {
			break
		}
		offset += spotifyTrackPageSize
	}
	return all, nil
}

// fetchArtistSearchTracks returns up to artistSearchPages pages of tracks
// matching the artist:"NAME" search filter (10 per page — the endpoint's
// per-request max).
func (p *SpotifyProvider) fetchArtistSearchTracks(ctx context.Context, artistName string) ([]artistPoolTrack, error) {
	var all []artistPoolTrack

	for page := range artistSearchPages {
		query := url.Values{
			"q":      {fmt.Sprintf(`artist:"%s"`, artistName)},
			"type":   {"track"},
			"limit":  {strconv.Itoa(artistSearchPageSize)},
			"offset": {strconv.Itoa(page * artistSearchPageSize)},
		}
		resp, err := p.webAPI(ctx, "GET", "/v1/search", query)
		if err != nil {
			return nil, fmt.Errorf("spotify: search artist tracks: %w", err)
		}
		var result struct {
			Tracks struct {
				Items []*spotifyItem `json:"items"`
				Total int            `json:"total"`
			} `json:"tracks"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse artist track search: %w", err)
		}

		for _, item := range result.Tracks.Items {
			all = append(all, artistPoolTrack{item: item})
		}

		if len(result.Tracks.Items) == 0 || (page+1)*artistSearchPageSize >= result.Tracks.Total {
			break
		}
	}
	return all, nil
}

// poolItemMatchesArtist reports whether a pool candidate plausibly belongs
// to the artist: items without an artists array are kept (not checkable),
// and otherwise any artist matching the ID or exact name qualifies. Search
// results for artist:"NAME" can still include tangential matches, so this
// is a light touch — URI dedupe is the real correctness bar.
func poolItemMatchesArtist(item *spotifyItem, artistID, artistName string) bool {
	if len(item.Artists) == 0 {
		return true
	}
	for _, a := range item.Artists {
		if (artistID != "" && a.ID == artistID) || (artistName != "" && a.Name == artistName) {
			return true
		}
	}
	return false
}

// popularPoolTrack converts a popular-pool candidate into a playlist.Track.
// It is deliberately separate from trackFromItem's hot path (playlist
// fetching): album-sourced candidates are simplified track objects, so the
// parent album's name, art URL, and release year are merged in here (like
// AlbumTracks), and the popularity meta key is set when the item reports a
// score (search results do; simplified album tracks don't — no ProviderMeta
// allocation for zero-popularity tracks).
func popularPoolTrack(pt artistPoolTrack) playlist.Track {
	t := trackFromItem(pt.item)
	if pt.album.ID != "" {
		t.Album = pt.album.Name
		if len(pt.album.Images) > 0 {
			t.AlbumArtURL = pt.album.Images[0].URL
		}
		if year := releaseYear(pt.album.ReleaseDate); year > 0 {
			t.Year = year
		}
	}
	if pt.item.Popularity > 0 {
		if t.ProviderMeta == nil {
			t.ProviderMeta = map[string]string{}
		}
		t.ProviderMeta[provider.MetaSpotifyPopularity] = strconv.Itoa(pt.item.Popularity)
	}
	return t
}

// markPoolLiked batches the pool's track URIs through
// /v1/me/library/contains (40 URIs per call — the endpoint's max) and marks
// contained tracks in ProviderMeta. Any failure skips marking entirely —
// liked marks are cosmetic and never fail the artist detail.
func (p *SpotifyProvider) markPoolLiked(ctx context.Context, tracks []playlist.Track) {
	uris := make([]string, 0, len(tracks))
	byURI := make(map[string][]int, len(tracks))
	for i, t := range tracks {
		uri := spotifyTrackURI(t)
		if uri == "" {
			continue
		}
		if len(byURI[uri]) == 0 {
			uris = append(uris, uri)
		}
		byURI[uri] = append(byURI[uri], i)
	}

	for len(uris) > 0 {
		size := min(libraryContainsChunk, len(uris))
		chunk := uris[:size]

		resp, err := p.webAPI(ctx, "GET", "/v1/me/library/contains", url.Values{"uris": {strings.Join(chunk, ",")}})
		if err != nil {
			return // non-fatal: skip marks
		}
		var states []bool
		if err := decodeBody(resp, &states); err != nil {
			return // non-fatal: skip marks
		}

		for i, uri := range chunk {
			if i >= len(states) || !states[i] {
				continue
			}
			for _, idx := range byURI[uri] {
				if tracks[idx].ProviderMeta == nil {
					tracks[idx].ProviderMeta = map[string]string{}
				}
				tracks[idx].ProviderMeta[provider.MetaSpotifyLiked] = "true"
			}
		}
		uris = uris[size:]
	}
}
