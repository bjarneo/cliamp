package yandex

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

var (
	_ playlist.Provider         = (*Provider)(nil)
	_ provider.Searcher         = (*Provider)(nil)
	_ provider.PlaybackReporter = (*Provider)(nil)
)

// TrackURIPrefix is the custom URI scheme for Yandex Music tracks. Track
// paths are URIs; the player resolves them to fresh signed stream URLs at
// play time (see ResolveSource).
const TrackURIPrefix = "yandex:track:"

const (
	likedPlaylistID  = "likes"
	wavePlaylistID   = "wave"
	playlistIDPrefix = "pl:"
	urlTTL           = 45 * time.Second // signed CDN URLs expire after ~1 minute
)

// Config holds settings for the Yandex Music provider.
type Config struct {
	Enabled bool   // true only when user explicitly sets enabled = true
	Token   string // personal OAuth token
}

// IsSet reports whether the provider should be shown.
func (c Config) IsSet() bool {
	return c.Enabled && strings.TrimSpace(c.Token) != ""
}

// waveState holds the ongoing "Моя волна" radio session so playback reports
// can be fed back and later batches can be fetched for the same session.
type waveState struct {
	sessionID string
	batchID   string
	tracks    []playlist.Track
	keys      []string // "trackId:albumId" keys for continuation requests
}

// Provider implements playlist.Provider and provider.Searcher for Yandex Music.
type Provider struct {
	api    *client
	mu     sync.Mutex
	userID uint64

	playlistCache []playlist.PlaylistInfo
	wave          *waveState
	urlCache      map[string]urlEntry
}

type urlEntry struct {
	url string
	at  time.Time
}

// New creates a Yandex Music provider. Authentication is verified lazily on
// the first API call so a temporary outage does not block startup.
func New(token string) *Provider {
	return &Provider{
		api:      newClient(strings.TrimSpace(token)),
		urlCache: map[string]urlEntry{},
	}
}

// NewFromConfig returns a provider, or nil when Yandex Music is not enabled.
func NewFromConfig(cfg Config) *Provider {
	if !cfg.IsSet() {
		return nil
	}
	return New(cfg.Token)
}

func (p *Provider) Name() string { return "Yandex Music" }

// Refresh clears cached account, playlist, wave session, and stream URL state.
func (p *Provider) Refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.userID = 0
	p.playlistCache = nil
	p.wave = nil
	p.urlCache = map[string]urlEntry{}
}

// CanRefreshPlaylist implements playlist.RefreshablePlaylist: only the wave
// session is ephemeral and benefits from an in-place reload. Regular
// playlists are static server-side objects — refreshing them in place just
// burns API calls.
func (p *Provider) CanRefreshPlaylist(id string) bool {
	return id == wavePlaylistID
}

// userID returns the account user id, verifying the token on first use.
// It takes p.mu itself: all callers must not hold the lock.
func (p *Provider) accountUserID() (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.userID != 0 {
		return p.userID, nil
	}
	uid, err := p.api.accountStatus()
	if err != nil {
		return 0, err
	}
	p.userID = uid
	return uid, nil
}

// Playlists returns the user's liked tracks, the personal wave, and their
// playlists.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	if p.playlistCache != nil {
		out := append([]playlist.PlaylistInfo(nil), p.playlistCache...)
		p.mu.Unlock()
		return out, nil
	}
	p.mu.Unlock()

	uid, err := p.accountUserID()
	if err != nil {
		return nil, err
	}
	lists, err := p.api.playlists(uid)
	if err != nil {
		return nil, err
	}

	infos := []playlist.PlaylistInfo{
		{
			ID:      likedPlaylistID,
			Name:    "Liked Tracks",
			Section: "My Music",
		},
		{
			ID:      wavePlaylistID,
			Name:    "Моя волна",
			Section: "My Music",
		},
	}
	for _, list := range lists {
		// Some endpoints omit the owner object; fall back to the top-level
		// uid field, then to the signed-in user.
		owner := list.Owner.UID
		if owner == 0 {
			owner = list.UID
		}
		if owner == 0 {
			owner = uid
		}
		section := "Saved Playlists"
		if list.Owner.UID == uid {
			section = "My Playlists"
		}
		name := strings.TrimSpace(list.Title)
		if name == "" {
			name = "Untitled Playlist"
		}
		infos = append(infos, playlist.PlaylistInfo{
			// Yandex playlist identity is (owner UID, kind); keeping only
			// kind would resolve saved playlists of other owners against
			// the signed-in user.
			ID:         fmt.Sprintf("%s%d:%d", playlistIDPrefix, owner, list.Kind),
			Name:       name,
			TrackCount: list.TrackCount,
			Section:    section,
		})
	}

	p.mu.Lock()
	p.playlistCache = append([]playlist.PlaylistInfo(nil), infos...)
	p.mu.Unlock()
	return infos, nil
}

