// Package bandcamp streams a fan's purchased Bandcamp collection through
// Bandcamp's official Subsonic API (open beta since July 2026,
// https://bandcamp.com/api/subsonic). The fan generates dedicated Subsonic
// credentials in Fan Settings — never the Bandcamp account login — and the
// shared internal/subsonicapi client does the rest.
//
// The unofficial identity-cookie fan-collection API (pagedata scraping +
// api/fancollection endpoints) was considered and rejected: Bandcamp's
// Acceptable Use policy prohibits scraping, the mobile API was cut off
// without notice in July 2024, anti-bot hardening since Aug 2026 requires
// browser impersonation, and the cookie is a full-account credential.
// The official Subsonic API is sanctioned, scoped, and revocable.
//
// Beta caveats are dated 2026-08-25 and live in the bandcamp dialect in
// internal/subsonicapi/dialect.go.
package bandcamp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/subsonicapi"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time checks for the interfaces Provider overrides. The browse,
// search, and sort interfaces are promoted from the embedded
// *subsonicapi.Client, which asserts them itself. The playlist-mutation
// interfaces are asserted here as well: promotion is shadowed by name, not
// by signature, so without these a drift in either interface would silently
// drop Provider's override — and with it the guard that keeps the read-only
// Library rows from being written to.
var (
	_ playlist.Provider            = (*Provider)(nil)
	_ playlist.Refresher           = (*Provider)(nil)
	_ provider.PlaylistWriter      = (*Provider)(nil)
	_ provider.PlaylistBatchWriter = (*Provider)(nil)
)

// DefaultURL is Bandcamp's official Subsonic API endpoint. The config url
// key overrides it as an escape hatch while the beta moves.
const DefaultURL = "https://bandcamp.com/api/subsonic"

// Synthetic Library row IDs. Prefixed so they can never collide with a
// server-assigned playlist ID.
const (
	recentPurchasesID = "bandcamp:recent"
	allPurchasesID    = "bandcamp:all"
	randomPurchasesID = "bandcamp:random"
)

// libraryRows is the single source for the synthetic rows: IDs, display
// names, and the read-only marking the picker filters on.
var libraryRows = []playlist.PlaylistInfo{
	{ID: recentPurchasesID, Name: "Recent purchases", Section: "Library", ReadOnly: true},
	{ID: allPurchasesID, Name: "All purchases", Section: "Library", ReadOnly: true},
	{ID: randomPurchasesID, Name: "Random purchases", Section: "Library", ReadOnly: true},
}

// recentAlbumCount bounds the newest-albums fetch behind the Recent row.
const recentAlbumCount = 10

// allPageSize is the search3 page size used to list the whole collection
// (an empty query returns every owned song on the beta); maxAllPages bounds
// the paging loop (200 x 500 songs).
const (
	allPageSize = 500
	maxAllPages = 200
)

// randomSampleSize bounds the Random purchases queue.
const randomSampleSize = 100

// albumFetchConcurrency bounds concurrent getAlbum calls against the public
// endpoint — politeness on a service with undocumented rate limits.
const albumFetchConcurrency = 4

// Deadlines for the library fetches. Tracks has no context of its own, so
// without these a stalling server keeps a fetch alive for as long as its
// requests take: maxAllPages sequential pages for the collection crawl,
// recentAlbumCount album reads for the newest-albums fan-out.
const (
	allCrawlTimeout    = 15 * time.Minute
	recentFetchTimeout = 5 * time.Minute
)

// libraryRow returns the synthetic row with the given ID, if any.
func libraryRow(id string) (playlist.PlaylistInfo, bool) {
	for _, row := range libraryRows {
		if row.ID == id {
			return row, true
		}
	}
	return playlist.PlaylistInfo{}, false
}

// readOnlyRowError guards the synthetic Library rows against playlist
// mutation (defense in depth behind the picker's ReadOnly filter — IPC
// clients can address playlists by ID directly).
func readOnlyRowError(row playlist.PlaylistInfo) error {
	return fmt.Errorf("bandcamp: %q is a library view, not a playlist", row.Name)
}

// Provider wraps the shared Subsonic client with Bandcamp-specific
// presentation: static Library rows (Recent / All / Random purchases) so the
// provider pane never opens empty (most fans have no Bandcamp playlists yet —
// the feature shipped with the beta). Row tracks are fetched lazily when
// opened and cached until Refresh.
type Provider struct {
	*subsonicapi.Client
	recentFetchMu sync.Mutex // serializes the recent-albums fan-out
	allFetchMu    sync.Mutex // serializes the whole-collection paging
	mu            sync.Mutex // guards the fields below
	recentCache   []playlist.Track
	allCache      []playlist.Track
	refreshGen    uint64             // bumped by Refresh so an in-flight fetch can't resurrect stale data
	allCancel     context.CancelFunc // aborts an in-flight collection crawl
	recentCancel  context.CancelFunc // aborts an in-flight recent-albums fetch
	allTruncated  string             // why the cached collection is short of everything, if it is
	lastWarn      string             // dedup for the footer warning
}

