//go:build !windows

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

// Compile-time interface checks.
var _ provider.Recommender = (*SpotifyProvider)(nil)

// Recommendation flow bounds. recommendTimeout wraps the whole multi-call
// flow; the page sizes respect the per-endpoint maximums (50 for top items
// and album tracks, 10 for artist albums).
const (
	recommendTimeout     = 30 * time.Second
	maxDiscoveryArtists  = 3
	recommendArtistsPage = 20
	recommendAlbumsPage  = 10
)

// metaSpotifyArtistIDs is the ProviderMeta key carrying a comma-separated
// list of a track's artist IDs, when the source recorded them. trackFromItem
// does not populate it today, so the discovery artist-absence check is
// best-effort: with no IDs on the seed tracks every top artist qualifies.
const metaSpotifyArtistIDs = "spotify.artist_ids"

// recommendCollector accumulates recommendation candidates, deduped by
// spotify URI within the results and against the seed, capped at limit.
type recommendCollector struct {
	limit    int
	seedURIs map[string]bool
	seen     map[string]bool
	tracks   []playlist.Track
}

// full reports whether the collector has reached its limit.
func (c *recommendCollector) full() bool { return len(c.tracks) >= c.limit }

// add appends tracks not already in the seed or the results, stopping at the
// limit.
func (c *recommendCollector) add(tracks []playlist.Track) {
	for _, t := range tracks {
		if c.full() {
			return
		}
		uri := spotifyTrackURI(t)
		if uri == "" || c.seedURIs[uri] || c.seen[uri] {
			continue
		}
		c.seen[uri] = true
		c.tracks = append(c.tracks, t)
	}
}

// RecommendTracks returns up to limit tracks related to the seed queue,
// mixing two sources: familiar (the user's medium-term top tracks, filtered
// against the seed) and discovery (tracks from the newest albums of up to
// three top artists absent from the seed's artists). Results are deduped by
// URI against each other and the seed and capped at limit. Failures are
// never fatal: a failed sub-call skips its source or chain, and an error is
// returned only when both source calls failed. Implements
// provider.Recommender.
func (p *SpotifyProvider) RecommendTracks(ctx context.Context, seed []playlist.Track, limit int) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, recommendTimeout)
	defer cancel()

	c := &recommendCollector{
		limit:    limit,
		seedURIs: make(map[string]bool, len(seed)),
		seen:     make(map[string]bool, limit),
	}
	for _, t := range seed {
		if uri := spotifyTrackURI(t); uri != "" {
			c.seedURIs[uri] = true
		}
	}

	// Familiar source: one page of medium-term top tracks.
	familiar, familiarErr := p.fetchRecommendTopTracks(ctx)
	if familiarErr == nil {
		c.add(familiar)
	}

	// Discovery source: newest albums of top artists absent from the seed.
	var artistsErr error
	if !c.full() {
		var top []spotifyArtist
		top, artistsErr = p.fetchRecommendTopArtists(ctx)
		if artistsErr == nil {
			for _, artist := range recommendDiscoveryArtists(top, seedArtistUniverse(seed)) {
				if c.full() {
					break
				}
				if err := p.collectRecommendArtistTracks(ctx, artist.ID, c); err != nil {
					continue // skip this chain, keep going
				}
			}
		}
	}

	if familiarErr != nil && artistsErr != nil {
		return nil, fmt.Errorf("spotify: recommend: familiar: %w; discovery: %w", familiarErr, artistsErr)
	}
	return c.tracks, nil
}

// fetchRecommendTopTracks returns one page (50, the endpoint max) of the
// user's medium-term top tracks.
func (p *SpotifyProvider) fetchRecommendTopTracks(ctx context.Context) ([]playlist.Track, error) {
	query := url.Values{
		"time_range": {"medium_term"},
		"limit":      {strconv.Itoa(spotifyTrackPageSize)},
	}
	resp, err := p.webAPI(ctx, "GET", "/v1/me/top/tracks", query)
	if err != nil {
		return nil, fmt.Errorf("spotify: top tracks: %w", err)
	}
	var result struct {
		Items []*spotifyItem `json:"items"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse top tracks: %w", err)
	}

	var tracks []playlist.Track
	for _, item := range result.Items {
		if item == nil || item.ID == "" {
			continue // skip unavailable tracks
		}
		tracks = append(tracks, trackFromItem(item))
	}
	return tracks, nil
}

// fetchRecommendTopArtists returns one page of the user's top artists.
func (p *SpotifyProvider) fetchRecommendTopArtists(ctx context.Context) ([]spotifyArtist, error) {
	query := url.Values{"limit": {strconv.Itoa(recommendArtistsPage)}}
	resp, err := p.webAPI(ctx, "GET", "/v1/me/top/artists", query)
	if err != nil {
		return nil, fmt.Errorf("spotify: top artists: %w", err)
	}
	var result struct {
		Items []spotifyArtist `json:"items"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse top artists: %w", err)
	}
	return result.Items, nil
}

