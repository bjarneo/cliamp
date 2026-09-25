package spotify

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.ArtistBrowser    = (*SpotifyProvider)(nil)
	_ provider.AlbumBrowser     = (*SpotifyProvider)(nil)
	_ provider.AlbumSortSaver   = (*SpotifyProvider)(nil)
	_ provider.AlbumTrackLoader = (*SpotifyProvider)(nil)
	_ provider.TrackPager       = (*SpotifyProvider)(nil)
)

// Sort type IDs for saved-album browsing. Sorts are client-side: the API only
// returns saved albums newest-saved-first.
const (
	SortRecent = "recent" // API order (newest saved first)
	SortTitle  = "title"
	SortArtist = "artist"
	SortYear   = "year" // newest release year first
)

// metaSpotifyID is the ProviderMeta key carrying the Spotify track/episode ID.
const metaSpotifyID = "spotify.id"

// webAPITimeout bounds multi-page browse fetches; probeTimeout bounds the
// single-call availability probes used by Playlists().
const (
	webAPITimeout = 2 * time.Minute
	probeTimeout  = 30 * time.Second
)

var albumSortTypes = []provider.SortType{
	{ID: SortRecent, Label: "Recently saved"},
	{ID: SortTitle, Label: "Title"},
	{ID: SortArtist, Label: "Artist"},
	{ID: SortYear, Label: "Year"},
}

func validAlbumSort(id string) bool {
	return slices.ContainsFunc(albumSortTypes, func(s provider.SortType) bool { return s.ID == id })
}

// spotifyAlbum is the album object shared by /v1/me/albums,
// /v1/artists/{id}/albums, and /v1/albums/{id} responses.
type spotifyAlbum struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Artists     []spotifyArtist `json:"artists"`
	ReleaseDate string          `json:"release_date"`
	TotalTracks int             `json:"total_tracks"`
	Images      []spotifyImage  `json:"images"`
}

func albumFromSpotify(a spotifyAlbum) provider.AlbumInfo {
	info := provider.AlbumInfo{
		ID:         a.ID,
		Name:       a.Name,
		Year:       releaseYear(a.ReleaseDate),
		TrackCount: a.TotalTracks,
	}
	if len(a.Artists) > 0 {
		info.Artist = a.Artists[0].Name
		info.ArtistID = a.Artists[0].ID
	}
	return info
}

