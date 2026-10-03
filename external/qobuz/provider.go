package qobuz

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ playlist.Provider         = (*QobuzProvider)(nil)
	_ playlist.Authenticator    = (*QobuzProvider)(nil)
	_ playlist.Refresher        = (*QobuzProvider)(nil)
	_ provider.Searcher         = (*QobuzProvider)(nil)
	_ provider.ArtistBrowser    = (*QobuzProvider)(nil)
	_ provider.AlbumBrowser     = (*QobuzProvider)(nil)
	_ provider.AlbumTrackLoader = (*QobuzProvider)(nil)
	_ provider.Closer           = (*QobuzProvider)(nil)
)

// favoriteTracksID is the synthetic playlist ID for the user's favorite tracks.
const favoriteTracksID = "favorites/tracks"

// randomTracksID is the synthetic playlist ID for a random sample of tracks
// drawn from across all of the user's playlists (deduplicated).
const randomTracksID = "playlists/random"

// playlistFetchConcurrency bounds how many playlist/get calls run in parallel
// when gathering tracks for the Random Tracks entry.
const playlistFetchConcurrency = 8

// favoritesPageSize is the page size for favorite album/artist browsing.
const favoritesPageSize = 100

// randomTracksLimit caps the synthetic Random Tracks list. When the
// deduplicated library exceeds this, a random sample is taken so it stays a
// fair cross-section. Matches the favorite tracks cap.
const randomTracksLimit = 500

// TrackURIPrefix is the custom URI scheme for Qobuz tracks. Track paths are
// "qobuz://track/<id>". The player resolves them to a fresh signed URL at
// play time through the SourceResolver registered in providers.go, so queue
// entries never hold expirable URLs.
const TrackURIPrefix = "qobuz://track/"

// albumSortTypes is the static sort list for Qobuz album browsing. Qobuz has no
// global catalog listing, so browsing surfaces the user's favorite albums.
var albumSortTypes = []provider.SortType{
	{ID: "favorites", Label: "Favorite Albums"},
}

// QobuzProvider implements playlist.Provider backed by the Qobuz API. Tracks
// carry qobuz:// URIs. ResolveSource turns them into signed stream URLs when
// playback starts. providers.go marks the resolved URL as Buffered.
type QobuzProvider struct {
	quality int

	mu         sync.Mutex
	client     *client
	authCancel context.CancelFunc
	authGen    uint64 // counts sign-ins, so a call clears only its own authCancel

	listCache  []playlist.PlaylistInfo
	trackCache map[string][]playlist.Track
}

// New creates a QobuzProvider. Authentication is deferred until the user first
// selects the provider. quality is the preferred Qobuz format_id.
func New(quality int) *QobuzProvider {
	if !validQuality(quality) {
		quality = defaultQuality
	}
	return &QobuzProvider{
		quality:    quality,
		trackCache: make(map[string][]playlist.Track),
	}
}

func (p *QobuzProvider) Name() string { return "Qobuz" }

// ensureClient builds an authenticated client from stored credentials only
// (no browser). Returns playlist.ErrNeedsAuth if interactive sign-in is needed.
func (p *QobuzProvider) ensureClient() (*client, error) {
	p.mu.Lock()
	if p.client != nil {
		c := p.client
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := newClientSilent(ctx, apiBaseURL)
	if err != nil {
		applog.Debug("qobuz: silent auth failed, prompting sign-in: %v", err)
		return nil, playlist.ErrNeedsAuth
	}

	p.mu.Lock()
	p.client = c
	p.mu.Unlock()
	return c, nil
}

// signIn runs the interactive sign-in. Tests replace it.
var signIn = newClientInteractive

// Authenticate runs the interactive OAuth sign-in flow (opens a browser, waits
// for the redirect). Implements playlist.Authenticator.
func (p *QobuzProvider) Authenticate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Cancel the old flow and register this one in one lock hold, so a
	// newer call or Close always finds the flow that runs.
	p.mu.Lock()
	if p.client != nil {
		p.mu.Unlock()
		return nil
	}
	if p.authCancel != nil {
		p.authCancel()
	}
	p.authGen++
	gen := p.authGen
	p.authCancel = cancel
	p.mu.Unlock()

	c, err := signIn(ctx)

	p.mu.Lock()
	if p.authGen == gen {
		p.authCancel = nil
	}
	p.mu.Unlock()

	if err != nil {
		return err
	}
	p.mu.Lock()
	p.client = c
	p.mu.Unlock()
	return nil
}

