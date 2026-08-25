package ytmusic

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/playlist"

	"google.golang.org/api/youtube/v3"
)

// itemInfo holds metadata for a single video in a playlist.
type itemInfo struct {
	videoID string
	title   string
	channel string
}

// youtubeAPIBatchSize is the maximum number of items per YouTube Data API request.
const youtubeAPIBatchSize = 50

// baseProvider holds shared state for YouTube and YouTube Music providers.
// Both providers share the same OAuth session and track cache.
type baseProvider struct {
	session      *Session
	clientID     string
	clientSecret string
	hasCookies   bool // true when cookies_from is configured
	mu           sync.Mutex
	trackCache   map[string][]playlist.Track // playlist ID -> cached tracks
	allPlaylists []playlistEntry             // cached raw playlist list
	classified   map[string]bool             // playlist ID -> is music (from classify.go)
	disk         *ytCache                    // lazy-loaded disk cache
	cacheScope   string                      // immutable identity of the active OAuth account
	authCancel   context.CancelFunc          // cancels any in-progress OAuth flow
}

func newBase(session *Session, clientID, clientSecret string, hasCookies bool) *baseProvider {
	cacheScope := storedOAuthCacheScope(strings.TrimSpace(clientID))
	if session != nil && session.cacheScope != "" {
		cacheScope = session.cacheScope
	}
	return &baseProvider{
		session:      session,
		clientID:     clientID,
		clientSecret: clientSecret,
		hasCookies:   hasCookies,
		trackCache:   make(map[string][]playlist.Track),
		cacheScope:   cacheScope,
	}
}

// ensureDiskCache lazily loads the disk cache. Must be called under mu.
func (b *baseProvider) ensureDiskCache() *ytCache {
	if b.disk == nil {
		b.disk = loadYTCache(b.cacheScope)
	}
	return b.disk
}

// initSession creates a session if one doesn't exist yet. If interactive is
// false, only stored credentials are tried (returning ErrNeedsAuth on failure).
// If interactive is true, a browser-based OAuth flow is started. Any previous
// in-progress OAuth flow is cancelled first to free the callback port.
func (b *baseProvider) initSession(interactive bool) error {
	b.mu.Lock()
	if b.session != nil {
		b.mu.Unlock()
		return nil
	}
	// Cancel any previous in-progress auth attempt so the old listener
	// on CallbackPort is released before we try to bind again.
	if b.authCancel != nil {
		b.authCancel()
		b.authCancel = nil
	}
	clientID := b.clientID
	clientSecret := b.clientSecret
	b.mu.Unlock()

	if clientID == "" {
		return fmt.Errorf("ytmusic: no client ID available")
	}

	var sess *Session
	var err error
	if interactive {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		b.mu.Lock()
		b.authCancel = cancel
		b.mu.Unlock()

		sess, err = NewSession(ctx, clientID, clientSecret)

		b.mu.Lock()
		b.authCancel = nil
		b.mu.Unlock()
		cancel()
	} else {
		sess, err = NewSessionSilent(context.Background(), clientID, clientSecret)
	}
	if err != nil {
		if !interactive {
			return playlist.ErrNeedsAuth
		}
		return err
	}

	b.mu.Lock()
	if b.session == nil {
		if b.cacheScope != sess.cacheScope {
			b.allPlaylists = nil
			b.classified = nil
			clear(b.trackCache)
		}
		b.session = sess
		b.cacheScope = sess.cacheScope
		b.disk = nil
	}
	b.mu.Unlock()
	return nil
}

func (b *baseProvider) ensureSession() error { return b.initSession(false) }
func (b *baseProvider) authenticate() error  { return b.initSession(true) }

// refresh clears playlist/track caches so the next call re-fetches from the
// API. Classification is preserved — it's on disk in a separate file, rarely
// changes, and re-classifying costs API quota.
func (b *baseProvider) refresh() {
	b.mu.Lock()
	b.allPlaylists = nil
	b.classified = nil
	clear(b.trackCache)
	dc := b.ensureDiskCache()
	dc.clear()
	snap := dc.snapshot()
	b.mu.Unlock()

	saveSnapshot(snap)
}

func (b *baseProvider) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.authCancel != nil {
		b.authCancel()
		b.authCancel = nil
	}
	if b.session != nil {
		b.session.Close()
		b.session = nil
	}
}