// Artists returns the artists the current user follows.
// AlbumCount stays 0: Spotify doesn't report it cheaply.
func (p *SpotifyProvider) Artists() ([]provider.ArtistInfo, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), webAPITimeout)
	defer cancel()

	var all []provider.ArtistInfo
	offset := 0

	for {
		query := url.Values{
			"type":   {"artist"},
			"limit":  {strconv.Itoa(spotifyPlaylistPageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		resp, err := p.webAPI(ctx, "GET", "/v1/me/following", query)
		if err != nil {
			return nil, fmt.Errorf("spotify: list artists: %w", err)
		}
		var result struct {
			Artists struct {
				Items []spotifyArtist `json:"items"`
				Total int             `json:"total"`
			} `json:"artists"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse artists: %w", err)
		}

		for _, a := range result.Artists.Items {
			all = append(all, provider.ArtistInfo{ID: a.ID, Name: a.Name})
		}

		if len(result.Artists.Items) == 0 || offset+spotifyPlaylistPageSize >= result.Artists.Total {
			break
		}
		offset += spotifyPlaylistPageSize
	}
	return all, nil
}

// ArtistAlbums returns the albums and singles of an artist in API order.
func (p *SpotifyProvider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), webAPITimeout)
	defer cancel()

	var all []provider.AlbumInfo
	offset := 0

	for {
		query := url.Values{
			"include_groups": {"album,single"},
			"limit":          {strconv.Itoa(spotifyArtistAlbumsPageSize)},
			"offset":         {strconv.Itoa(offset)},
		}
		path := fmt.Sprintf("/v1/artists/%s/albums", artistID)
		resp, err := p.webAPI(ctx, "GET", path, query)
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

		for _, a := range result.Items {
			all = append(all, albumFromSpotify(a))
		}

		if len(result.Items) == 0 || offset+spotifyArtistAlbumsPageSize >= result.Total {
			break
		}
		offset += spotifyArtistAlbumsPageSize
	}
	return all, nil
}

// AlbumList returns one window of the user's saved albums, sorted by sortType.
// The full list is fetched once and cached for playlistListCacheTTL.
func (p *SpotifyProvider) AlbumList(sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	if sortType == "" {
		sortType = SortRecent
	}
	if !validAlbumSort(sortType) {
		return nil, fmt.Errorf("spotify: unknown album sort %q", sortType)
	}

	albums, err := p.savedAlbumInfos()
	if err != nil {
		return nil, err
	}

	sorted := albums
	if sortType != SortRecent {
		sorted = slices.Clone(albums)
		switch sortType {
		case SortTitle:
			sort.SliceStable(sorted, func(i, j int) bool {
				return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
			})
		case SortArtist:
			sort.SliceStable(sorted, func(i, j int) bool {
				ai, aj := strings.ToLower(sorted[i].Artist), strings.ToLower(sorted[j].Artist)
				if ai != aj {
					return ai < aj
				}
				return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
			})
		case SortYear:
			sort.SliceStable(sorted, func(i, j int) bool {
				if sorted[i].Year != sorted[j].Year {
					return sorted[i].Year > sorted[j].Year // newest year first
				}
				return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
			})
		}
	}

	if offset < 0 {
		offset = 0
	}
	if size < 0 {
		size = 0
	}
	if offset >= len(sorted) {
		return nil, nil
	}
	end := min(offset+size, len(sorted))
	return slices.Clone(sorted[offset:end]), nil
}

// savedAlbumInfos returns the full saved-albums list (newest saved first),
// cached for playlistListCacheTTL.
func (p *SpotifyProvider) savedAlbumInfos() ([]provider.AlbumInfo, error) {
	p.mu.Lock()
	if !p.albumCacheAt.IsZero() && time.Since(p.albumCacheAt) < playlistListCacheTTL {
		cached := slices.Clone(p.albumCache)
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), webAPITimeout)
	defer cancel()

	var all []provider.AlbumInfo
	offset := 0

	for {
		query := url.Values{
			"limit":  {strconv.Itoa(spotifyPlaylistPageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		resp, err := p.webAPI(ctx, "GET", "/v1/me/albums", query)
		if err != nil {
			return nil, fmt.Errorf("spotify: list albums: %w", err)
		}
		var result struct {
			Items []struct {
				Album spotifyAlbum `json:"album"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse albums: %w", err)
		}

		for _, item := range result.Items {
			all = append(all, albumFromSpotify(item.Album))
		}

		if len(result.Items) == 0 || offset+spotifyPlaylistPageSize >= result.Total {
			break
		}
		offset += spotifyPlaylistPageSize
	}

	p.mu.Lock()
	p.albumCache = all
	p.albumCacheAt = time.Now()
	p.mu.Unlock()

	return slices.Clone(all), nil
}

// invalidateAlbumCache clears the saved-albums cache. Call after save/unsave
// album write operations so the next AlbumList reflects the change.
func (p *SpotifyProvider) invalidateAlbumCache() {
	p.mu.Lock()
	p.albumCache = nil
	p.albumCacheAt = time.Time{}
	p.mu.Unlock()
}

func (p *SpotifyProvider) AlbumSortTypes() []provider.SortType {
	return albumSortTypes
}

// DefaultAlbumSort returns the persisted sort (config [spotify] album_sort),
// falling back to SortRecent. main.go wiring doesn't thread the config value
// in, so it is read lazily here and memoized.
func (p *SpotifyProvider) DefaultAlbumSort() string {
	p.mu.Lock()
	sortType := p.browseSort
	p.mu.Unlock()
	if sortType != "" {
		return sortType
	}

	if cfg, err := config.Load(); err == nil && validAlbumSort(cfg.Spotify.AlbumSort) {
		p.mu.Lock()
		p.browseSort = cfg.Spotify.AlbumSort
		p.mu.Unlock()
		return cfg.Spotify.AlbumSort
	}
	return SortRecent
}

// SaveAlbumSort validates sortType against the known IDs, remembers it
// in-process, and persists it to the [spotify] config section.
func (p *SpotifyProvider) SaveAlbumSort(sortType string) error {
	if sortType == "" {
		sortType = SortRecent
	}
	if !validAlbumSort(sortType) {
		return fmt.Errorf("spotify: unknown album sort %q", sortType)
	}

	p.mu.Lock()
	p.browseSort = sortType
	p.mu.Unlock()

	if err := config.SaveSpotifySort(sortType); err != nil {
		return fmt.Errorf("spotify: save album sort: %w", err)
	}
	return nil
}