// recommendDiscoveryArtists picks up to maxDiscoveryArtists top artists
// absent from the seed's artist universe (matched by ID or name).
func recommendDiscoveryArtists(top []spotifyArtist, seedArtists map[string]bool) []spotifyArtist {
	var out []spotifyArtist
	for _, a := range top {
		if len(out) == maxDiscoveryArtists {
			break
		}
		if a.ID != "" && !seedArtists[a.ID] && !seedArtists[strings.ToLower(a.Name)] {
			out = append(out, a)
		}
	}
	return out
}

// seedArtistUniverse collects the artist IDs carried by the seed tracks plus
// their artist names. Queue tracks usually carry names only (no ID plumbing),
// so names keep the absence check working.
func seedArtistUniverse(seed []playlist.Track) map[string]bool {
	universe := make(map[string]bool)
	for _, t := range seed {
		for _, id := range strings.Split(t.ProviderMeta[metaSpotifyArtistIDs], ",") {
			if id = strings.TrimSpace(id); id != "" {
				universe[id] = true
			}
		}
		for _, name := range strings.Split(t.Artist, ", ") {
			if name != "" {
				universe[strings.ToLower(name)] = true
			}
		}
	}
	return universe
}

// collectRecommendArtistTracks walks one discovery chain: the artist's albums
// (include_groups=album, one page of 10 — the endpoint's per-request max),
// newest release first, adding each album's tracks to c. A failed album-tracks
// call skips that album and keeps going; the chain's error is returned so the
// caller can skip to the next artist.
func (p *SpotifyProvider) collectRecommendArtistTracks(ctx context.Context, artistID string, c *recommendCollector) error {
	query := url.Values{
		"include_groups": {"album"},
		"limit":          {strconv.Itoa(recommendAlbumsPage)},
	}
	resp, err := p.webAPI(ctx, "GET", "/v1/artists/"+artistID+"/albums", query)
	if err != nil {
		return fmt.Errorf("spotify: artist albums: %w", err)
	}
	var page struct {
		Items []spotifyAlbum `json:"items"`
	}
	if err := decodeBody(resp, &page); err != nil {
		return fmt.Errorf("spotify: parse artist albums: %w", err)
	}

	// Newest release first. ISO release dates sort correctly as strings.
	sort.SliceStable(page.Items, func(i, j int) bool {
		return page.Items[i].ReleaseDate > page.Items[j].ReleaseDate
	})

	for _, album := range page.Items {
		if c.full() {
			return nil
		}
		tracks, err := p.fetchRecommendAlbumTracks(ctx, album)
		if err != nil {
			continue // skip this album, keep going
		}
		c.add(tracks)
	}
	return nil
}

// fetchRecommendAlbumTracks returns one page of an album's tracks, merged
// with the album name, art URL, and release year from the parent album
// object (simplified album-track objects lack that metadata).
func (p *SpotifyProvider) fetchRecommendAlbumTracks(ctx context.Context, album spotifyAlbum) ([]playlist.Track, error) {
	query := url.Values{"limit": {strconv.Itoa(spotifyTrackPageSize)}}
	resp, err := p.webAPI(ctx, "GET", "/v1/albums/"+album.ID+"/tracks", query)
	if err != nil {
		return nil, fmt.Errorf("spotify: album tracks: %w", err)
	}
	var result struct {
		Items []*spotifyItem `json:"items"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse album tracks: %w", err)
	}

	artURL := ""
	if len(album.Images) > 0 {
		artURL = album.Images[0].URL
	}
	year := releaseYear(album.ReleaseDate)

	var tracks []playlist.Track
	for _, item := range result.Items {
		if item == nil || item.ID == "" {
			continue // skip unavailable tracks
		}
		t := trackFromItem(item)
		t.Album = album.Name
		t.AlbumArtURL = artURL
		t.Year = year
		t.ProviderMeta = map[string]string{metaSpotifyID: item.ID}
		tracks = append(tracks, t)
	}
	return tracks, nil
}
