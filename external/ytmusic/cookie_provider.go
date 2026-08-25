package ytmusic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
)

// Compile-time interface checks.
var (
	_ playlist.Provider  = (*CookieProvider)(nil)
	_ provider.Searcher  = (*CookieProvider)(nil)
	_ playlist.Refresher = (*CookieProvider)(nil)
	_ provider.Closer    = (*CookieProvider)(nil)
)

// ProviderKind indicates the flavor of the YouTube provider.
type ProviderKind int

const (
	KindMusic ProviderKind = iota
	KindVideo
	KindAll
)

type cookieBase struct {
	browser     string
	fetchFn     func(browser string) ([]playlist.PlaylistInfo, error)
	resolveFn   func(ctx context.Context, pageURL string, start, count int, browser ...string) ([]playlist.Track, int, error)
	mu          sync.Mutex
	playlists   []playlist.PlaylistInfo
	trackCache  map[string][]playlist.Track
	generation  uint64
	nextLoad    uint64
	loadCancels map[uint64]context.CancelFunc
}

const (
	cookiePlaylistBatchSize   = 100
	cookiePlaylistLoadTimeout = 5 * time.Minute
)

func newCookieBase(browser string) *cookieBase {
	return &cookieBase{
		browser:     browser,
		fetchFn:     resolve.FetchUserPlaylists,
		trackCache:  make(map[string][]playlist.Track),
		loadCancels: make(map[uint64]context.CancelFunc),
	}
}

func (b *cookieBase) fetchPlaylists() ([]playlist.PlaylistInfo, error) {
	b.mu.Lock()
	if b.playlists != nil {
		res := b.playlists
		b.mu.Unlock()
		return res, nil
	}
	generation := b.generation

	b.mu.Unlock()

	fn := b.fetchFn
	if fn == nil {
		fn = resolve.FetchUserPlaylists
	}
	pls, err := fn(b.browser)
	if err != nil {
		return nil, fmt.Errorf("ytmusic: fetch playlists: %w", err)
	}
	if pls == nil {
		pls = []playlist.PlaylistInfo{}
	}

	b.mu.Lock()
	if b.generation == generation {
		b.playlists = pls
	}
	b.mu.Unlock()
	return pls, nil
}

func (b *cookieBase) fetchTracks(target string) ([]playlist.Track, error) {
	b.mu.Lock()
	if cached, ok := b.trackCache[target]; ok {
		b.mu.Unlock()
		return cached, nil
	}
	generation := b.generation

	ctx, cancel := context.WithTimeout(context.Background(), cookiePlaylistLoadTimeout)
	b.nextLoad++
	loadID := b.nextLoad
	if b.loadCancels == nil {
		b.loadCancels = make(map[uint64]context.CancelFunc)
	}
	b.loadCancels[loadID] = cancel
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.loadCancels, loadID)
		b.mu.Unlock()
		cancel()
	}()

	resolveBatch := b.resolveFn
	if resolveBatch == nil {
		resolveBatch = resolve.ResolveYTDLBatchPageContext
	}
	var tracks []playlist.Track
	for start := 0; ; {
		batch, entries, err := resolveBatch(ctx, target, start, cookiePlaylistBatchSize, b.browser)
		if err != nil {
			return nil, fmt.Errorf("ytmusic: resolve playlist tracks: %w", err)
		}
		tracks = append(tracks, batch...)
		if entries < cookiePlaylistBatchSize {
			break
		}
		start += cookiePlaylistBatchSize
	}

	b.mu.Lock()
	if b.generation == generation {
		b.trackCache[target] = tracks
	}
	b.mu.Unlock()
	return tracks, nil
}

func (b *cookieBase) refresh() {
	b.mu.Lock()
	b.generation++
	for _, cancel := range b.loadCancels {
		cancel()
	}
	clear(b.loadCancels)
	b.playlists = nil
	clear(b.trackCache)
	b.mu.Unlock()
}

func (b *cookieBase) close() {
	b.mu.Lock()
	b.generation++
	for _, cancel := range b.loadCancels {
		cancel()
	}
	clear(b.loadCancels)
	b.mu.Unlock()
}

// CookieProvider provides YouTube and YouTube Music playlist access using
// browser cookies via yt-dlp, without requiring Google Cloud OAuth credentials.
type CookieProvider struct {
	base *cookieBase
	kind ProviderKind
}