// NewFromConfig creates a Provider from a config.BandcampConfig value.
// Returns nil unless both Subsonic credentials are present.
func NewFromConfig(cfg config.BandcampConfig) *Provider {
	if !cfg.IsSet() {
		return nil
	}
	if _, err := endpoint(cfg); err != nil {
		applog.UserWarn("%v; using %s instead", err, DefaultURL)
	}
	return &Provider{Client: newClient(cfg)}
}

// endpoint returns the API base URL to talk to: the configured override, or
// DefaultURL when none is set. Every request carries the Subsonic token and
// salt in its query string, and a token/salt pair can be replayed, so an
// override must use https. Plain http is allowed only to a loopback address,
// where the traffic never leaves the machine (a local debugging proxy).
func endpoint(cfg config.BandcampConfig) (string, error) {
	if cfg.URL == "" {
		return DefaultURL, nil
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" {
		return "", errors.New("bandcamp: the configured url is not a valid absolute URL")
	}
	if u.Scheme == "https" || (u.Scheme == "http" && isLoopback(u.Hostname())) {
		return cfg.URL, nil
	}
	return "", fmt.Errorf("bandcamp: the configured url must use https, not %s://%s — Subsonic credentials travel in its query string", u.Scheme, u.Host)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newClient(cfg config.BandcampConfig) *subsonicapi.Client {
	base, err := endpoint(cfg)
	if err != nil {
		// Bandcamp credentials only work at Bandcamp anyway, so the safe
		// fallback is the official endpoint rather than the insecure one.
		base = DefaultURL
	}
	return subsonicapi.NewBandcampClient(subsonicapi.Config{
		BaseURL:    base,
		User:       cfg.User,
		Password:   cfg.Password,
		BrowseSort: cfg.BrowseSort,
		SaveSort:   config.SaveBandcampSort,
	})
}

// Validate checks the given credentials against the live API with an
// authenticated call. Bandcamp's ping answers ok even with bad credentials,
// so the setup wizard must use this instead of Ping.
func Validate(cfg config.BandcampConfig) error {
	if _, err := endpoint(cfg); err != nil {
		return err
	}
	return newClient(cfg).ValidateAuth()
}

// Playlists returns the static Library rows followed by the fan's Bandcamp
// playlists. A playlist-list failure still returns the Library rows, with
// the error alongside so the pane renders them under a status warning
// instead of going blank — the two sections draw from different endpoints
// and fail independently. Rejected credentials (the shared client has
// already ruled out an outage) fail the pane outright into the persistent
// credential-error surface.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	out := slices.Clone(libraryRows)
	lists, err := p.Client.Playlists()
	if err != nil {
		if errors.Is(err, subsonicapi.ErrBadCredentials) {
			return nil, err
		}
		return out, err
	}
	for _, l := range lists {
		l.Section = "Your playlists"
		out = append(out, l)
	}
	return out, nil
}

// warnOnce surfaces a footer warning, skipping consecutive duplicates so a
// persistent beta gap does not re-warn on every pane visit.
func (p *Provider) warnOnce(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	p.mu.Lock()
	dup := msg == p.lastWarn
	p.lastWarn = msg
	p.mu.Unlock()
	if !dup {
		applog.UserWarn("%s", msg)
	}
}

// AddTrackToPlaylist rejects the synthetic Library rows; everything else
// goes to the shared client. Implements provider.PlaylistWriter.
func (p *Provider) AddTrackToPlaylist(ctx context.Context, playlistID string, track playlist.Track) error {
	if row, ok := libraryRow(playlistID); ok {
		return readOnlyRowError(row)
	}
	return p.Client.AddTrackToPlaylist(ctx, playlistID, track)
}

// AddTracksToPlaylist rejects the synthetic Library rows; everything else
// goes to the shared client. Implements provider.PlaylistBatchWriter.
func (p *Provider) AddTracksToPlaylist(ctx context.Context, playlistID string, tracks []playlist.Track) (int, int, error) {
	if row, ok := libraryRow(playlistID); ok {
		return 0, 0, readOnlyRowError(row)
	}
	return p.Client.AddTracksToPlaylist(ctx, playlistID, tracks)
}