// Tracks returns the tracks of the liked list, the wave session, or one of
// the user's playlists.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	playlistID = strings.TrimSpace(playlistID)
	uid, err := p.accountUserID()
	if err != nil {
		return nil, err
	}

	var remote []track
	switch {
	case playlistID == likedPlaylistID:
		ids, err := p.api.likedTracks(uid)
		if err != nil {
			return nil, err
		}
		plain := make([]string, 0, len(ids))
		for _, id := range ids {
			plain = append(plain, plainID(id))
		}
		remote, err = p.api.tracks(plain)
		if err != nil {
			return nil, err
		}
	case playlistID == wavePlaylistID:
		return p.loadWave()
	case strings.HasPrefix(playlistID, playlistIDPrefix):
		// ID format: pl:<owner-uid>:<kind>. Use the playlist owner, not the
		// signed-in user — saved playlists belong to someone else.
		parts := strings.SplitN(strings.TrimPrefix(playlistID, playlistIDPrefix), ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("yandex: invalid playlist id %q", playlistID)
		}
		owner, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("yandex: invalid playlist id %q", playlistID)
		}
		kind, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("yandex: invalid playlist id %q", playlistID)
		}
		remote, err = p.api.playlistTracks(owner, kind)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("yandex: unknown playlist id %q", playlistID)
	}
	return p.toPlaylistTracks(remote), nil
}

// loadWave starts a wave session and loads its initial batch plus up to two
// continuation batches, so one load gives roughly fifteen tracks. Refresh()
// discards the session; the next load starts a fresh wave.
func (p *Provider) loadWave() ([]playlist.Track, error) {
	p.mu.Lock()
	if p.wave != nil {
		out := append([]playlist.Track(nil), p.wave.tracks...)
		p.mu.Unlock()
		return out, nil
	}
	p.mu.Unlock()

	initial, sessionID, batchID, err := p.api.rotorStartWave()
	if err != nil {
		return nil, err
	}
	w := &waveState{sessionID: sessionID, batchID: batchID}
	w.tracks = p.toPlaylistTracks(initial)
	w.keys = trackKeys(initial)

	const extraBatches = 2
	for range extraBatches {
		batch, bid, err := p.api.rotorWaveTracks(sessionID, nil, w.keys)
		if err != nil || len(batch) == 0 {
			// Continuation is best-effort; keep whatever was loaded.
			break
		}
		w.batchID = bid
		w.tracks = append(w.tracks, p.toPlaylistTracks(batch)...)
		w.keys = append(w.keys, trackKeys(batch)...)
	}

	p.mu.Lock()
	if p.wave == nil {
		p.wave = w
	}
	out := append([]playlist.Track(nil), p.wave.tracks...)
	p.mu.Unlock()
	return out, nil
}

// SearchTracks searches the Yandex Music catalog. Implements provider.Searcher.
func (p *Provider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	found, err := p.api.search(q, limit)
	if err != nil {
		return nil, err
	}
	return p.toPlaylistTracks(found), nil
}

// toPlaylistTracks converts API tracks to URI-based playlist tracks. Stream
// URLs are resolved lazily at play time, so listing a playlist costs no
// download-info requests and links never expire before playback.
func (p *Provider) toPlaylistTracks(remote []track) []playlist.Track {
	tracks := make([]playlist.Track, 0, len(remote))
	for _, t := range remote {
		id := string(t.ID)
		if id == "" || t.Error != nil {
			continue
		}
		if real := string(t.RealID); real != "" {
			id = real
		}
		tracks = append(tracks, playlist.Track{
			Path:         TrackURIPrefix + id,
			Title:        trackTitle(t),
			Artist:       joinArtists(t.Artists),
			Album:        joinAlbums(t.Albums),
			Year:         albumYear(t.Albums),
			DurationSecs: millisToSeconds(t.DurationMs),
			Unplayable:   !t.Available,
			Stream:       true, // resolved to a buffered HTTP stream at play time
			ProviderMeta: map[string]string{provider.MetaYandexID: id},
		})
	}
	return tracks
}

