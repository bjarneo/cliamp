package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/audio"
	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.Searcher        = (*SpotifyProvider)(nil)
	_ provider.PlaylistWriter  = (*SpotifyProvider)(nil)
	_ provider.PlaylistCreator = (*SpotifyProvider)(nil)
	_ provider.CustomStreamer  = (*SpotifyProvider)(nil)
	_ provider.Closer          = (*SpotifyProvider)(nil)
	_ provider.TrackPager      = (*SpotifyProvider)(nil)
)

// SpotifyProvider implements playlist.Provider using the Spotify Web API
// for playlist/track metadata and go-librespot for audio streaming.
// playlistCache holds a snapshot_id and the fetched tracks for a playlist,
// allowing us to skip re-fetching playlists that haven't changed.
type playlistCache struct {
	// snapshotID is the Web API's snapshot_id; revision is the client
	// protocol's rootlist revision. They version the same playlist but are
	// different id spaces, so each path compares only its own. Neither is
	// evidence about the other: an absent counterpart means "never seen by
	// that path", so the Web API adopts it rather than reading it as a change,
	// while the client path drops the entry, which is the safe direction.
	snapshotID string
	revision   string
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
	// uris is the resolve this read is slicing, when the client protocol is
	// serving it. It lives here rather than in a map keyed by playlist so that
	// it cannot outlive the read or be taken by another one: a page sliced from
	// somebody else's snapshot is how a list ends up short by one and
	// duplicated by one, with nothing able to tell afterwards.
	uris []string
}

// tracksPage is one page of a read, plus the resolve it came from when the
// client protocol served it. The Web API pages the list itself and leaves uris
// nil.
type tracksPage struct {
	tracks   []playlist.Track
	total    int
	pageSize int
	uris     []string
}

type SpotifyProvider struct {
	session          *Session
	clientID         string
	bitrate          int
	userID           string // Spotify user ID, fetched lazily on first Playlists() call
	meFetched        bool   // /v1/me has been attempted this session; suppresses retry on failure
	mu               sync.Mutex
	trackCache       map[string]*playlistCache // playlist ID → cache entry
	pending          map[string]*pendingTracks
	declinedByWeb    map[string]bool // playlist ID -> the web api refused it this read
	declinedByClient map[string]bool // playlist ID -> the client protocol refused it this read
	apiMode          apiMode         // which read path to prefer; see CLIAMP_SPOTIFY_API
	// clientPage is the client-protocol read, indirected so tests can count the
	// attempts the decline flags exist to prevent: a failed attempt is
	// otherwise indistinguishable from one that never happened. Always set.
	clientPage func(context.Context, string, int, []string) (tracksPage, error)
	authCancel context.CancelFunc // cancels any in-progress OAuth flow

	// Playlist list cache to avoid redundant API calls on provider switch.
	listCache   []playlist.PlaylistInfo
	listCacheAt time.Time
}

const playlistListCacheTTL = 5 * time.Minute

// webTracksAttemptBudget bounds the Web API attempt for a playlist that has a
// client-protocol fallback available. A page normally returns well inside a
// second; the budget exists so a throttled account falls through quickly rather
// than spending the caller's whole deadline in a retry backoff.
const webTracksAttemptBudget = 6 * time.Second

// savedTracksProbeBudget bounds the check that decides whether the cached
// Liked Songs list is still current. It is an optimisation -- failing it costs
// a re-read, not correctness -- so it must never sit in a Web API backoff for
// the caller's whole deadline, which is how a cooldown gets extended. It is
// spent across both paths, not granted to each: a client attempt that burns it
// leaves the Web API none, which fails the check and re-reads.
const savedTracksProbeBudget = 4 * time.Second

// New creates a SpotifyProvider. If session is nil, authentication is
// deferred until the user first selects the Spotify provider.
// bitrate sets the preferred Spotify stream quality in kbps (96, 160, or 320).
func New(session *Session, clientID string, bitrate int) *SpotifyProvider {
	p := &SpotifyProvider{
		session:          session,
		clientID:         clientID,
		bitrate:          bitrate,
		trackCache:       make(map[string]*playlistCache),
		pending:          make(map[string]*pendingTracks),
		declinedByWeb:    make(map[string]bool),
		declinedByClient: make(map[string]bool),
		apiMode:          resolveAPIMode(),
	}
	p.clientPage = p.contextTracksPage
	return p
}

