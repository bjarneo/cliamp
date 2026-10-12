// Package mass integrates cliamp with a Music Assistant server: its library
// is browsed and searched over the HTTP API at POST {URL}/api, authenticated
// with a long-lived token as a Bearer header, and its players are controlled
// over the same API. Audio reaches cliamp through the sendspin package.
package mass

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.AlbumBrowser     = (*Provider)(nil)
	_ provider.AlbumTrackLoader = (*Provider)(nil)
	_ provider.Searcher         = (*Provider)(nil)
	_ provider.CatalogSearcher  = (*Provider)(nil)
	_ provider.TrackPager       = (*Provider)(nil)
	_ playlist.Provider         = (*Provider)(nil)
)

// MetaURI is the ProviderMeta key holding a track's Music Assistant URI.
const MetaURI = "mass.uri"

// pathPrefix marks playlist paths of Music Assistant tracks. They are never
// opened as files: Music Assistant plays them through the Sendspin player.
const pathPrefix = "mass://"

// IsTrackPath reports whether path is a Music Assistant track.
func IsTrackPath(path string) bool { return strings.HasPrefix(path, pathPrefix) }

// httpClient is used for all Music Assistant API calls with a finite timeout.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// maxResponseBody limits JSON API responses to 20 MB to prevent unbounded
// memory growth.
const maxResponseBody = 20 << 20

const (
	cmdLibraryPlaylists = "music/playlists/library_items"
	cmdLibraryAlbums    = "music/albums/library_items"
	cmdPlaylistTracks   = "music/playlists/playlist_tracks"
	cmdAlbumTracks      = "music/albums/album_tracks"
	cmdSearch           = "music/search"
)

// Provider implements playlist.Provider, provider.Searcher and
// provider.AlbumBrowser against a Music Assistant server, and controls its
// players. Requests carry the API token as a Bearer header; the token is
// never logged.
type Provider struct {
	baseURL string
	token   string

	mu            sync.Mutex
	playlistCache []playlist.PlaylistInfo
	trackCache    map[string][]playlist.Track
	search        *librarySearch // set while search results replace the library
	searchGen     int
}

// NewFromConfig creates a Provider for cfg. Returns nil when the URL or token
// is missing. The token is kept in memory and sent only as the
// Authorization header.
func NewFromConfig(cfg config.MassConfig) *Provider {
	if !cfg.IsSet() {
		return nil
	}
	return &Provider{
		baseURL:    strings.TrimRight(cfg.URL, "/"),
		token:      cfg.Token,
		trackCache: make(map[string][]playlist.Track),
	}
}

// NewFromEnv creates a Provider from MUSIC_ASSISTANT_URL and
// MUSIC_ASSISTANT_TOKEN, keeping the other settings from cfg. Returns nil if
// either is unset.
func NewFromEnv(cfg config.MassConfig) *Provider {
	cfg.URL = os.Getenv("MUSIC_ASSISTANT_URL")
	cfg.Token = os.Getenv("MUSIC_ASSISTANT_TOKEN")
	return NewFromConfig(cfg)
}

// URL is the server's base URL. The Sendspin server runs on the same host.
func (p *Provider) URL() string { return p.baseURL }

func (p *Provider) Name() string {
	return "Music Assistant"
}

// Refresh clears cached playlist and track data so the next Playlists/Tracks
// call re-fetches from the server. Implements playlist.Refresher.
func (p *Provider) Refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.playlistCache = nil
	p.trackCache = make(map[string][]playlist.Track)
}

// rpcRequest performs a POST to the MA JSON-RPC API and decodes the command
// result into out. The MA API returns the command result body directly (not
// wrapped), so out receives that payload.
func (p *Provider) rpcRequest(ctx context.Context, command string, args map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{
		"message_id": fmt.Sprintf("cliamp-%d", rand.Int64()),
		"command":    command,
		"args":       args,
	})
	if err != nil {
		return fmt.Errorf("mass marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mass request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mass api %s: %w", command, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read a small snippet for the error message, never the full body.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("mass api %s: http status %s: %s", command, resp.Status, strings.TrimSpace(string(snippet)))
	}

	if out == nil {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("mass api %s: decode: %w", command, err)
	}
	return nil
}

