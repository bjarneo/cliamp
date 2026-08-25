package tidal

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ playlist.Provider         = (*TidalProvider)(nil)
	_ playlist.Authenticator    = (*TidalProvider)(nil)
	_ playlist.Refresher        = (*TidalProvider)(nil)
	_ provider.Searcher         = (*TidalProvider)(nil)
	_ provider.ArtistBrowser    = (*TidalProvider)(nil)
	_ provider.AlbumBrowser     = (*TidalProvider)(nil)
	_ provider.AlbumTrackLoader = (*TidalProvider)(nil)
	_ provider.Closer           = (*TidalProvider)(nil)
)

// favoriteTracksID is the synthetic playlist ID for the user's favorite tracks.
const favoriteTracksID = "favorites/tracks"

// favoriteTracksLimit caps the synthetic Favorite Tracks list, matching Qobuz.
const favoriteTracksLimit = 500

// TrackURIPrefix is the custom URI scheme for Tidal tracks. Track paths are
// "tidal://track/<id>"; the player resolves them to a fresh signed URL (or
// DASH segment list) at play time via the SourceResolver registered in
// main.go, so queue entries never hold expirable URLs.
const TrackURIPrefix = "tidal://track/"

// albumSortTypes is the static sort list for Tidal album browsing. The private
// API has no global catalog listing, so browsing surfaces favorite albums.
var albumSortTypes = []provider.SortType{
	{ID: "favorites", Label: "Favorite Albums"},
}

// TidalProvider implements playlist.Provider backed by Tidal's private client
// API. Tracks carry tidal:// URIs; ResolveSource turns them into playable
// sources when playback starts (see stream.go for the URL registry).
type TidalProvider struct {
	quality      string // normalized Tidal audioquality value
	clientID     string
	clientSecret string

	// downgradeNoticed dedupes the "delivered as AAC" footer notice so a
	// playlist of AAC-only tracks warns once per session, not per track.
	downgradeNoticed atomic.Bool

	mu         sync.Mutex
	client     *client
	authCancel context.CancelFunc

	listCache  []playlist.PlaylistInfo
	trackCache map[string][]playlist.Track
}

// New creates a TidalProvider. Authentication is deferred until the user first
// selects the provider. quality is a [tidal] config quality name (see
// NormalizeQuality); unrecognized values fall back to lossless. Empty client
// credentials fall back to the built-in pair.
func New(quality, clientID, clientSecret string) *TidalProvider {
	q, ok := normalizeQuality(quality)
	if !ok {
		applog.UserError("tidal: unknown quality %q, using \"lossless\" (valid: low, high, lossless, hires)", quality)
		q = qualityLossless
	}
	if clientID == "" {
		clientID, clientSecret = fallbackClientID, fallbackClientSecret
	} else if clientSecret == "" {
		// Matches python-tidal: a client without a secret uses its ID as one.
		clientSecret = clientID
	}
	return &TidalProvider{
		quality:      q,
		clientID:     clientID,
		clientSecret: clientSecret,
		trackCache:   make(map[string][]playlist.Track),
	}
}

func (p *TidalProvider) Name() string { return "Tidal" }

// ensureClient builds an authenticated client from stored credentials only
// (no browser). Returns playlist.ErrNeedsAuth if interactive sign-in is needed.
func (p *TidalProvider) ensureClient() (*client, error) {
	p.mu.Lock()
	if p.client != nil {
		c := p.client
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := newClientSilent(ctx)
	if err != nil {
		applog.Debug("tidal: silent auth failed, prompting sign-in: %v", err)
		return nil, playlist.ErrNeedsAuth
	}

	p.mu.Lock()
	p.client = c
	p.mu.Unlock()
	return c, nil
}

// Authenticate runs the interactive device-flow sign-in (shows a
// link.tidal.com URL, waits for approval). Implements playlist.Authenticator.
func (p *TidalProvider) Authenticate() error {
	p.mu.Lock()
	if p.client != nil {
		p.mu.Unlock()
		return nil
	}
	if p.authCancel != nil {
		p.authCancel()
		p.authCancel = nil
	}
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	p.mu.Lock()
	p.authCancel = cancel
	p.mu.Unlock()

	c, err := newClientInteractive(ctx, p.clientID, p.clientSecret)

	p.mu.Lock()
	p.authCancel = nil
	p.mu.Unlock()
	cancel()

	if err != nil {
		return err
	}
	p.mu.Lock()
	p.client = c
	p.mu.Unlock()
	return nil
}

// Close cancels any in-progress sign-in. Implements provider.Closer.
func (p *TidalProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authCancel != nil {
		p.authCancel()
		p.authCancel = nil
	}
}

// Refresh clears cached playlist and track lists so the next call re-fetches
// them. Stream URLs are resolved fresh at play time (see ResolveSource), so
// no URL state needs repairing here. Implements playlist.Refresher.
func (p *TidalProvider) Refresh() {
	p.mu.Lock()
	p.listCache = nil
	p.trackCache = make(map[string][]playlist.Track)
	p.mu.Unlock()
}

// mapErr translates client errors into provider-level errors: a revoked
// refresh token drops the cached client so the next access runs the
// interactive sign-in instead of failing forever.
func (p *TidalProvider) mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errAuthRevoked) {
		applog.UserWarn("tidal: session revoked, sign in again")
		p.mu.Lock()
		p.client = nil
		p.mu.Unlock()
		return playlist.ErrNeedsAuth
	}
	return err
}