// Tracks routes the synthetic Library rows to their fetches and everything
// else to the server playlist.
func (p *Provider) Tracks(id string) ([]playlist.Track, error) {
	switch id {
	case recentPurchasesID:
		return p.cachedTracks(&p.recentFetchMu, &p.recentCache, p.fetchRecent)
	case allPurchasesID:
		tracks, err := p.cachedTracks(&p.allFetchMu, &p.allCache, p.fetchAll)
		if err == nil {
			p.warnIfTruncated()
		}
		return tracks, err
	case randomPurchasesID:
		return p.randomTracks()
	}
	return p.Client.Tracks(id)
}

// warnIfTruncated re-announces a collection that is short of everything the
// fan owns. The warning the crawl emitted is long gone by the next open, and
// the list is served from cache from then on, so without this a short list
// silently becomes "your collection".
func (p *Provider) warnIfTruncated() {
	p.mu.Lock()
	msg := p.allTruncated
	p.mu.Unlock()
	if msg != "" {
		applog.UserWarn("%s", msg)
	}
}

// Refresh clears the Library caches along with the shared client's caches
// and unsupported-endpoint memo.
func (p *Provider) Refresh() {
	p.mu.Lock()
	p.recentCache = nil
	p.allCache = nil
	p.refreshGen++
	p.lastWarn = ""
	p.allTruncated = ""
	cancels := []context.CancelFunc{p.allCancel, p.recentCancel}
	p.mu.Unlock()
	// Cut in-flight fetches short now rather than at their next boundary:
	// their results are stale the moment Refresh runs.
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
	p.Client.Refresh()
}

// errRefreshed is returned by a fetch that noticed a Refresh mid-flight;
// cachedTracks starts it over so the caller gets post-refresh data.
var errRefreshed = errors.New("bandcamp: refreshed during fetch")

// cachedTracks serves slot from cache or runs fetch under fetchMu so
// concurrent callers (UI + IPC) run it once. fetch receives the refresh
// generation it started under: a result from a stale generation is served
// but never cached, and fetch may also decline caching (partial results)
// so the next open retries.
func (p *Provider) cachedTracks(fetchMu *sync.Mutex, slot *[]playlist.Track, fetch func(gen uint64) (tracks []playlist.Track, cacheable bool, err error)) ([]playlist.Track, error) {
	fetchMu.Lock()
	defer fetchMu.Unlock()

	for {
		p.mu.Lock()
		if *slot != nil {
			cached := slices.Clone(*slot)
			p.mu.Unlock()
			return cached, nil
		}
		gen := p.refreshGen
		p.mu.Unlock()

		tracks, cacheable, err := fetch(gen)
		if errors.Is(err, errRefreshed) {
			continue
		}
		if err != nil {
			return nil, err
		}

		p.mu.Lock()
		if cacheable && gen == p.refreshGen {
			*slot = tracks
		}
		p.mu.Unlock()
		return slices.Clone(tracks), nil
	}
}

// truncationNotice returns the standing "this list is short of the whole
// collection" message, for tests.
func (p *Provider) truncationNotice() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.allTruncated
}

// registerFetch builds a deadline-bounded context for a library fetch and
// parks its cancel in slot so Refresh can abort it. The returned cancel
// clears the slot as well as releasing the context.
func (p *Provider) registerFetch(slot *context.CancelFunc, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	p.mu.Lock()
	*slot = cancel
	p.mu.Unlock()
	return ctx, func() {
		p.mu.Lock()
		*slot = nil
		p.mu.Unlock()
		cancel()
	}
}

// stale reports whether Refresh has run since generation gen was read.
func (p *Provider) stale(gen uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return gen != p.refreshGen
}

// fetchRecent flattens the newest purchased albums with bounded concurrency.
// Partial results are usable (some albums may fail on the beta) but neither
// silent nor cached; it errors only when nothing loaded at all.
func (p *Provider) fetchRecent(gen uint64) ([]playlist.Track, bool, error) {
	ctx, cancel := p.registerFetch(&p.recentCancel, recentFetchTimeout)
	defer cancel()

	albums, err := p.AlbumListContext(ctx, subsonicapi.SortNewest, 0, recentAlbumCount)
	if err != nil {
		if p.stale(gen) {
			return nil, false, errRefreshed
		}
		return nil, false, err
	}

	perAlbum := make([][]playlist.Track, len(albums))
	errs := make([]error, len(albums))
	sem := make(chan struct{}, albumFetchConcurrency)
	var wg sync.WaitGroup
	for i, al := range albums {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			perAlbum[i], errs[i] = p.AlbumTracksContext(ctx, al.ID)
		}()
	}
	wg.Wait()

	total, loaded := 0, 0
	for i, ts := range perAlbum {
		if errs[i] == nil {
			loaded++
		}
		total += len(ts)
	}
	// Sized for the tracks, not the albums, and never nil: cachedTracks
	// reads a nil slot as "not fetched yet", so an empty collection has to
	// come back as an empty slice to stay cached.
	tracks := make([]playlist.Track, 0, total)
	for _, ts := range perAlbum {
		tracks = append(tracks, ts...)
	}
	complete := true
	for _, err := range errs {
		if err == nil {
			continue
		}
		// Only when nothing loaded at all is there no list to show. An album
		// that fetched fine but holds no songs still counts as loaded.
		if loaded == 0 {
			return nil, false, err
		}
		p.warnOnce("bandcamp: some recent purchases failed to load: %v", err)
		complete = false
		break
	}
	return tracks, complete, nil
}