// Playlists lists the library folders (recently played, artists, albums,
// tracks, genres, radio stations) followed by the user's playlists, or the
// results of a library search. Implements playlist.Provider.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	if lists, ok := p.searchLists(); ok {
		return lists, nil
	}
	p.mu.Lock()
	if p.playlistCache != nil {
		out := p.playlistCache
		p.mu.Unlock()
		return out, nil
	}
	p.mu.Unlock()

	var items []massPlaylist
	if err := p.rpcRequest(context.Background(), cmdLibraryPlaylists, map[string]any{
		"limit": 500, "offset": 0, "summary": true,
	}, &items); err != nil {
		return nil, err
	}
	infos := make([]playlist.PlaylistInfo, 0, len(libraryFolders)+len(items))
	for _, f := range libraryFolders {
		infos = append(infos, playlist.PlaylistInfo{ID: folderPrefix + f.key, Name: f.name, Section: "Library"})
	}
	for _, it := range items {
		if it.Name == "" {
			continue
		}
		infos = append(infos, playlist.PlaylistInfo{
			ID:         it.URI,
			Name:       it.Name,
			TrackCount: it.TrackCount,
			Section:    "Playlists",
		})
	}

	p.mu.Lock()
	p.playlistCache = infos
	p.mu.Unlock()
	return infos, nil
}

// Tracks returns the rows of a library folder, or the tracks of the
// playlist, album, artist or genre whose URI is id (with or without the
// mass:// prefix). Implements playlist.Provider.
func (p *Provider) Tracks(id string) ([]playlist.Track, error) {
	if id == searchTracksID {
		return p.searchTracks()
	}
	if strings.HasPrefix(id, folderPrefix) {
		return p.folderTracks(id)
	}
	itemURI := strings.TrimPrefix(id, pathPrefix)

	p.mu.Lock()
	if cached, ok := p.trackCache[itemURI]; ok {
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	var tracks []massTrack
	var args map[string]any
	var command string
	switch {
	case isMediaURI(itemURI, "playlist"):
		command = cmdPlaylistTracks
		args = itemArgs(itemURI)
	case isMediaURI(itemURI, "album"):
		command = cmdAlbumTracks
		args = itemArgs(itemURI)
	case isMediaURI(itemURI, "artist"):
		command = "music/artists/artist_tracks"
		args = itemArgs(itemURI)
	case isMediaURI(itemURI, "genre"):
		command = "music/genres/tracks"
		args = map[string]any{"item_id": itemIDFromURI(itemURI)}
	default:
		return nil, fmt.Errorf("mass: unsupported item uri %q", id)
	}
	if err := p.rpcRequest(context.Background(), command, args, &tracks); err != nil {
		return nil, err
	}

	out := make([]playlist.Track, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, p.trackFromMass(t))
	}

	p.mu.Lock()
	p.trackCache[itemURI] = out
	p.mu.Unlock()
	return out, nil
}

// SearchTracks searches the MA server for tracks matching query across all
// providers + the library. Implements provider.Searcher.
func (p *Provider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	if limit <= 0 {
		limit = 25
	}
	var result struct {
		Tracks []massTrack `json:"tracks"`
	}
	if err := p.rpcRequest(ctx, cmdSearch, map[string]any{
		"search_query": query,
		"media_types":  []string{"track"},
		"limit":        limit,
	}, &result); err != nil {
		return nil, err
	}
	out := make([]playlist.Track, 0, len(result.Tracks))
	for _, t := range result.Tracks {
		if t.Name == "" {
			continue
		}
		out = append(out, p.trackFromMass(t))
	}
	return out, nil
}