// Close cancels any in-progress sign-in. Implements provider.Closer.
func (p *QobuzProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authCancel != nil {
		p.authCancel()
		p.authCancel = nil
	}
}

// Refresh clears cached playlist and track lists so the next call re-fetches
// them. ResolveSource gets stream URLs at play time, so no URL state needs
// repair here. Implements playlist.Refresher.
func (p *QobuzProvider) Refresh() {
	p.mu.Lock()
	p.listCache = nil
	p.trackCache = make(map[string][]playlist.Track)
	p.mu.Unlock()
}

// mapErr translates errors of client c into provider-level errors. A
// rejected user_auth_token drops the cached client, so the next access asks
// for sign-in instead of failing until restart. It drops the client only
// when it is still c, so a slow request keeps the client of a newer sign-in.
func (p *QobuzProvider) mapErr(c *client, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errUnauthorized) {
		p.mu.Lock()
		if p.client == c {
			p.client = nil
			applog.UserWarn("qobuz: session expired, sign in again")
		}
		p.mu.Unlock()
		return playlist.ErrNeedsAuth
	}
	return err
}

// Playlists returns the user's Qobuz playlists plus synthetic Favorite Tracks
// and Random Tracks entries.
func (p *QobuzProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	if p.listCache != nil {
		cached := slices.Clone(p.listCache)
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pls, err := c.userPlaylists(ctx)
	if err != nil {
		return nil, p.mapErr(c, err)
	}

	lists := []playlist.PlaylistInfo{
		{
			ID:      favoriteTracksID,
			Name:    "Favorite Tracks",
			Section: "Library",
		},
		{
			ID:      randomTracksID,
			Name:    "Random Tracks",
			Section: "Library",
		},
	}
	for _, pl := range pls {
		lists = append(lists, playlist.PlaylistInfo{
			ID:           pl.ID.String(),
			Name:         pl.Name,
			TrackCount:   pl.TracksCount,
			DurationSecs: pl.Duration,
			Section:      "Your playlists",
		})
	}

	p.mu.Lock()
	p.listCache = lists
	p.mu.Unlock()
	return slices.Clone(lists), nil
}

// Tracks returns the tracks of a playlist (or the synthetic Favorite Tracks /
// Random Tracks entries). The tracks carry qobuz:// URIs. Stream URLs resolve
// at play time.
func (p *QobuzProvider) Tracks(playlistID string) ([]playlist.Track, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	if cached, ok := p.trackCache[playlistID]; ok {
		tracks := slices.Clone(cached)
		p.mu.Unlock()
		return tracks, nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var apiTracks []apiTrack
	switch playlistID {
	case favoriteTracksID:
		apiTracks, err = c.favoriteTracks(ctx, 0, 500)
	case randomTracksID:
		apiTracks, err = p.randomTracks(ctx, c)
	default:
		apiTracks, err = c.playlistTracks(ctx, playlistID)
	}
	if err != nil {
		return nil, p.mapErr(c, err)
	}

	tracks := tracksFromAPI(apiTracks, nil)

	p.mu.Lock()
	p.trackCache[playlistID] = tracks
	p.mu.Unlock()
	return slices.Clone(tracks), nil
}

// randomTracks aggregates the tracks of every user playlist (fetched
// concurrently), drops tracks that appear in more than one playlist, and
// returns a random sample of at most randomTracksLimit. Sampling (rather than
// truncating) keeps the entry a fair cross-section of the whole library;
// refreshing the provider picks a new sample.
func (p *QobuzProvider) randomTracks(ctx context.Context, c *client) ([]apiTrack, error) {
	pls, err := c.userPlaylists(ctx)
	if err != nil {
		return nil, fmt.Errorf("qobuz: list playlists: %w", err)
	}

	// Fetch each playlist's tracks in parallel, then merge in playlist order so
	// dedupe (first occurrence wins) stays deterministic.
	lists := make([][]apiTrack, len(pls))
	errs := make([]error, len(pls))
	sem := make(chan struct{}, playlistFetchConcurrency)
	var wg sync.WaitGroup
	for i := range pls {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			lists[idx], errs[idx] = c.playlistTracks(ctx, pls[idx].ID.String())
		}(i)
	}
	wg.Wait()

	var all []apiTrack
	for i := range pls {
		if errs[i] != nil {
			return nil, fmt.Errorf("qobuz: playlist %s: %w", pls[i].ID, errs[i])
		}
		all = append(all, lists[i]...)
	}
	return sampleTracks(dedupeTracksByID(all), randomTracksLimit, rand.Shuffle), nil
}

// sampleTracks shuffles a copy of in and returns up to n of the result. The
// list is always randomized because that's the whole point of the Random Tracks
// entry. When the library is larger than n, the shuffle makes it a fair
// sample of the whole library rather than its first n. The shuffle func is
// injected so tests stay deterministic; production passes rand.Shuffle, which
// is safe for concurrent use.
func sampleTracks(in []apiTrack, n int, shuffle func(n int, swap func(i, j int))) []apiTrack {
	out := slices.Clone(in)
	shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// dedupeTracksByID returns tracks with duplicate Qobuz IDs removed, keeping the
// first occurrence. Tracks with an empty ID are always kept.
func dedupeTracksByID(in []apiTrack) []apiTrack {
	seen := make(map[string]bool, len(in))
	out := make([]apiTrack, 0, len(in))
	for _, t := range in {
		id := t.ID.String()
		if id != "" {
			if seen[id] {
				continue
			}
			seen[id] = true
		}
		out = append(out, t)
	}
	return out
}

// SearchTracks searches the Qobuz catalog. Implements provider.Searcher.
func (p *QobuzProvider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	apiTracks, err := c.searchTracks(ctx, query, limit)
	if err != nil {
		return nil, p.mapErr(c, err)
	}
	return tracksFromAPI(apiTracks, nil), nil
}

// Artists returns the user's favorite artists. Implements provider.ArtistBrowser.
func (p *QobuzProvider) Artists() ([]provider.ArtistInfo, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var artists []provider.ArtistInfo
	for offset := 0; ; offset += favoritesPageSize {
		page, err := c.favoriteArtists(ctx, offset, favoritesPageSize)
		if err != nil {
			return nil, p.mapErr(c, err)
		}
		for _, a := range page {
			artists = append(artists, provider.ArtistInfo{
				ID:         a.ID.String(),
				Name:       a.Name,
				AlbumCount: a.AlbumsCount,
			})
		}
		if len(page) < favoritesPageSize {
			break
		}
	}
	return artists, nil
}

// ArtistAlbums returns the albums of an artist. Implements provider.ArtistBrowser.
func (p *QobuzProvider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	albums, err := c.artistAlbums(ctx, artistID)
	if err != nil {
		return nil, p.mapErr(c, err)
	}
	out := make([]provider.AlbumInfo, 0, len(albums))
	for _, a := range albums {
		out = append(out, albumInfo(a))
	}
	return out, nil
}

// AlbumList returns the user's favorite albums (Qobuz has no global album
// catalog to browse). Implements provider.AlbumBrowser.
func (p *QobuzProvider) AlbumList(_ string, offset, size int) ([]provider.AlbumInfo, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}
	if size <= 0 {
		size = favoritesPageSize
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	albums, err := c.favoriteAlbums(ctx, offset, size)
	if err != nil {
		return nil, p.mapErr(c, err)
	}
	out := make([]provider.AlbumInfo, 0, len(albums))
	for _, a := range albums {
		out = append(out, albumInfo(a))
	}
	return out, nil
}

func (p *QobuzProvider) AlbumSortTypes() []provider.SortType { return albumSortTypes }

func (p *QobuzProvider) DefaultAlbumSort() string { return "favorites" }

// AlbumTracks returns the tracks of an album. Implements provider.AlbumTrackLoader.
func (p *QobuzProvider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	album, err := c.albumGet(ctx, albumID)
	if err != nil {
		return nil, p.mapErr(c, err)
	}
	var tracks []apiTrack
	if album.Tracks != nil {
		tracks = album.Tracks.Items
	}
	return tracksFromAPI(tracks, &album), nil
}

// tracksFromAPI converts API tracks into playlist tracks carrying qobuz://
// URIs. albumFallback supplies album metadata for tracks that lack it
// (album/get nests tracks without an album field). It sends no requests.
// Stream URLs resolve at play time.
func tracksFromAPI(in []apiTrack, albumFallback *apiAlbum) []playlist.Track {
	out := make([]playlist.Track, len(in))
	for i, t := range in {
		out[i] = trackFromAPI(t, albumFallback)
	}
	return out
}

// trackFromAPI maps a single API track to a playlist.Track. A track that
// Qobuz does not allow to stream is unplayable.
func trackFromAPI(t apiTrack, albumFallback *apiAlbum) playlist.Track {
	album := t.Album
	if album == nil {
		album = albumFallback
	}

	track := playlist.Track{
		Path:         TrackURIPrefix + t.ID.String(),
		Title:        t.Title,
		Artist:       trackArtist(t, album),
		TrackNumber:  t.TrackNumber,
		DurationSecs: t.Duration,
		Stream:       true,
		ProviderMeta: map[string]string{provider.MetaQobuzID: t.ID.String()},
	}
	if album != nil {
		track.Album = album.Title
		track.Genre = album.Genre.Name
		track.Year = provider.YearFromDate(album.ReleaseDateOriginal)
	}

	if !t.Streamable {
		track.Unplayable = true
	}
	return track
}

// ResolveSource turns a qobuz://track/<id> URI into a signed stream URL when
// playback starts. Resolving at play time keeps the URL fresh no matter how
// long the track sat in a queue. It is registered as the player's
// SourceResolver in providers.go.
func (p *QobuzProvider) ResolveSource(uri string) (string, error) {
	trackID, ok := strings.CutPrefix(uri, TrackURIPrefix)
	if !ok || trackID == "" || strings.ContainsAny(trackID, "/?#") {
		return "", fmt.Errorf("qobuz: invalid track URI %q", uri)
	}
	c, err := p.ensureClient()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	file, err := c.trackFileURL(ctx, trackID, p.quality, "")
	if err != nil {
		return "", p.mapErr(c, err)
	}
	if file.URL == "" {
		return "", fmt.Errorf("qobuz: no stream URL for track %s", trackID)
	}
	return file.URL, nil
}

// trackArtist picks the best available artist name for a track.
func trackArtist(t apiTrack, album *apiAlbum) string {
	if t.Performer.Name != "" {
		return t.Performer.Name
	}
	if album != nil {
		return album.Artist.Name
	}
	return ""
}

// albumInfo maps a Qobuz album to provider.AlbumInfo.
func albumInfo(a apiAlbum) provider.AlbumInfo {
	return provider.AlbumInfo{
		ID:         a.ID,
		Name:       a.Title,
		Artist:     a.Artist.Name,
		ArtistID:   a.Artist.ID.String(),
		Year:       provider.YearFromDate(a.ReleaseDateOriginal),
		TrackCount: a.TracksCount,
		Genre:      a.Genre.Name,
	}
}