// trackKeys returns "trackId:albumId" keys used by wave continuations and
// feedback events.
func trackKeys(ts []track) []string {
	keys := make([]string, 0, len(ts))
	for _, t := range ts {
		id := string(t.ID)
		if id == "" {
			continue
		}
		if len(t.Albums) > 0 && t.Albums[0].ID != 0 {
			id = id + ":" + strconv.FormatUint(t.Albums[0].ID, 10)
		}
		keys = append(keys, id)
	}
	return keys
}

func waveKeyFor(keys []string, id string) string {
	for _, k := range keys {
		if plainID(k) == id {
			return k
		}
	}
	return id
}

// ResolveSource resolves a yandex:track: URI to a fresh signed stream URL at
// play time. Registered as a player.SourceResolver in main.go.
func (p *Provider) ResolveSource(uri string) (string, error) {
	id, ok := strings.CutPrefix(uri, TrackURIPrefix)
	if !ok || id == "" || strings.ContainsAny(id, "/?#") {
		return "", fmt.Errorf("yandex: invalid track uri %q", uri)
	}
	return p.resolveStreamURL(id, false)
}

// resolveStreamURL returns a signed CDN URL for a track, caching entries for
// urlTTL. force bypasses the cache, for example after the URL expired.
func (p *Provider) resolveStreamURL(trackID string, force bool) (string, error) {
	p.mu.Lock()
	if !force {
		if e, ok := p.urlCache[trackID]; ok && time.Since(e.at) < urlTTL {
			p.mu.Unlock()
			return e.url, nil
		}
	}
	p.mu.Unlock()

	u, err := p.api.streamURL(trackID)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	p.urlCache[trackID] = urlEntry{url: u, at: time.Now()}
	p.mu.Unlock()
	return u, nil
}

// CanReportPlayback implements provider.PlaybackReporter.
func (p *Provider) CanReportPlayback(track playlist.Track) bool {
	return track.Meta(provider.MetaYandexID) != ""
}

// ReportNowPlaying implements provider.PlaybackReporter.
func (p *Provider) ReportNowPlaying(track playlist.Track, _ time.Duration, _ bool) error {
	return p.report(track, 0, rotorTrackStarted)
}

// ReportScrobble implements provider.PlaybackReporter.
func (p *Provider) ReportScrobble(track playlist.Track, elapsed, _ time.Duration, _ bool) error {
	return p.report(track, int(elapsed.Seconds()), rotorTrackFinished)
}

// report posts play-audio stats and, when the track belongs to the active
// wave session, a rotor feedback event so the wave adapts to real listening.
func (p *Provider) report(track playlist.Track, playedSeconds int, waveEvent string) error {
	id := track.Meta(provider.MetaYandexID)
	if id == "" {
		return nil
	}
	p.mu.Lock()
	uid := p.userID
	var wave *waveState
	if p.wave != nil {
		for _, k := range p.wave.keys {
			if plainID(k) == id {
				wave = p.wave
				break
			}
		}
	}
	p.mu.Unlock()
	if uid == 0 {
		return nil
	}
	if err := p.api.reportPlayback(uid, id, track.DurationSecs, playedSeconds); err != nil {
		return err
	}
	if wave == nil {
		return nil
	}
	return p.api.rotorWaveFeedback(
		wave.sessionID, wave.batchID, waveEvent,
		waveKeyFor(wave.keys, id),
		float64(track.DurationSecs), float64(playedSeconds),
	)
}

func trackTitle(t track) string {
	title := strings.TrimSpace(t.Title)
	if version := strings.TrimSpace(t.Version); version != "" {
		title = title + " (" + version + ")"
	}
	return title
}

func joinArtists(artists []artist) string {
	names := make([]string, 0, len(artists))
	for _, a := range artists {
		if name := strings.TrimSpace(a.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func joinAlbums(albums []album) string {
	names := make([]string, 0, len(albums))
	for _, a := range albums {
		if name := strings.TrimSpace(a.Title); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func albumYear(albums []album) int {
	for _, a := range albums {
		if a.Year > 0 {
			return a.Year
		}
	}
	return 0
}

func millisToSeconds(ms int) int {
	if ms <= 0 {
		return 0
	}
	return (ms + 999) / 1000
}