// fetchAndClassify loads all playlists and classifies them as music vs non-music.
// Results are cached in-memory for the session and on disk for fast startup.
// If fresh disk cache exists with full classification, no API calls or auth are needed.
func (b *baseProvider) fetchAndClassify() error {
	b.mu.Lock()
	if b.allPlaylists != nil {
		b.mu.Unlock()
		return nil
	}

	// Try disk cache first — no session/auth needed if data is fresh.
	dc := b.ensureDiskCache()
	if dc.playlistsFresh() {
		classified := loadClassification(b.cacheScope)
		if classified != nil {
			allClassified := true
			for _, pl := range dc.Playlists {
				if _, ok := classified[pl.ID]; !ok {
					allClassified = false
					break
				}
			}
			if allClassified {
				b.allPlaylists = dc.Playlists
				b.classified = classified
				b.mu.Unlock()
				return nil
			}
		}
	}
	b.mu.Unlock()

	// Disk cache stale or incomplete — fetch from API.
	if err := b.ensureSession(); err != nil {
		return err
	}

	b.mu.Lock()
	sess := b.session
	cacheScope := b.cacheScope
	b.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("ytmusic: session unavailable")
	}
	svc := sess.Service()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var all []playlistEntry
	seen := make(map[string]bool)

	pageToken := ""
	for {
		call := svc.Playlists.List([]string{"snippet", "contentDetails"}).
			Mine(true).
			MaxResults(50).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		resp, err := call.Do()
		if err != nil {
			return fmt.Errorf("ytmusic: list playlists: %w", err)
		}

		for _, item := range resp.Items {
			count := int(item.ContentDetails.ItemCount)
			if count <= 0 || seen[item.Id] {
				continue
			}
			seen[item.Id] = true
			all = append(all, playlistEntry{
				ID:         item.Id,
				Name:       item.Snippet.Title,
				TrackCount: count,
			})
		}

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	// Classify playlists (parallel, with disk cache).
	// Pass nil to let classifyPlaylists load from disk itself —
	// the earlier loadClassification() was skipped because cache was stale.
	classified := classifyWithTimeout(svc, all, 60*time.Second, nil, cacheScope)

	b.mu.Lock()
	if b.allPlaylists != nil {
		// Another goroutine completed fetchAndClassify while we were fetching.
		b.mu.Unlock()
		return nil
	}
	b.allPlaylists = all
	b.classified = classified
	// Persist playlists to disk cache.
	dc = b.ensureDiskCache()
	dc.setPlaylists(all)
	snap := dc.snapshot()
	b.mu.Unlock()

	// Save outside the lock — disk I/O shouldn't block other goroutines.
	saveSnapshot(snap)

	return nil
}

// filteredPlaylists returns playlists filtered by music classification.
func (b *baseProvider) filteredPlaylists(wantMusic bool) []playlist.PlaylistInfo {
	b.mu.Lock()
	defer b.mu.Unlock()

	var result []playlist.PlaylistInfo
	for _, pl := range b.allPlaylists {
		isMusic, ok := b.classified[pl.ID]
		if !ok {
			isMusic = false
		}
		if isMusic == wantMusic {
			result = append(result, playlist.PlaylistInfo{
				ID:         pl.ID,
				Name:       pl.Name,
				TrackCount: pl.TrackCount,
			})
		}
	}
	return result
}