// ensureSession tries to create a session using stored credentials only
// (no browser). Returns playlist.ErrNeedsAuth if interactive sign-in is needed.
func (p *SpotifyProvider) ensureSession() error {
	p.mu.Lock()
	if p.session != nil {
		p.mu.Unlock()
		return nil
	}
	clientID := p.clientID
	p.mu.Unlock()

	if clientID == "" {
		return fmt.Errorf("spotify: no client ID available")
	}
	sess, err := NewSessionSilent(context.Background(), clientID)
	if err != nil {
		return playlist.ErrNeedsAuth
	}
	p.mu.Lock()
	p.session = sess
	p.resetSessionScopedStateLocked()
	p.mu.Unlock()
	return nil
}

// Authenticate runs the interactive sign-in flow (opens browser, waits for callback).
// Any previous in-progress OAuth flow is cancelled first to free the callback port.
func (p *SpotifyProvider) Authenticate() error {
	p.mu.Lock()
	if p.session != nil {
		p.mu.Unlock()
		return nil
	}
	if p.authCancel != nil {
		p.authCancel()
		p.authCancel = nil
	}
	clientID := p.clientID
	p.mu.Unlock()

	if clientID == "" {
		return fmt.Errorf("spotify: no client ID available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	p.mu.Lock()
	p.authCancel = cancel
	p.mu.Unlock()

	sess, err := NewSession(ctx, clientID)

	p.mu.Lock()
	p.authCancel = nil
	p.mu.Unlock()
	cancel()

	if err != nil {
		return err
	}
	p.mu.Lock()
	p.session = sess
	p.resetSessionScopedStateLocked()
	p.mu.Unlock()
	return nil
}

// Close releases the session if one was created.
func (p *SpotifyProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authCancel != nil {
		p.authCancel()
		p.authCancel = nil
	}
	if p.session != nil {
		p.session.Close()
		p.session = nil
		p.resetSessionScopedStateLocked()
	}
}

// resetSessionScopedStateLocked clears /v1/me-derived caches when the session
// changes. p.mu must be held.
func (p *SpotifyProvider) resetSessionScopedStateLocked() {
	p.userID = ""
	p.meFetched = false
}

func (p *SpotifyProvider) Name() string { return "Spotify" }

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

	// This only decides whether a playlist is labelled "Your" or "Followed", so
	// it gets a deadline of its own. Sharing the caller's meant a throttled
	// /v1/me could spend the whole budget here and leave the pane with nothing
	// to show, having never reached the request that matters.
	idCtx, cancel := context.WithTimeout(ctx, currentUserBudget)
	defer cancel()

	var me struct {
		ID string `json:"id"`
	}
	if resp, err := p.webAPI(idCtx, "GET", "/v1/me", nil); err == nil {
		_ = decodeBody(resp, &me)
	} else {
		applog.Warn("spotify: could not identify the account, playlists will not be split by owner: %v", err)
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

	// The listing is the one place the client protocol leads rather than
	// follows: the Web API does not fail here, it just answers with less --
	// no folders, and none of the entries it declines to serve. Falling back
	// still beats no listing at all, since this is an internal endpoint.
	if p.apiMode.usesClient() {
		lists, err := p.playlistsFromRootlist(ctx)
		if err == nil {
			p.mu.Lock()
			p.listCache = lists
			p.listCacheAt = time.Now()
			p.mu.Unlock()
			return slices.Clone(lists), nil
		}
		applog.Warn("spotify: rootlist unavailable, falling back to the web api: %v", err)
	}

	userID := p.currentUserID(ctx)

	var all []playlist.PlaylistInfo
	offset := 0
	limit := spotifyPlaylistPageSize

	// Liked Songs is not part of /v1/me/playlists, so it is built separately.
	liked, err := p.savedTracksInfo(ctx)
	if err != nil {
		return nil, err
	}
	all = append(all, liked)

	for {
		query := url.Values{
			"limit":  {fmt.Sprintf("%d", limit)},
			"offset": {fmt.Sprintf("%d", offset)},
			"fields": {"items(id,name,snapshot_id,owner(id),items.total),total"},
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
			if userID != "" && item.Owner.ID == userID {
				section = "Your playlists"
			}
			all = append(all, playlist.PlaylistInfo{
				ID:         item.ID,
				Name:       item.Name,
				TrackCount: count,
				Section:    section,
			})
			// The snapshot moved under a list a read may still be paging.
			// Dropping only the resolve would make that read's next page
			// re-resolve against the new snapshot and splice the two into one
			// committed list when the edit left the total alone -- the same
			// reason applyRevisions discards the whole read. A playlist the
			// cache has never seen keeps whatever read is live: there is
			// nothing stale to drop, and deleting its resolve would splice
			// that read for no gain.
			if adoptSnapshot(p.trackCache, item.ID, item.SnapshotID) {
				p.discardLoadLocked(item.ID)
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

	albums, err := p.savedAlbums(ctx)
	if err != nil {
		return nil, err
	}
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

// savedTracksInfo builds the Liked Songs row. Spotify does not list it among
// the playlists and does not expose a localized display name for it, so the
// name is fixed and only the count is fetched -- with limit=1, since the
// response body is discarded apart from the total.
func (p *SpotifyProvider) savedTracksInfo(ctx context.Context) (playlist.PlaylistInfo, error) {
	resp, err := p.webAPI(ctx, "GET", "/v1/me/tracks", url.Values{"limit": {"1"}})
	if err != nil {
		return playlist.PlaylistInfo{}, fmt.Errorf("spotify: your music: %w", err)
	}
	var result struct {
		Total int `json:"total"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return playlist.PlaylistInfo{}, fmt.Errorf("spotify: parse your music: %w", err)
	}
	return playlist.PlaylistInfo{
		ID:         savedTracksPlaylistID,
		Name:       "Your Music",
		TrackCount: result.Total,
		Section:    "Library",
	}, nil
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
		return p.AlbumTracks(albumID)
	}
	// Check cache — if we have tracks and the snapshot_id hasn't changed, return cached.
	p.mu.Lock()
	tracks, cachedTotal, hit := p.cachedTracksLocked(playlistID)
	p.clearDeclinesLocked(playlistID)
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Liked Songs has no snapshot_id to invalidate it, so a committed list
	// would otherwise be served here however old it is -- and this is the entry
	// point the IPC and CLI callers use. Check it the way the paged read does.
	if hit && (playlistID != savedTracksPlaylistID || p.savedTracksCurrent(ctx, tracks, cachedTotal)) {
		return tracks, nil
	}

	// A like or unlike mid-load shifts every later offset, so pages read either
	// side of the change splice into a list short by one and duplicated by one.
	// Restart against the new snapshot when the total moves, bounding restarts
	// so a library being actively edited cannot loop forever.
	const maxRestarts = 2
	var all []playlist.Track
	// uris is this read's own resolve, carried between its pages so every one
	// is sliced from the same snapshot. A restart drops it and takes a new one.
	var uris []string
	total, offset, restarts := -1, 0, 0
	for {
		page, err := p.fetchTracksPage(ctx, playlistID, offset, uris)
		if err != nil {
			return nil, err
		}
		uris = page.uris
		if total < 0 {
			total = page.total
		}
		if page.total != total {
			if restarts == maxRestarts {
				return nil, fmt.Errorf("spotify: list tracks: %q changed while loading", playlistID)
			}
			restarts++
			all, uris, total, offset = nil, nil, -1, 0
			continue
		}
		all = append(all, page.tracks...)
		if offset+page.pageSize >= total {
			break
		}
		offset += page.pageSize
	}

	// Cache the fetched tracks.
	p.mu.Lock()
	p.cacheTracksLocked(playlistID, all, total)
	p.mu.Unlock()

	return slices.Clone(all), nil
}

// fetchTracksPage reads one page of a playlist, preferring the documented Web
// API and reaching for the client protocol only when it refuses. Spotify serves
// the user's own playlists there, so the common case stays on the documented
// path; a playlist owned by someone else, or a Spotify-owned mix, comes back
// 403 and is readable only through the client protocol.
func (p *SpotifyProvider) fetchTracksPage(ctx context.Context, playlistID string, offset int, uris []string) (tracksPage, error) {
	fallback := p.apiMode.usesClient()

	// Once the Web API has refused this list, every later page of the same read
	// would be refused too. Asking anyway costs a request per page -- sixty for
	// a large library -- and hammering an API that is rate limiting is how a
	// cooldown gets extended.
	if fallback && (p.apiMode.skipsWeb() || p.webDeclined(playlistID)) {
		return p.clientPage(ctx, playlistID, offset, uris)
	}

	// Liked Songs leads with the client protocol. The Web API does not refuse
	// it, so the fallback below would never reach it -- but it pages the list
	// fifty at a time, and a library of a few thousand tracks is the request
	// volume that earns a day-long throttle. One resolve replaces all of it.
	// The list cannot lose anything in the move: Spotify keeps saved episodes
	// in a separate collection, so this one holds nothing but tracks.
	if fallback && playlistID == savedTracksPlaylistID && !p.clientDeclined(playlistID) {
		page, err := p.clientPage(ctx, playlistID, offset, uris)
		if err == nil {
			return page, nil
		}
		p.noteClientDeclined(playlistID)
		applog.Warn("spotify: client protocol declined saved tracks (%v), trying the web api", err)
	}

	webCtx := ctx
	if fallback {
		// A refusal comes back at once, but a throttled Web API can sit in its
		// retry backoff for the caller's whole deadline. With somewhere else to
		// go, waiting that out helps nobody: bound the attempt and move on.
		var cancel context.CancelFunc
		webCtx, cancel = context.WithTimeout(ctx, webTracksAttemptBudget)
		defer cancel()
	}

	tracks, total, err := p.webTracksPage(webCtx, playlistID, offset)
	if err == nil {
		// The Web API pages the list itself and caps a page at fifty, and
		// leaves the read nothing to carry between pages.
		return tracksPage{tracks: tracks, total: total, pageSize: spotifyTrackPageSize}, nil
	}
	if !fallback {
		return tracksPage{}, err
	}
	// Pages read before and after this switch are spliced into one list, so the
	// two paths must enumerate a playlist in the same order. Both are
	// newest-first: the resolve was measured descending by added_at across a
	// 6078-track collection with no violations, and /v1/me/tracks documents the
	// same. If that ever diverges, an edit that leaves the total unchanged
	// would commit a list spliced from two orderings.
	applog.Warn("spotify: web api declined %q (%v), trying the client protocol", playlistID, err)
	p.noteWebDeclined(playlistID)
	page, cerr := p.clientPage(ctx, playlistID, offset, uris)
	if cerr != nil {
		// Report the Web API's refusal: it is the documented path and its error
		// says why the playlist is unreadable.
		applog.Warn("spotify: client protocol also failed for %q: %v", playlistID, cerr)
		return tracksPage{}, err
	}
	return page, nil
}

// webTracksPage reads one page of a playlist's tracks through the Web API and
// returns it with the list's current total. Saved tracks and playlist items
// come from different endpoints with different shapes, so this is the single
// place that difference lives. Items without an ID -- local files, unavailable
// tracks -- are skipped, so the returned slice is usually shorter than the page
// size and the caller must advance by the page size rather than by len(tracks).
func (p *SpotifyProvider) webTracksPage(ctx context.Context, playlistID string, offset int) ([]playlist.Track, int, error) {
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

// webDeclined reports whether the Web API has already refused this list during
// the read in progress.
func (p *SpotifyProvider) webDeclined(playlistID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.declinedByWeb[playlistID]
}

// noteWebDeclined records a refusal so the remaining pages of this read go
// straight to the client protocol. It is scoped to the read, like the resolved
// URIs, so reopening the list asks the Web API again.
func (p *SpotifyProvider) noteWebDeclined(playlistID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.declinedByWeb[playlistID] = true
}

// adoptSnapshot reconciles a cached playlist with the snapshot_id the Web API
// listing just reported. An entry carrying a revision was seeded by the client
// protocol, which versions playlists differently, so its missing snapshot is
// not evidence of a change and is adopted. An entry with neither id has no
// provenance -- a read that completed before any listing -- and is dropped, or
// a list committed before an edit would adopt the snapshot taken after it and
// never be invalidated again. It reports whether the entry was dropped; a
// playlist the cache has never seen is not one, and whatever read is paging it
// right now must be left alone.
func adoptSnapshot(cache map[string]*playlistCache, playlistID, snapshotID string) (dropped bool) {
	cached, ok := cache[playlistID]
	if !ok {
		return false
	}
	switch {
	case cached.snapshotID == "" && cached.revision != "":
		cached.snapshotID = snapshotID
	case cached.snapshotID != snapshotID:
		delete(cache, playlistID)
		return true
	}
	return false
}

// clearDeclinesLocked forgets which paths refused a playlist. The flags exist
// to stop one read asking a refusing path once per page, so they are cleared
// when a read starts: letting them outlive their read would let one transient
// failure route every later read down the other path for the life of the
// process, and for Liked Songs the other path is the one that pages fifty at a
// time, so recovering would cost more than the failure did. p.mu must be held.
func (p *SpotifyProvider) clearDeclinesLocked(playlistID string) {
	delete(p.declinedByWeb, playlistID)
	delete(p.declinedByClient, playlistID)
}

// clientDeclined and noteClientDeclined are the mirror of the two above, for
// the lists the client protocol leads. Without them a failing resolve would be
// retried once per page on the way to the Web API, which is the cost the Web
// API's own stickiness exists to avoid.
func (p *SpotifyProvider) clientDeclined(playlistID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.declinedByClient[playlistID]
}

func (p *SpotifyProvider) noteClientDeclined(playlistID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.declinedByClient[playlistID] = true
}

// discardLoadLocked drops everything scoped to one read of a playlist: the
// partial accumulation, and the resolved track URIs behind it. The URI list is
// a snapshot of the playlist taken when the read began, so it must not outlive
// the read -- keeping it would serve an edited playlist from stale contents
// that the snapshot pin cannot detect, since the length it compares comes from
// the same stale list. p.mu must be held.
func (p *SpotifyProvider) discardLoadLocked(playlistID string) {
	delete(p.pending, playlistID)
	p.clearDeclinesLocked(playlistID)
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

// savedTracksUnchangedClient answers the same question as savedTracksUnchanged
// over the client protocol. The cached list is filled from a context resolve,
// so proving it current with a resolve costs no Web API quota and keeps working
// while that quota is exhausted -- which is exactly when a cache is worth most.
// The resolve is taken fresh and then dropped: a stored one would outlive the
// check and could serve a later read from a snapshot nothing revalidated.
func (p *SpotifyProvider) savedTracksUnchangedClient(ctx context.Context, tracks []playlist.Track, total int) (bool, error) {
	// A read already paging this list is about to replace the cached one, so
	// proving the cache current would spend a resolve on an answer nobody will
	// use. Report unproven and let that read finish.
	p.mu.Lock()
	live := p.pending[savedTracksPlaylistID] != nil
	p.mu.Unlock()
	if live {
		return false, nil
	}

	// This resolve belongs to the check alone and goes out of scope with it.
	uris, err := p.contextTrackURIs(ctx, savedTracksPlaylistID)
	if err != nil {
		return false, err
	}
	if len(uris) != total {
		return false, nil
	}
	if len(uris) == 0 {
		return len(tracks) == 0, nil
	}
	return len(tracks) > 0 && uris[0] == tracks[0].Path, nil
}

// savedTracksCurrent reports whether the cached Liked Songs list still matches
// the library, asking whichever path the mode allows. A path that cannot answer
// is not a reason to serve a list nothing checked, so an unanswerable check
// reads as changed and the list is re-read.
func (p *SpotifyProvider) savedTracksCurrent(ctx context.Context, tracks []playlist.Track, total int) bool {
	probeCtx, cancel := context.WithTimeout(ctx, savedTracksProbeBudget)
	defer cancel()

	if p.apiMode.usesClient() {
		unchanged, err := p.savedTracksUnchangedClient(probeCtx, tracks, total)
		if err == nil {
			return unchanged
		}
		applog.Warn("spotify: client protocol could not check saved tracks (%v), asking the web api", err)
		if p.apiMode.skipsWeb() {
			return false
		}
	}
	return p.savedTracksUnchanged(probeCtx, tracks, total)
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
		return tracks, 0, err
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

	if hit && (playlistID != savedTracksPlaylistID || p.savedTracksCurrent(ctx, tracks, cachedTotal)) {
		return tracks, 0, nil
	}
	// Page zero takes a fresh resolve rather than the abandoned read's, so a
	// resume is proved against the library now rather than against the snapshot
	// that read left behind, agreeing with itself.
	if offset == 0 {
		p.mu.Lock()
		p.clearDeclinesLocked(playlistID)
		p.mu.Unlock()
	}

	// A continuation slices the resolve its own read started from. Page zero
	// passes none, so it takes a fresh one. The accumulation itself is carried
	// by pointer: the fetch below runs unlocked, and a page-zero re-entry can
	// replace the accumulation while the page is in flight -- so at commit the
	// page must be proven to belong to the accumulation still standing, not
	// merely to one that was standing when the page was asked for.
	var carried *pendingTracks
	var carriedURIs []string
	if offset > 0 {
		p.mu.Lock()
		if pend := p.pending[playlistID]; pend != nil && pend.want == offset {
			carried = pend
			carriedURIs = pend.uris
		}
		p.mu.Unlock()
	}

	result, err := p.fetchTracksPage(ctx, playlistID, offset, carriedURIs)
	if err != nil {
		return nil, 0, err
	}
	page, total := result.tracks, result.total

	// Page size follows whichever path served this page: the Web API pages the
	// list and caps at fifty, the client protocol batches metadata instead.
	next := offset + result.pageSize
	if next >= total {
		next = 0
	}

	// Whether an abandoned accumulation can be resumed may need a request, so
	// settle it before taking the lock the accumulation is guarded by. The
	// proof is against this accumulation by pointer: it can also be replaced
	// while the probe is in flight, and adopting into a stranger would hand it
	// a resolve its pages never came from.
	resumable := false
	var proofPend *pendingTracks
	if offset == 0 {
		p.mu.Lock()
		pend := p.pending[playlistID]
		proofPend = pend
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
		if resumable && pend != nil && pend == proofPend {
			// The fresh resolve just proved equal to the accumulation at total
			// and head, so the remaining pages come from it rather than from
			// the one the abandoned read was holding.
			pend.uris = result.uris
			return slices.Clone(pend.tracks), pend.want, nil
		}
		pend = &pendingTracks{total: total, snapshot: p.snapshotIDLocked(playlistID), uris: result.uris}
		p.pending[playlistID] = pend
	}
	// A page at an offset this accumulation is not waiting for belongs to a
	// superseded chain: serve it to its caller, but do not accumulate it. That
	// includes a continuation whose accumulation was replaced while its page
	// was in flight -- its pages come from the replaced read's resolve, and a
	// same-total edit is exactly why the replacement happened, which no total
	// check could catch.
	if pend == nil || pend.want != offset || (offset > 0 && pend != carried) {
		return page, next, nil
	}
	// The live chain's own page reporting a different total means the library
	// moved under it. Every later page would mismatch the pinned snapshot too,
	// so the load can never commit -- stop now rather than spending the rest of
	// the pages on a result that is already discarded.
	// Only a Web-served read can reach this: the client path slices every page
	// from one resolve, so its pages always agree on the total.
	if pend.total != total {
		p.discardLoadLocked(playlistID)
		return nil, 0, fmt.Errorf("spotify: list tracks %q: %w", playlistID, playlist.ErrListChanged)
	}
	pend.tracks = append(pend.tracks, page...)
	pend.want = next
	if next == 0 {
		p.cacheTracksLocked(playlistID, pend.tracks, total)
		p.discardLoadLocked(playlistID)
	}
	return page, next, nil
}

// isAuthError returns true if the error is an authentication/session-related
// failure that can be resolved by re-authenticating.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}

	// context.DeadlineExceeded and context.Canceled are NOT auth errors.
	// They commonly fire during rapid track skipping when a previous NewStream's
	// network fetch is interrupted, and previously caused spurious re-auth
	// attempts (which then escalated to opening a browser tab mid-skip).
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var keyErr *audio.KeyProviderError
	return errors.As(err, &keyErr)
}

// URISchemes returns the URI prefixes handled by this provider.
// Implements provider.CustomStreamer.
func (p *SpotifyProvider) URISchemes() []string { return []string{"spotify:"} }

// NewStreamer creates a SpotifyStreamer for the given spotify: URI (track or
// episode).
// If the stream fails due to an auth error (e.g. expired session, AES key
// rejection), the player tries a silent reconnect from cached credentials.
// If that fails — or the retry still hits an auth error — the streamer
// surfaces playlist.ErrNeedsAuth so the UI can prompt the user to sign in.
// We deliberately do NOT auto-launch a browser-based OAuth flow from this
// path: rapid track skipping can produce transient stream errors and a
// browser tab popping up mid-skip.
//
// Implements provider.CustomStreamer.
func (p *SpotifyProvider) NewStreamer(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	if err := p.ensureSession(); err != nil {
		return nil, beep.Format{}, 0, err
	}
	spotID, err := librespot.SpotifyIdFromUri(uri)
	if err != nil {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: invalid URI %q: %w", uri, err)
	}

	tryStream := func() (*spotifyStreamer, error) {
		ctx, setupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer setupCancel()
		stream, streamCancel, err := p.session.NewStream(ctx, *spotID, p.bitrate)
		if err != nil {
			return nil, err
		}
		return newSpotifyStreamer(stream, streamCancel), nil
	}

	s, err := tryStream()
	if err == nil {
		return s, s.Format(), s.Duration(), nil
	}
	if !isAuthError(err) {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: new stream: %w", err)
	}

	// Auth error — try a silent reconnect from cached credentials.
	applog.UserWarn("spotify: stream auth error (%v), attempting silent reconnect...", err)

	reconnCtx, reconnCancel := context.WithTimeout(context.Background(), 30*time.Second)
	reconnErr := p.session.Reconnect(reconnCtx)
	reconnCancel()

	if reconnErr != nil {
		applog.UserWarn("spotify: silent reconnect failed (%v); sign-in required", reconnErr)
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: stream auth error, silent reconnect failed: %w", playlist.ErrNeedsAuth)
	}

	s, err = tryStream()
	if err == nil {
		return s, s.Format(), s.Duration(), nil
	}
	if !isAuthError(err) {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: new stream after silent reconnect: %w", err)
	}

	// Still failing after a silent reconnect — surface ErrNeedsAuth so the
	// UI can prompt the user to sign in. Do NOT open a browser from here.
	applog.UserWarn("spotify: stream still failing after silent reconnect (%v); sign-in required", err)
	return nil, beep.Format{}, 0, fmt.Errorf("spotify: stream auth error after silent reconnect: %w", playlist.ErrNeedsAuth)
}

// Rate-limit handling mirrors external/tidal: cap what a Retry-After can ask
// for, and retry only a couple of times. Spotify escalates a cooldown when it
// is asked again during one -- a second becomes a minute becomes a day -- so
// waiting out a long hold is both useless and harmful. go-librespot, which
// cliamp already uses for playback, reaches the same conclusion for the same
// endpoint family: "4xx isn't transient: retrying (especially a 429) just adds
// load [...] a 429 carries a cooldown".
// currentUserBudget bounds the account lookup that only labels sections.
const currentUserBudget = 5 * time.Second

const (
	rateLimitRetries = 2
	rateLimitWaitCap = 60 * time.Second
)

// retryAfter returns how long the server asked the caller to wait, or zero when
// it did not say. Values beyond the cap are returned as asked so the caller can
// report them; they are not waited out.
func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		// RFC 9110 also allows an HTTP-date. Read as zero, it looked like no
		// wait at all and the caller retried after a second.
		if when, derr := http.ParseTime(v); derr == nil {
			if d := time.Until(when); d > 0 {
				return d
			}
		}
		return 0
	}
	return time.Duration(secs) * time.Second
}

// webAPI calls the Spotify Web API via the session with retry on 429.
func (p *SpotifyProvider) webAPI(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	return p.webAPIWithBody(ctx, method, path, query, nil, "", http.StatusOK)
}

// webAPIWithBody is like webAPI but accepts an optional request body, content type,
// and a set of acceptable HTTP status codes (e.g. 200, 201). Retries 429 with
// exponential backoff (honoring Retry-After when present).
func (p *SpotifyProvider) webAPIWithBody(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string, acceptStatus ...int) (*http.Response, error) {

	// Buffer the body so it can be replayed on retry.
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		resp, err := p.session.webApiWithBody(ctx, method, path, query, reqBody, contentType)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			asked := retryAfter(resp.Header)

			// Asking again during a cooldown is what makes Spotify extend it:
			// a one second hold becomes a minute, then a day. Retry only a
			// hold short enough to be worth waiting out, and only twice.
			if attempt >= rateLimitRetries || asked > rateLimitWaitCap {
				applog.UserWarn("spotify: rate limited on %s, asked to wait %v", path, asked)
				return nil, fmt.Errorf("spotify: rate limited on %s, retry in %v: %w", path, asked, playlist.ErrRateLimited)
			}

			wait := asked
			if wait <= 0 {
				wait = time.Duration(attempt+1) * time.Second
			}
			applog.UserWarn("spotify: rate limited on %s, retrying in %v (attempt %d/%d)", path, wait, attempt+1, rateLimitRetries)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
				continue
			}
		}

		ok := slices.Contains(acceptStatus, resp.StatusCode)
		if !ok {
			respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			if readErr != nil {
				return nil, fmt.Errorf("http status %s (failed to read body: %v)", resp.Status, readErr)
			}
			return nil, fmt.Errorf("http status %s: %s", resp.Status, string(respBody))
		}
		return resp, nil
	}
}

// devModeSearchLimit is the largest per-request limit /v1/search accepts for an
// app in Spotify's Development Mode. SearchTracks uses it for every request so
// personal client IDs never need a rejected probe before pagination starts.
const devModeSearchLimit = 10

func isInvalidLimit(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "400") && strings.Contains(msg, "Invalid limit")
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
	if p.clientID == DefaultClientID {
		// Preserve one-request searches for the shared legacy client. Fall back
		// to Development Mode pages if Spotify applies the new cap to it later.
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

// AlbumTracks returns every track of a Spotify album, in disc and track order.
// Implements provider.AlbumTrackLoader, so an album placeholder from
// SearchTracks can be expanded into a playable list.
//
// /v1/albums/{id}/tracks returns simplified track objects that omit the album
// they belong to, so the album's own name, artist and release year are fetched
// once and filled in on every track for display.
func (p *SpotifyProvider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return p.AlbumTracksContext(ctx, albumID)
}

// AlbumTracksContext returns every track of a Spotify album with caller-controlled cancellation.
func (p *SpotifyProvider) AlbumTracksContext(ctx context.Context, albumID string) ([]playlist.Track, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	album, err := p.album(ctx, albumID)
	if err != nil {
		return nil, err
	}
	placeholder := albumFromItem(album)

	var tracks []playlist.Track
	for offset := 0; ; offset += spotifyTrackPageSize {
		page, err := p.albumTracksPage(ctx, albumID, offset)
		if err != nil {
			return nil, err
		}
		for _, item := range page {
			if item == nil || item.ID == "" {
				continue // skip null/unavailable results
			}
			track := trackFromItem(item)
			track.Album = placeholder.Album
			track.Year = placeholder.Year
			if track.Artist == "" {
				track.Artist = placeholder.Artist
			}
			tracks = append(tracks, track)
		}
		if len(page) < spotifyTrackPageSize {
			break
		}
	}
	return tracks, nil
}

// album fetches an album's own metadata.
func (p *SpotifyProvider) album(ctx context.Context, albumID string) (*spotifyAlbumItem, error) {
	resp, err := p.webAPI(ctx, "GET", "/v1/albums/"+url.PathEscape(albumID), nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: album %s: %w", albumID, err)
	}
	var album spotifyAlbumItem
	if err := decodeBody(resp, &album); err != nil {
		return nil, fmt.Errorf("spotify: parse album %s: %w", albumID, err)
	}
	return &album, nil
}

// albumTracksPage fetches one page of an album's track list.
func (p *SpotifyProvider) albumTracksPage(ctx context.Context, albumID string, offset int) ([]*spotifyItem, error) {
	q := url.Values{
		"limit": {strconv.Itoa(spotifyTrackPageSize)},
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}

	resp, err := p.webAPI(ctx, "GET", "/v1/albums/"+url.PathEscape(albumID)+"/tracks", q)
	if err != nil {
		return nil, fmt.Errorf("spotify: album %s tracks: %w", albumID, err)
	}
	var page struct {
		Items []*spotifyItem `json:"items"`
	}
	if err := decodeBody(resp, &page); err != nil {
		return nil, fmt.Errorf("spotify: parse album %s tracks: %w", albumID, err)
	}
	return page.Items, nil
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

	resp, err := p.webAPIWithBody(ctx, "POST", path, nil, bytes.NewReader(body), "application/json", http.StatusOK, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("spotify: add track: %w", err)
	}
	resp.Body.Close()

	// Invalidate caches for this playlist.
	p.mu.Lock()
	delete(p.trackCache, playlistID)
	p.discardLoadLocked(playlistID)
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

	resp, err := p.webAPIWithBody(ctx, "POST", "/v1/me/playlists", nil, bytes.NewReader(body), "application/json", http.StatusOK, http.StatusCreated)
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

// decodeBody reads and decodes a JSON response body, then closes it.
func decodeBody(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(v)
}