// CookieProviders holds the YouTube Music, YouTube, and YouTube All cookie-backed providers.
type CookieProviders struct {
	Music *CookieProvider
	Video *CookieProvider
	All   *CookieProvider
}

// NewCookieProviders creates cookie-backed YouTube providers for Music, Video, and All.
func NewCookieProviders(browser string) CookieProviders {
	browser = strings.TrimSpace(browser)
	base := newCookieBase(browser)
	return CookieProviders{
		Music: &CookieProvider{base: base, kind: KindMusic},
		Video: &CookieProvider{base: base, kind: KindVideo},
		All:   &CookieProvider{base: base, kind: KindAll},
	}
}

// NewCookieProvider creates a single cookie-backed YouTube provider of the given kind.
func NewCookieProvider(browser string, kind ProviderKind) *CookieProvider {
	browser = strings.TrimSpace(browser)
	return &CookieProvider{
		base: newCookieBase(browser),
		kind: kind,
	}
}

// Name returns the display name of this provider.
func (p *CookieProvider) Name() string {
	switch p.kind {
	case KindMusic:
		return "YouTube Music"
	case KindVideo:
		return "YouTube"
	case KindAll:
		return "YouTube (All)"
	default:
		return "YouTube"
	}
}

// Playlists returns the available playlists for this provider.
func (p *CookieProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	userPls, err := p.base.fetchPlaylists()
	if err != nil {
		return nil, err
	}

	// Filter out system playlists from scraped feed to avoid duplicates with pinned entries,
	// but preserve their track counts for the pinned rows.
	var (
		customPls []playlist.PlaylistInfo
		lmCount   int
		llCount   int
	)
	for _, pl := range userPls {
		switch pl.ID {
		case playlistIDLikedMusic:
			lmCount = pl.TrackCount
		case playlistIDLikedVideos:
			llCount = pl.TrackCount
		default:
			customPls = append(customPls, pl)
		}
	}

	result := make([]playlist.PlaylistInfo, 0, len(customPls)+2)
	switch p.kind {
	case KindMusic:
		result = append(result, playlist.PlaylistInfo{
			ID:         playlistIDLikedMusic,
			Name:       "Liked Music",
			TrackCount: lmCount,
		})
	case KindVideo:
		result = append(result, playlist.PlaylistInfo{
			ID:         playlistIDLikedVideos,
			Name:       "Liked Videos",
			TrackCount: llCount,
		})
	case KindAll:
		result = append(result,
			playlist.PlaylistInfo{
				ID:         playlistIDLikedMusic,
				Name:       "Liked Music",
				TrackCount: lmCount,
			},
			playlist.PlaylistInfo{
				ID:         playlistIDLikedVideos,
				Name:       "Liked Videos",
				TrackCount: llCount,
			},
		)
	}
	result = append(result, customPls...)
	return result, nil
}

func formatPlaylistURL(playlistID string, isMusic bool) string {
	if strings.HasPrefix(playlistID, "http://") || strings.HasPrefix(playlistID, "https://") {
		return playlistID
	}
	switch playlistID {
	case playlistIDLikedMusic:
		return "https://music.youtube.com/playlist?list=LM"
	case playlistIDLikedVideos:
		return "https://www.youtube.com/playlist?list=LL"
	default:
		if isMusic {
			return "https://music.youtube.com/playlist?list=" + playlistID
		}
		return "https://www.youtube.com/playlist?list=" + playlistID
	}
}

// Tracks resolves tracks in the given playlist via yt-dlp.
func (p *CookieProvider) Tracks(playlistID string) ([]playlist.Track, error) {
	if playlistID == "" {
		return nil, fmt.Errorf("ytmusic: empty playlist id")
	}
	target := formatPlaylistURL(playlistID, p.kind == KindMusic)
	return p.base.fetchTracks(target)
}

// SearchTracks performs a search query using yt-dlp's ytsearch: protocol.
func (p *CookieProvider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	tracks, err := resolve.ResolveYTDLBatchContext(ctx, fmt.Sprintf("ytsearch%d:%s", limit, q), 0, 0, p.base.browser)
	if err != nil {
		return nil, fmt.Errorf("ytmusic: search tracks: %w", err)
	}
	return tracks, nil
}

// Refresh clears the cached playlists.
func (p *CookieProvider) Refresh() {
	p.base.refresh()
}

// Close releases any held resources.
func (p *CookieProvider) Close() { p.base.close() }
