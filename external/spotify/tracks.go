package spotify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

// playlistCache holds a snapshot_id and the fetched tracks for a playlist,
// allowing us to skip re-fetching playlists that haven't changed.
type playlistCache struct {
	snapshotID string
	tracks     []playlist.Track
	total      int
}

// pendingTracks accumulates a progressive load. want is the offset the next
// page must carry and total is the list size the first page reported; a page
// that is out of order, or that reports a different total and so was read from
// a changed library, is served to the caller but never accumulated. Contiguity
// alone is not enough: a mutation mid-load shifts every later offset, so pages
// from two snapshots can splice together into a list that is short by one and
// duplicated by one, which revalidation cannot detect.
type pendingTracks struct {
	tracks   []playlist.Track
	want     int
	total    int
	snapshot string // playlist snapshot_id the accumulation began under; "" for saved tracks
}

// Tracks returns all tracks for the given Spotify playlist ID.
// Track.Path is set to the canonical spotify: URI for the player to resolve.
// Results are cached by snapshot_id; unchanged playlists skip the API call.
// Saved-album entries (savedAlbumIDPrefix) are expanded via AlbumTracks.
func (p *SpotifyProvider) Tracks(playlistID string) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}

	if albumID, ok := isSavedAlbumID(playlistID); ok {
		tracks, err := p.AlbumTracks(albumID)
		if err != nil {
			return nil, fmt.Errorf("spotify: list saved album tracks: %w", err)
		}
		return tracks, nil
	}
	// Check cache — if we have tracks and the snapshot_id hasn't changed, return cached.
	p.mu.Lock()
	if tracks, _, ok := p.cachedTracksLocked(playlistID); ok {
		p.mu.Unlock()
		return tracks, nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// A like or unlike mid-load shifts every later offset, so pages read either
	// side of the change splice into a list short by one and duplicated by one.
	// Restart against the new snapshot when the total moves, bounding restarts
	// so a library being actively edited cannot loop forever.
	const maxRestarts = 2
	var all []playlist.Track
	total, offset, restarts := -1, 0, 0
	for {
		page, pageTotal, err := p.fetchTracksPage(ctx, playlistID, offset)
		if err != nil {
			return nil, err
		}
		if total < 0 {
			total = pageTotal
		}
		if pageTotal != total {
			if restarts == maxRestarts {
				return nil, fmt.Errorf("spotify: list tracks: %q changed while loading", playlistID)
			}
			restarts++
			all, total, offset = nil, -1, 0
			continue
		}
		all = append(all, page...)
		if offset+spotifyTrackPageSize >= total {
			break
		}
		offset += spotifyTrackPageSize
	}

	// Cache the fetched tracks.
	p.mu.Lock()
	p.cacheTracksLocked(playlistID, all, total)
	p.mu.Unlock()

	return slices.Clone(all), nil
}