// tracks fetches tracks for a playlist (shared between both providers).
// Checks in-memory cache, then disk cache, then fetches from API.
func (b *baseProvider) tracks(playlistID string) ([]playlist.Track, error) {
	b.mu.Lock()
	if cached, ok := b.trackCache[playlistID]; ok {
		b.mu.Unlock()
		return cached, nil
	}

	// Check disk cache — avoids API call and auth if fresh.
	dc := b.ensureDiskCache()
	if tracks, ok := dc.tracksFresh(playlistID); ok {
		b.trackCache[playlistID] = tracks
		b.mu.Unlock()
		return tracks, nil
	}
	b.mu.Unlock()

	// Disk cache miss — fetch from API.
	if err := b.ensureSession(); err != nil {
		return nil, err
	}

	b.mu.Lock()
	sess := b.session
	b.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("ytmusic: session unavailable")
	}
	svc := sess.Service()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var items []itemInfo

	pageToken := ""
	for {
		call := svc.PlaylistItems.List([]string{"snippet", "contentDetails"}).
			PlaylistId(playlistID).
			MaxResults(50).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("ytmusic: list playlist items: %w", err)
		}

		for _, item := range resp.Items {
			vid := item.ContentDetails.VideoId
			if vid == "" {
				continue
			}
			title := item.Snippet.Title
			if title == "Private video" || title == "Deleted video" {
				continue
			}
			channel := item.Snippet.VideoOwnerChannelTitle
			if !b.hasCookies && (channel == "Music Library Uploads" || channel == "") {
				continue
			}
			items = append(items, itemInfo{
				videoID: vid,
				title:   title,
				channel: channel,
			})
		}

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	durations := b.fetchDurations(ctx, svc, items)

	var tracks []playlist.Track
	for _, it := range items {
		tracks = append(tracks, playlist.Track{
			Path:         "https://music.youtube.com/watch?v=" + it.videoID,
			Title:        it.title,
			Artist:       cleanChannelName(it.channel),
			Stream:       false,
			DurationSecs: durations[it.videoID],
		})
	}

	// Persist to in-memory and disk cache.
	b.mu.Lock()
	b.trackCache[playlistID] = tracks
	dc = b.ensureDiskCache()
	dc.setTracks(playlistID, tracks)
	snap := dc.snapshot()
	b.mu.Unlock()

	// Save outside the lock — disk I/O shouldn't block other goroutines.
	saveSnapshot(snap)

	return tracks, nil
}

func (b *baseProvider) fetchDurations(ctx context.Context, svc *youtube.Service, items []itemInfo) map[string]int {
	var mu sync.Mutex
	durations := make(map[string]int)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // limit concurrent API calls

	for i := 0; i < len(items); i += youtubeAPIBatchSize {
		end := min(i+youtubeAPIBatchSize, len(items))
		batch := items[i:end]

		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			var ids []string
			for _, it := range batch {
				ids = append(ids, it.videoID)
			}
			vResp, err := svc.Videos.List([]string{"contentDetails"}).
				Id(ids...).
				Context(ctx).
				Do()
			if err != nil {
				return
			}
			mu.Lock()
			for _, v := range vResp.Items {
				durations[v.Id] = parseISO8601Duration(v.ContentDetails.Duration)
			}
			mu.Unlock()
		})
	}

	wg.Wait()
	return durations
}

// Special auto-generated playlist IDs exposed by the YouTube Data API.
const (
	playlistIDLikedMusic  = "LM" // YouTube Music liked songs
	playlistIDLikedVideos = "LL" // YouTube liked videos
)

// playlistCounts fetches item counts for the given playlist IDs in a single
// API call. IDs not exposed by the API are omitted from the result.
// Returns an empty map when no session is available (e.g. disk-cache-only boot).
func (b *baseProvider) playlistCounts(ids ...string) map[string]int {
	out := make(map[string]int, len(ids))
	if len(ids) == 0 {
		return out
	}
	b.mu.Lock()
	sess := b.session
	b.mu.Unlock()
	if sess == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := sess.Service().Playlists.List([]string{"contentDetails"}).
		Id(ids...).
		Context(ctx).
		Do()
	if err != nil {
		return out
	}
	for _, item := range resp.Items {
		out[item.Id] = int(item.ContentDetails.ItemCount)
	}
	return out
}

// ─── YouTube Music Provider ────────────────────────────────────────────────

// YouTubeMusicProvider shows playlists classified as music content.
type YouTubeMusicProvider struct {
	base *baseProvider
}

func (p *YouTubeMusicProvider) Name() string        { return "YouTube Music" }
func (p *YouTubeMusicProvider) Authenticate() error { return p.base.authenticate() }
func (p *YouTubeMusicProvider) Close()              { p.base.close() }
func (p *YouTubeMusicProvider) Refresh()            { p.base.refresh() }
func (p *YouTubeMusicProvider) Tracks(id string) ([]playlist.Track, error) {
	return p.base.tracks(id)
}

func (p *YouTubeMusicProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	if err := p.base.fetchAndClassify(); err != nil {
		return nil, err
	}

	counts := p.base.playlistCounts(playlistIDLikedMusic)
	all := []playlist.PlaylistInfo{{
		ID:         playlistIDLikedMusic,
		Name:       "Liked Music",
		TrackCount: counts[playlistIDLikedMusic],
	}}
	all = append(all, p.base.filteredPlaylists(true)...)
	return all, nil
}

