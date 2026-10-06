package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

const playlistListCacheTTL = 5 * time.Minute

// currentUserID returns the authenticated user's Spotify ID, fetched from
// /v1/me at most once per session. Failures are remembered so a network blip
// during the first call doesn't trigger a request on every later use.
func (p *SpotifyProvider) currentUserID(ctx context.Context) string {
	p.mu.Lock()
	if p.meFetched {
		id := p.userID
		p.mu.Unlock()
		return id
	}
	p.mu.Unlock()

	var me struct {
		ID string `json:"id"`
	}
	if resp, err := p.webAPI(ctx, "GET", "/v1/me", nil); err == nil {
		_ = decodeBody(resp, &me)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.userID = me.ID
	p.meFetched = true
	return p.userID
}

// Playlists returns all playlists in the authenticated user's Spotify library.
func (p *SpotifyProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	p.mu.Lock()
	if p.listCache != nil && time.Since(p.listCacheAt) < playlistListCacheTTL {
		cached := slices.Clone(p.listCache)
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	userID := p.currentUserID(ctx)

	var all []playlist.PlaylistInfo
	offset := 0
	limit := spotifyPlaylistPageSize

	// List of Playlists only includes created playlists by the User.
	// This doesn't include the 'Liked Songs' playlist.
	resp, err := p.webAPI(ctx, "GET", "/v1/me/tracks", nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: your music: %w", err)
	}

	var result struct {
		Total int `json:"total"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse playlists: %w", err)
	}

	// Unfortunately, the Spotify API doesn't expose the localized display name.
	// i.e. 'Liked Songs' or 'Lieblingssongs' etc.
	// For the moment, "Your Music" must sufficice without adding a localization
	// map.
	all = append(all, playlist.PlaylistInfo{
		ID:         savedTracksPlaylistID,
		Name:       "Your Music",
		TrackCount: result.Total,
		Section:    "Library",
	})

	for {
		query := url.Values{
			"limit":  {fmt.Sprintf("%d", limit)},
			"offset": {fmt.Sprintf("%d", offset)},
			"fields": {"items(id,name,snapshot_id,collaborative,owner(id),items.total),total"},
		}

		resp, err := p.webAPI(ctx, "GET", "/v1/me/playlists", query)
		if err != nil {
			return nil, fmt.Errorf("spotify: list playlists: %w", err)
		}

		var result struct {
			Items []spotifyPlaylistItem `json:"items"`
			Total int                   `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse playlists: %w", err)
		}

		p.mu.Lock()
		for _, item := range result.Items {
			count := 0
			if item.Items != nil {
				count = item.Items.Total
			}
			section := "Followed playlists"
			owned := userID != "" && item.Owner.ID == userID
			if owned {
				section = "Your playlists"
			}
			// Without a user ID, ownership is unknown, so offer every playlist.
			p.writable[item.ID] = owned || item.Collaborative || userID == ""
			all = append(all, playlist.PlaylistInfo{
				ID:         item.ID,
				Name:       item.Name,
				TrackCount: count,
				Section:    section,
			})
			// Update snapshot_id in cache; if it changed, invalidate cached tracks.
			if cached, ok := p.trackCache[item.ID]; ok {
				if cached.snapshotID != item.SnapshotID {
					delete(p.trackCache, item.ID)
				}
			}
			// Store snapshot_id for later cache checks in Tracks().
			if _, ok := p.trackCache[item.ID]; !ok && item.SnapshotID != "" {
				p.trackCache[item.ID] = &playlistCache{snapshotID: item.SnapshotID}
			}
		}
		p.mu.Unlock()

		if offset+limit >= result.Total {
			break
		}
		offset += limit
	}

	// Saved albums are extra. When they fail, show the playlists and report
	// the error, and do not cache the partial list.
	albums, albumsErr := p.savedAlbums(ctx)
	all = append(all, albums...)

	// Group playlists by section so the UI can emit one header per group.
	// Library first, then owned, then followed, then saved albums; preserve
	// API order within each section.
	sectionOrder := map[string]int{
		"Library":            0,
		"Your playlists":     1,
		"Followed playlists": 2,
		savedAlbumSection:    3,
	}
	sort.SliceStable(all, func(i, j int) bool {
		return sectionOrder[all[i].Section] < sectionOrder[all[j].Section]
	})

	if albumsErr != nil {
		return all, albumsErr
	}

	p.mu.Lock()
	p.listCache = all
	p.listCacheAt = time.Now()
	p.mu.Unlock()

	return slices.Clone(all), nil
}

// savedAlbums returns the authenticated user's saved albums from
// /v1/me/albums, paginated. Each is surfaced as a playlist entry whose ID
// carries the savedAlbumIDPrefix, so Tracks() expands it via AlbumTracks.
func (p *SpotifyProvider) savedAlbums(ctx context.Context) ([]playlist.PlaylistInfo, error) {
	var all []playlist.PlaylistInfo
	offset := 0

	for {
		query := url.Values{
			"limit":  {strconv.Itoa(spotifyAlbumPageSize)},
			"offset": {strconv.Itoa(offset)},
		}

		resp, err := p.webAPI(ctx, "GET", "/v1/me/albums", query)
		if err != nil {
			return nil, fmt.Errorf("spotify: list saved albums: %w", err)
		}

		var result struct {
			Items []struct {
				Album spotifyAlbumItem `json:"album"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := decodeBody(resp, &result); err != nil {
			return nil, fmt.Errorf("spotify: parse saved albums: %w", err)
		}

		for _, item := range result.Items {
			a := item.Album
			if a.ID == "" {
				continue // skip unavailable albums
			}
			name := a.Name
			if artist := artistNames(a.Artists); artist != "" {
				name = artist + " - " + a.Name
			}
			all = append(all, playlist.PlaylistInfo{
				ID:         savedAlbumIDPrefix + a.ID,
				Name:       name,
				TrackCount: a.TotalTracks,
				Section:    savedAlbumSection,
			})
		}

		if offset+spotifyAlbumPageSize >= result.Total {
			break
		}
		offset += spotifyAlbumPageSize
	}

	// Spotify returns saved albums most-recently-added first; sort by the
	// "Artist - Album" display name so the list reads alphabetically by artist.
	sort.SliceStable(all, func(i, j int) bool {
		return strings.ToLower(all[i].Name) < strings.ToLower(all[j].Name)
	})

	return all, nil
}

// CanAddToPlaylist reports whether tracks can be added to pl. Liked Songs
// and saved albums use other endpoints, and Spotify rejects adds to a
// playlist that the user neither owns nor collaborates on.
// Implements provider.PlaylistTargetFilter.
func (p *SpotifyProvider) CanAddToPlaylist(pl playlist.PlaylistInfo) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writable[pl.ID]
}

// AddTrackToPlaylist adds a track to an existing Spotify playlist.
// The track's Path is used as the Spotify URI (e.g. "spotify:track:..." or
// "spotify:episode:..."); the Spotify API accepts either.
// Implements provider.PlaylistWriter.
func (p *SpotifyProvider) AddTrackToPlaylist(ctx context.Context, playlistID string, track playlist.Track) error {
	trackURI := track.Path
	if err := p.ensureSession(); err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{"uris": []string{trackURI}})
	path := fmt.Sprintf("/v1/playlists/%s/items", playlistID)

	resp, err := p.webAPIWithRetry(ctx, "POST", path, nil, bytes.NewReader(body), "application/json", http.StatusOK, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("spotify: add track: %w", err)
	}
	resp.Body.Close()

	// Invalidate caches for this playlist.
	p.mu.Lock()
	delete(p.trackCache, playlistID)
	p.listCache = nil
	p.mu.Unlock()

	return nil
}

// CreatePlaylist creates a new private Spotify playlist and returns its ID.
func (p *SpotifyProvider) CreatePlaylist(ctx context.Context, name string) (string, error) {
	if err := p.ensureSession(); err != nil {
		return "", err
	}

	body, _ := json.Marshal(map[string]any{"name": name, "public": false})

	resp, err := p.webAPIWithRetry(ctx, "POST", "/v1/me/playlists", nil, bytes.NewReader(body), "application/json", http.StatusOK, http.StatusCreated)
	if err != nil {
		return "", fmt.Errorf("spotify: create playlist: %w", err)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return "", fmt.Errorf("spotify: parse created playlist: %w", err)
	}

	// Invalidate playlist list cache.
	p.mu.Lock()
	p.listCache = nil
	p.mu.Unlock()

	return result.ID, nil
}