// Playlists returns the user's Tidal playlists plus a synthetic Favorite
// Tracks entry.
func (p *TidalProvider) Playlists() ([]playlist.PlaylistInfo, error) {
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
		return nil, p.mapErr(err)
	}

	lists := []playlist.PlaylistInfo{
		{
			ID:      favoriteTracksID,
			Name:    "Favorite Tracks",
			Section: "Library",
		},
	}
	for _, pl := range pls {
		lists = append(lists, playlist.PlaylistInfo{
			ID:           pl.UUID,
			Name:         pl.Title,
			TrackCount:   pl.NumberOfTracks,
			DurationSecs: pl.Duration,
			Section:      "Your playlists",
		})
	}

	p.mu.Lock()
	p.listCache = lists
	p.mu.Unlock()
	return slices.Clone(lists), nil
}

// Tracks returns the tracks of a playlist (or the synthetic Favorite Tracks
// entry). Tracks carry tidal:// URIs; stream URLs resolve at play time.
func (p *TidalProvider) Tracks(playlistID string) ([]playlist.Track, error) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var apiTracks []apiTrack
	if playlistID == favoriteTracksID {
		apiTracks, err = c.favoriteTracks(ctx, favoriteTracksLimit)
	} else {
		apiTracks, err = c.playlistTracks(ctx, playlistID)
	}
	if err != nil {
		return nil, p.mapErr(err)
	}

	tracks := tracksFromAPI(apiTracks, nil)

	p.mu.Lock()
	p.trackCache[playlistID] = tracks
	p.mu.Unlock()
	return slices.Clone(tracks), nil
}

// SearchTracks searches the Tidal catalog for albums and tracks. Album hits
// lead the results as album placeholders (playlist.Track.IsAlbum), because a
// query is usually an artist or record name and the album is the more useful
// answer; the UI expands the chosen one with AlbumTracks. Implements
// provider.Searcher.
func (p *TidalProvider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}

	// Tracks and albums are separate endpoints; fetch them concurrently.
	var (
		apiTracks []apiTrack
		trackErr  error
		apiAlbums []apiAlbum
		albumErr  error
		wg        sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		apiTracks, trackErr = c.searchTracks(ctx, query, limit)
	}()
	go func() {
		defer wg.Done()
		apiAlbums, albumErr = c.searchAlbums(ctx, query, limit)
	}()
	wg.Wait()
	if trackErr != nil {
		return nil, p.mapErr(trackErr)
	}
	if albumErr != nil {
		// Tracks still answer the query; degrade to a track-only result.
		applog.Debug("tidal: album search failed: %v", albumErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]playlist.Track, 0, len(apiAlbums)+len(apiTracks))
	for _, a := range apiAlbums {
		out = append(out, albumPlaceholder(a))
	}
	return append(out, tracksFromAPI(apiTracks, nil)...), nil
}

// Artists returns the user's favorite artists. Implements provider.ArtistBrowser.
func (p *TidalProvider) Artists() ([]provider.ArtistInfo, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	artists, err := c.favoriteArtists(ctx)
	if err != nil {
		return nil, p.mapErr(err)
	}
	out := make([]provider.ArtistInfo, 0, len(artists))
	for _, a := range artists {
		out = append(out, provider.ArtistInfo{
			ID:   a.ID.String(),
			Name: a.Name,
		})
	}
	return out, nil
}

// ArtistAlbums returns the albums of an artist. Implements provider.ArtistBrowser.
func (p *TidalProvider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	albums, err := c.artistAlbums(ctx, artistID)
	if err != nil {
		return nil, p.mapErr(err)
	}
	out := make([]provider.AlbumInfo, 0, len(albums))
	for _, a := range albums {
		out = append(out, albumInfo(a))
	}
	return out, nil
}

// AlbumList returns the user's favorite albums (the private API has no global
// album catalog to browse). Implements provider.AlbumBrowser.
func (p *TidalProvider) AlbumList(_ string, offset, size int) ([]provider.AlbumInfo, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	albums, err := c.favoriteAlbums(ctx, offset, size)
	if err != nil {
		return nil, p.mapErr(err)
	}
	out := make([]provider.AlbumInfo, 0, len(albums))
	for _, a := range albums {
		out = append(out, albumInfo(a))
	}
	return out, nil
}

func (p *TidalProvider) AlbumSortTypes() []provider.SortType { return albumSortTypes }

func (p *TidalProvider) DefaultAlbumSort() string { return "favorites" }