// ─── YouTube Provider ──────────────────────────────────────────────────────

// YouTubeProvider shows playlists classified as non-music (video) content.
type YouTubeProvider struct {
	base *baseProvider
}

func (p *YouTubeProvider) Name() string        { return "YouTube" }
func (p *YouTubeProvider) Authenticate() error { return p.base.authenticate() }
func (p *YouTubeProvider) Close()              { /* shared base; closed via music provider */ }
func (p *YouTubeProvider) Refresh()            { p.base.refresh() }
func (p *YouTubeProvider) Tracks(id string) ([]playlist.Track, error) {
	return p.base.tracks(id)
}

func (p *YouTubeProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	if err := p.base.fetchAndClassify(); err != nil {
		return nil, err
	}

	counts := p.base.playlistCounts(playlistIDLikedVideos)
	all := []playlist.PlaylistInfo{{
		ID:         playlistIDLikedVideos,
		Name:       "Liked Videos",
		TrackCount: counts[playlistIDLikedVideos],
	}}
	all = append(all, p.base.filteredPlaylists(false)...)
	return all, nil
}

// ─── YouTube All Provider ──────────────────────────────────────────────────

// YouTubeAllProvider shows all playlists regardless of classification.
type YouTubeAllProvider struct {
	base *baseProvider
}

func (p *YouTubeAllProvider) Name() string        { return "YouTube (All)" }
func (p *YouTubeAllProvider) Authenticate() error { return p.base.authenticate() }
func (p *YouTubeAllProvider) Close()              { /* shared base; closed via music provider */ }
func (p *YouTubeAllProvider) Refresh()            { p.base.refresh() }
func (p *YouTubeAllProvider) Tracks(id string) ([]playlist.Track, error) {
	return p.base.tracks(id)
}

func (p *YouTubeAllProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	if err := p.base.fetchAndClassify(); err != nil {
		return nil, err
	}

	counts := p.base.playlistCounts(playlistIDLikedMusic, playlistIDLikedVideos)
	all := []playlist.PlaylistInfo{
		{ID: playlistIDLikedMusic, Name: "Liked Music", TrackCount: counts[playlistIDLikedMusic]},
		{ID: playlistIDLikedVideos, Name: "Liked Videos", TrackCount: counts[playlistIDLikedVideos]},
	}

	b := p.base
	b.mu.Lock()
	for _, pl := range b.allPlaylists {
		all = append(all, playlist.PlaylistInfo{
			ID:         pl.ID,
			Name:       pl.Name,
			TrackCount: pl.TrackCount,
		})
	}
	b.mu.Unlock()

	return all, nil
}

// ─── Constructor ───────────────────────────────────────────────────────────

// Providers holds the YouTube Music, YouTube, and YouTube All providers,
// sharing a single OAuth session.
type Providers struct {
	Music *YouTubeMusicProvider
	Video *YouTubeProvider
	All   *YouTubeAllProvider
}

// New creates all three YouTube providers with a shared session.
func New(session *Session, clientID, clientSecret string, hasCookies bool) Providers {
	base := newBase(session, clientID, clientSecret, hasCookies)
	return Providers{
		Music: &YouTubeMusicProvider{base: base},
		Video: &YouTubeProvider{base: base},
		All:   &YouTubeAllProvider{base: base},
	}
}

// ─── Helpers ───────────────────────────────────────────────────────────────

var iso8601Re = regexp.MustCompile(`P(?:(\d+)D)?T?(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?`)

func parseISO8601Duration(d string) int {
	m := iso8601Re.FindStringSubmatch(d)
	if m == nil {
		return 0
	}
	var total int
	if m[1] != "" {
		v, _ := strconv.Atoi(m[1])
		total += v * 86400
	}
	if m[2] != "" {
		v, _ := strconv.Atoi(m[2])
		total += v * 3600
	}
	if m[3] != "" {
		v, _ := strconv.Atoi(m[3])
		total += v * 60
	}
	if m[4] != "" {
		v, _ := strconv.Atoi(m[4])
		total += v
	}
	return total
}

func cleanChannelName(name string) string {
	name = strings.TrimSuffix(name, " - Topic")
	return name
}