// fetchAll lists the whole purchased collection — search3 with an empty
// query returns every owned song on the beta, paged — sorted by artist,
// album, track number so it reads like a library.
//
// The crawl advances by however many songs a page actually returned, not by
// the size it asked for, so a server that quietly caps songCount below
// allPageSize still pages to the end. Only an empty page means the
// collection ended: a page of songs already seen means the server is not
// honoring songOffset, and a list that stops for that reason — or at the
// page cap — is short of the real collection, so it is announced rather
// than passed off as everything the fan owns.
func (p *Provider) fetchAll(gen uint64) ([]playlist.Track, bool, error) {
	ctx, cancel := p.registerFetch(&p.allCancel, allCrawlTimeout)
	defer cancel()

	tracks := make([]playlist.Track, 0, allPageSize)
	seen := make(map[string]struct{}, allPageSize)
	var complete bool
	var cutShort string
	offset := 0
	for page := 0; page < maxAllPages; page++ {
		batch, err := p.SearchTracksFrom(ctx, "", offset, allPageSize)
		if err != nil {
			// Refresh cancels this context, so a stale generation explains
			// the failure: start over rather than reporting it.
			if p.stale(gen) {
				return nil, false, errRefreshed
			}
			if len(tracks) == 0 {
				return nil, false, err
			}
			// Everything fetched so far is still usable — keep it rather
			// than making one bad page cost the whole crawl.
			cutShort = fmt.Sprintf("the listing failed partway (%v)", err)
			break
		}
		offset += len(batch)
		if len(batch) == 0 {
			complete = true
			break
		}
		added := 0
		for _, t := range batch {
			key := trackKey(t)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			tracks = append(tracks, t)
			added++
		}
		if added == 0 {
			// A repeated page means songOffset was ignored. If that page was
			// not even full it is the tail of the collection echoed back, so
			// there is nothing missing to report.
			if len(batch) < allPageSize {
				complete = true
			} else {
				cutShort = "the server repeated a page instead of paging further"
			}
			break
		}
	}
	if !complete && cutShort == "" {
		cutShort = fmt.Sprintf("the %d-page limit was reached", maxAllPages)
	}
	// Caching a short list beats re-crawling hundreds of pages on every open,
	// so keep it — but never silently. Recorded here, announced by the row
	// that was opened, so a fresh crawl and a cache hit warn exactly once.
	p.mu.Lock()
	if cutShort == "" {
		p.allTruncated = ""
	} else {
		p.allTruncated = fmt.Sprintf("bandcamp: showing %d purchases, not the whole collection — %s; press Ctrl+R to retry", len(tracks), cutShort)
	}
	p.mu.Unlock()
	slices.SortStableFunc(tracks, func(a, b playlist.Track) int {
		return cmp.Or(
			strings.Compare(strings.ToLower(a.Artist), strings.ToLower(b.Artist)),
			strings.Compare(strings.ToLower(a.Album), strings.ToLower(b.Album)),
			cmp.Compare(a.TrackNumber, b.TrackNumber),
		)
	})
	return tracks, true, nil
}

// trackKey identifies a song while paging, so a repeated page is recognized
// as one. The provider id is authoritative; a response that omits it still
// gets a stable key from the metadata rather than collapsing every such
// track onto one empty key.
func trackKey(t playlist.Track) string {
	if id := t.Meta(provider.MetaBandcampID); id != "" {
		return "id:" + id
	}
	return fmt.Sprintf("%s|%s|%s|%d|%d", t.Title, t.Artist, t.Album, t.TrackNumber, t.DurationSecs)
}

// randomTracks returns a fresh random sample of the whole collection on
// every call (the qobuz "Random Tracks" idiom), shuffling the private copy
// of the all-purchases list that cachedTracks hands out.
func (p *Provider) randomTracks() ([]playlist.Track, error) {
	all, err := p.cachedTracks(&p.allFetchMu, &p.allCache, p.fetchAll)
	if err != nil {
		return nil, err
	}
	p.warnIfTruncated()
	rand.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	return all[:min(randomSampleSize, len(all))], nil
}