// AlbumTracks returns the tracks of an album. Implements provider.AlbumTrackLoader.
func (p *TidalProvider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	c, err := p.ensureClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// The album metadata (used only as a fallback for track fields) and the
	// track list are independent round-trips; fetch them concurrently.
	var (
		album     apiAlbum
		albumErr  error
		tracks    []apiTrack
		tracksErr error
		wg        sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		album, albumErr = c.albumGet(ctx, albumID)
	}()
	go func() {
		defer wg.Done()
		tracks, tracksErr = c.albumTracks(ctx, albumID)
	}()
	wg.Wait()
	if albumErr != nil {
		return nil, p.mapErr(albumErr)
	}
	if tracksErr != nil {
		return nil, p.mapErr(tracksErr)
	}
	return tracksFromAPI(tracks, &album), nil
}

// tracksFromAPI converts API tracks into playlist tracks carrying tidal://
// URIs. albumFallback supplies album metadata for tracks that lack it
// (albums/{id}/tracks nests tracks without an album field). No network calls
// happen here; sources resolve at play time.
func tracksFromAPI(in []apiTrack, albumFallback *apiAlbum) []playlist.Track {
	out := make([]playlist.Track, len(in))
	for i, t := range in {
		out[i] = trackFromAPI(t, albumFallback)
	}
	return out
}

// trackFromAPI maps a single API track to a playlist.Track.
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
		ProviderMeta: map[string]string{provider.MetaTidalID: t.ID.String()},
	}
	if album != nil {
		track.Album = album.Title
		track.Year = provider.YearFromDate(album.ReleaseDate)
	}
	if !t.AllowStreaming || !t.StreamReady {
		track.Unplayable = true
	}
	return track
}

// ResolveSource turns a tidal://track/<id> URI into a playable source when
// playback starts: a direct CDN URL for BTS (AAC) deliveries, or the DASH
// segment list for FLAC. Resolving at play time keeps signed URLs fresh no
// matter how long the track sat in a queue, and reports server-side quality
// downgrades. It is registered as the player's SourceResolver in main.go.
func (p *TidalProvider) ResolveSource(uri string) (streamURL string, segments []string, err error) {
	trackID := strings.TrimPrefix(uri, TrackURIPrefix)
	if trackID == "" || trackID == uri {
		return "", nil, fmt.Errorf("tidal: invalid track URI %q", uri)
	}
	c, err := p.ensureClient()
	if err != nil {
		return "", nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	requested := requestQuality(p.quality)
	pi, err := c.playbackInfo(ctx, trackID, requested)
	if err != nil {
		return "", nil, p.mapErr(err)
	}
	src, err := streamSourceFromManifest(pi)
	if err != nil {
		return "", nil, err
	}
	p.noteDowngrade(src.quality)

	if len(src.segments) > 0 {
		detail := ""
		if pi.BitDepth > 0 && pi.SampleRate > 0 {
			detail = fmt.Sprintf(", %d-bit/%dHz", pi.BitDepth, pi.SampleRate)
		}
		applog.Info("tidal: track %s delivered %s via DASH FLAC (%d segments%s)",
			trackID, src.quality, len(src.segments), detail)
		return "", src.segments, nil
	}
	applog.Info("tidal: track %s delivered %s via direct URL (AAC)", trackID, src.quality)
	streamURLs.register(trackID, src.url)
	return src.url, nil, nil
}

// noteDowngrade warns (once per session) when a FLAC quality setting is being
// served an AAC tier — the device client cliamp uses cannot get FLAC for
// tracks without a hi-res master.
func (p *TidalProvider) noteDowngrade(delivered string) {
	if !isFLACQuality(p.quality) || isFLACQuality(delivered) || delivered == "" {
		return
	}
	if p.downgradeNoticed.CompareAndSwap(false, true) {
		applog.UserWarn("tidal: delivered %s (AAC) — no FLAC for this track with cliamp's client type", delivered)
	}
}

// trackArtist picks the best available artist name for a track.
func trackArtist(t apiTrack, album *apiAlbum) string {
	if t.Artist.Name != "" {
		return t.Artist.Name
	}
	if len(t.Artists) > 0 && t.Artists[0].Name != "" {
		return t.Artists[0].Name
	}
	if album != nil {
		return album.Artist.Name
	}
	return ""
}

// albumInfo maps a Tidal album to provider.AlbumInfo.
func albumInfo(a apiAlbum) provider.AlbumInfo {
	return provider.AlbumInfo{
		ID:         a.ID.String(),
		Name:       a.Title,
		Artist:     a.Artist.Name,
		ArtistID:   a.Artist.ID.String(),
		Year:       provider.YearFromDate(a.ReleaseDate),
		TrackCount: a.NumberOfTracks,
	}
}

// albumPlaceholder converts an album search hit into an album placeholder
// Track. The result is deliberately not playable: Path carries a
// tidal://album/ URI so the entry is identifiable, and playlist.Track.IsAlbum
// signals the UI to expand it through AlbumTracks before queueing.
func albumPlaceholder(a apiAlbum) playlist.Track {
	return playlist.Track{
		Path:   "tidal://album/" + a.ID.String(),
		Title:  a.Title,
		Artist: a.Artist.Name,
		Album:  a.Title,
		Year:   provider.YearFromDate(a.ReleaseDate),
		ProviderMeta: map[string]string{
			playlist.MetaKind:    playlist.MetaKindAlbum,
			playlist.MetaAlbumID: a.ID.String(),
		},
	}
}