// fetchTracksPage reads one page of a playlist's tracks and returns it with the
// list's current total. Saved tracks and playlist items come from different
// endpoints with different shapes, so this is the single place that difference
// lives; both Tracks and TracksPage page through it so they cannot drift apart.
// Items without an ID -- local files, unavailable tracks -- are skipped, so the
// returned slice is usually shorter than the page size and the caller must
// advance by the page size rather than by len(tracks).
func (p *SpotifyProvider) fetchTracksPage(ctx context.Context, playlistID string, offset int) ([]playlist.Track, int, error) {
	query := url.Values{
		"limit":  {strconv.Itoa(spotifyTrackPageSize)},
		"offset": {strconv.Itoa(offset)},
	}
	path := "/v1/me/tracks"
	if playlistID != savedTracksPlaylistID {
		query.Set("fields", "items(item(id,name,type,uri,artists(name),album(name,release_date,images),show(name,images),images,release_date,duration_ms,track_number,is_playable,restrictions(reason))),total")
		path = fmt.Sprintf("/v1/playlists/%s/items", playlistID)
	}
	resp, err := p.webAPI(ctx, "GET", path, query)
	if err != nil {
		if playlistID != savedTracksPlaylistID && hasStatus(err, http.StatusForbidden) {
			return nil, 0, fmt.Errorf("spotify: Development Mode apps can read only playlists you own or collaborate on. Remove client_id from [spotify] to use the built-in client, or add the tracks to your own playlist in Spotify: %w", err)
		}
		return nil, 0, fmt.Errorf("spotify: list tracks: %w", err)
	}
	var result struct {
		Items []struct {
			Item  *spotifyItem `json:"item"`
			Track *spotifyItem `json:"track"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, 0, fmt.Errorf("spotify: parse tracks: %w", err)
	}
	var tracks []playlist.Track
	for _, item := range result.Items {
		t := item.Item
		if t == nil {
			t = item.Track
		}
		if t == nil || t.ID == "" {
			continue
		}
		tracks = append(tracks, trackFromItem(t))
	}
	return tracks, result.Total, nil
}

// headMatches reports whether an abandoned accumulation still begins with the
// freshly fetched page 0, meaning nothing entered or left the head of the list
// while it was closed. Resuming stitches two separate loads together, so an
// unchanged total is not enough on its own: a same-total swap below the head
// would splice the old ordering onto the new suffix.
func headMatches(accumulated, page []playlist.Track) bool {
	if len(accumulated) < len(page) {
		return false
	}
	for i, t := range page {
		if accumulated[i].Path != t.Path {
			return false
		}
	}
	return true
}

// snapshotIDLocked returns the snapshot_id last seen for playlistID, or "" if
// none is known. p.mu must be held.
func (p *SpotifyProvider) snapshotIDLocked(playlistID string) string {
	if cached := p.trackCache[playlistID]; cached != nil {
		return cached.snapshotID
	}
	return ""
}

// playlistSnapshot reads a playlist's current snapshot_id, Spotify's own version
// token: it changes on every edit, so an unchanged one proves the playlist is
// untouched -- which an unchanged total and head cannot, since an ordinary
// playlist can be edited anywhere.
func (p *SpotifyProvider) playlistSnapshot(ctx context.Context, playlistID string) (string, error) {
	resp, err := p.webAPI(ctx, "GET", "/v1/playlists/"+playlistID, url.Values{"fields": {"snapshot_id"}})
	if err != nil {
		return "", fmt.Errorf("spotify: playlist snapshot %q: %w", playlistID, err)
	}
	var result struct {
		SnapshotID string `json:"snapshot_id"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return "", fmt.Errorf("spotify: parse playlist snapshot %q: %w", playlistID, err)
	}
	return result.SnapshotID, nil
}

// cachedTracksLocked returns a copy of the committed list and its total, if
// any. p.mu must be held.
func (p *SpotifyProvider) cachedTracksLocked(playlistID string) (tracks []playlist.Track, total int, ok bool) {
	cached := p.trackCache[playlistID]
	if cached == nil || cached.tracks == nil {
		return nil, 0, false
	}
	return slices.Clone(cached.tracks), cached.total, true
}

// cacheTracksLocked stores a fully loaded track list. p.mu must be held.
func (p *SpotifyProvider) cacheTracksLocked(playlistID string, tracks []playlist.Track, total int) {
	if cached, ok := p.trackCache[playlistID]; ok {
		cached.tracks = tracks
		cached.total = total
		return
	}
	p.trackCache[playlistID] = &playlistCache{tracks: tracks, total: total}
}

// savedTracksUnchanged revalidates a cached Liked Songs list with a single
// limit=1 request. /v1/me/tracks ordering is undocumented but is empirically
// added_at descending, so an unchanged total plus an unchanged newest entry
// means no add or removal. If that ever stops holding the comparison simply
// misses and we refetch, so the failure direction is stale-free.
func (p *SpotifyProvider) savedTracksUnchanged(ctx context.Context, tracks []playlist.Track, total int) bool {
	resp, err := p.webAPI(ctx, "GET", "/v1/me/tracks", url.Values{"limit": {"1"}})
	if err != nil {
		return false
	}
	var result struct {
		Items []struct {
			Track *spotifyItem `json:"track"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := decodeBody(resp, &result); err != nil || result.Total != total {
		return false
	}
	if len(result.Items) == 0 || result.Items[0].Track == nil {
		return len(tracks) == 0
	}
	return len(tracks) > 0 && result.Items[0].Track.URI == tracks[0].Path
}

// TracksPage returns one page of playlistID's tracks plus the offset to request
// next, or 0 when the playlist is fully loaded. Implements provider.TrackPager.
func (p *SpotifyProvider) TracksPage(playlistID string, offset int) ([]playlist.Track, int, error) {
	if err := p.ensureSession(); err != nil {
		return nil, 0, err
	}
	// Saved albums are a separate endpoint and are small enough to arrive whole,
	// so hand them back as one complete page. Tracks() routes them the same way;
	// leaving them out here would build a playlist-items URL from an album ID.
	if albumID, ok := isSavedAlbumID(playlistID); ok {
		tracks, err := p.AlbumTracks(albumID)
		if err != nil {
			return nil, 0, fmt.Errorf("spotify: list saved album tracks: %w", err)
		}
		return tracks, 0, nil
	}
	p.mu.Lock()
	var tracks []playlist.Track
	var cachedTotal int
	hit := false
	if offset == 0 {
		tracks, cachedTotal, hit = p.cachedTracksLocked(playlistID)
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if hit && (playlistID != savedTracksPlaylistID || p.savedTracksUnchanged(ctx, tracks, cachedTotal)) {
		return tracks, 0, nil
	}
	page, total, err := p.fetchTracksPage(ctx, playlistID, offset)
	if err != nil {
		return nil, 0, err
	}

	next := offset + spotifyTrackPageSize
	if next >= total {
		next = 0
	}

	// Whether an abandoned accumulation can be resumed may need a request, so
	// settle it before taking the lock the accumulation is guarded by.
	resumable := false
	if offset == 0 {
		p.mu.Lock()
		pend := p.pending[playlistID]
		viable := pend != nil && pend.want > 0 && pend.total == total
		head := viable && playlistID == savedTracksPlaylistID && headMatches(pend.tracks, page)
		snapshot := ""
		if viable && playlistID != savedTracksPlaylistID {
			snapshot = pend.snapshot
		}
		p.mu.Unlock()

		switch {
		case head:
			// Saved tracks cannot hide an edit: a like lands at position 0 and an
			// unlike moves the total, so the head and total together are proof.
			resumable = true
		case snapshot != "":
			current, err := p.playlistSnapshot(ctx, playlistID)
			resumable = err == nil && current == snapshot
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	pend := p.pending[playlistID]
	if offset == 0 {
		// Re-entering a list abandoned mid-load resumes the earlier accumulation
		// rather than refetching every page already paid for, at the cost of one
		// request to prove nothing moved in between.
		if resumable && pend != nil {
			return slices.Clone(pend.tracks), pend.want, nil
		}
		pend = &pendingTracks{total: total, snapshot: p.snapshotIDLocked(playlistID)}
		p.pending[playlistID] = pend
	}
	// A page at an offset this accumulation is not waiting for belongs to a
	// superseded chain: serve it to its caller, but do not accumulate it.
	if pend == nil || pend.want != offset {
		return page, next, nil
	}
	// The live chain's own page reporting a different total means the library
	// moved under it. Every later page would mismatch the pinned snapshot too,
	// so the load can never commit -- stop now rather than spending the rest of
	// the pages on a result that is already discarded.
	if pend.total != total {
		delete(p.pending, playlistID)
		return nil, 0, fmt.Errorf("spotify: list tracks %q: %w", playlistID, playlist.ErrListChanged)
	}
	pend.tracks = append(pend.tracks, page...)
	pend.want = next
	if next == 0 {
		p.cacheTracksLocked(playlistID, pend.tracks, total)
		delete(p.pending, playlistID)
	}
	return page, next, nil
}