// AlbumList returns a page of the MA library albums. Implements
// provider.AlbumBrowser.
func (p *Provider) AlbumList(sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	var items []massAlbum
	if err := p.rpcRequest(context.Background(), cmdLibraryAlbums, map[string]any{
		"limit": size, "offset": offset, "summary": true,
	}, &items); err != nil {
		return nil, err
	}
	albums := make([]provider.AlbumInfo, 0, len(items))
	for _, a := range items {
		if a.Name == "" {
			continue
		}
		albums = append(albums, provider.AlbumInfo{
			ID:         a.URI,
			Name:       a.Name,
			Artist:     a.artistStr(),
			Year:       a.Year,
			TrackCount: a.trackCount(),
		})
	}
	return albums, nil
}

// AlbumSortTypes returns the sort options. Music Assistant lists the library
// in a single canonical order, so only a default is offered.
func (p *Provider) AlbumSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "default", Label: "Default"}}
}

// DefaultAlbumSort returns the default sort id.
func (p *Provider) DefaultAlbumSort() string { return "default" }

// AlbumTracks returns the tracks of an album identified by uri.
// Implements provider.AlbumTrackLoader.
func (p *Provider) AlbumTracks(albumURI string) ([]playlist.Track, error) {
	return p.Tracks(albumURI)
}

// massPlaylist mirrors the slim library playlist item returned by the MA API.
type massPlaylist struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
	// TrackCount may be absent on summary items; defaults to 0 (unknown).
	TrackCount int `json:"track_count"`
}

// massNamed unmarshals either a JSON string or {"name": "..."}.
type massNamed struct{ Name string }

func (n *massNamed) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &n.Name)
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	n.Name = obj.Name
	return nil
}

type massAlbum struct {
	URI        string      `json:"uri"`
	Name       string      `json:"name"`
	Year       int         `json:"year"`
	Artists    []massNamed `json:"artists"`
	TrackCount int         `json:"track_count"`
}

func namedJoin(ns []massNamed) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		if n.Name != "" {
			parts = append(parts, n.Name)
		}
	}
	return strings.Join(parts, ", ")
}

func (a massAlbum) artistStr() string { return namedJoin(a.Artists) }
func (a massAlbum) trackCount() int   { return a.TrackCount }

type massTrack struct {
	URI         string      `json:"uri"`
	ItemID      string      `json:"item_id"`
	Name        string      `json:"name"`
	Duration    float64     `json:"duration"` // seconds; library tracks have fractions
	TrackNumber int         `json:"track_number"`
	Album       *massAlbum  `json:"album"`
	Artists     []massNamed `json:"artists"`
}

func (t massTrack) artistStr() string {
	if s := namedJoin(t.Artists); s != "" {
		return s
	}
	if t.Album != nil {
		return t.Album.artistStr()
	}
	return ""
}

// trackFromMass maps a Music Assistant track to a playlist row. The path only
// identifies the item; playback goes through the Sendspin player.
func (p *Provider) trackFromMass(t massTrack) playlist.Track {
	uri := t.URI
	if uri == "" && t.ItemID != "" {
		uri = "library://track/" + t.ItemID
	}
	tr := playlist.Track{
		Path:         pathPrefix + uri,
		Title:        t.Name,
		Artist:       t.artistStr(),
		DurationSecs: int(math.Round(t.Duration)),
		TrackNumber:  t.TrackNumber,
		ProviderMeta: map[string]string{MetaURI: uri},
	}
	if t.Album != nil {
		tr.Album = t.Album.Name
	}
	return tr
}

// isMediaURI reports whether uri has the given MA media-type prefix (e.g.
// spotify://playlist/... or filesystem://album/...).
func isMediaURI(uri, mediaType string) bool {
	colon := strings.Index(uri, "://")
	if colon < 0 {
		return false
	}
	rest := uri[colon+3:]
	return strings.HasPrefix(rest, mediaType+"/")
}

// itemIDFromURI extracts the provider item id from a MA media URI
// (scheme://mediaType/provider/id → id).
func itemIDFromURI(uri string) string {
	colon := strings.Index(uri, "://")
	if colon < 0 {
		return uri
	}
	rest := uri[colon+3:]
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return rest
	}
	rest = rest[slash+1:]
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[i+1:]
	}
	return rest
}
