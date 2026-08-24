// Package lyrion implements a cliamp provider for Lyrion Music Server (LMS,
// formerly Logitech Media Server).
//
// The library is browsed over LMS's JSON-RPC endpoint (POST /jsonrpc.js) and
// tracks are played from its HTTP file endpoint (/music/<track_id>/download)
// by cliamp's own playback engine. cliamp does not act as, or drive, an LMS
// player — see docs/lyrion.md for what that implies.
package lyrion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const (
	// maxResponseBody bounds a single JSON-RPC response so a misbehaving or
	// hostile server cannot exhaust memory.
	maxResponseBody = 32 << 20

	// pageSize is the per-request row count for queries whose result set is
	// bounded by its container (one playlist, one album). LMS treats a count
	// larger than the available rows as "all of them".
	pageSize = 10000

	// rpcPath is the JSON-RPC endpoint, served on the same port as the web UI.
	rpcPath = "/jsonrpc.js"

	// musicPath is the prefix of the HTTP file endpoint.
	musicPath = "/music/"

	// downloadSuffix requests the original, untranscoded file.
	downloadSuffix = "/download"
)

// TrackURIPrefix is the custom URI scheme for Lyrion tracks. Track paths carry
// this rather than a direct HTTP URL so that no credential is ever written to
// a track: cliamp persists Track.Path to resume state and to the play history,
// and LMS authenticates with the user's actual password rather than a
// revocable token. ResolveSource expands it at play time.
const TrackURIPrefix = "lyrion://track/"

var httpClient = &http.Client{Timeout: 30 * time.Second}

// Album sort orders. The IDs are passed to LMS as its `sort:` parameter.
const (
	SortByName       = "album"
	SortByNew        = "new"
	SortByChanged    = "changed"
	SortByLastPlayed = "lastplayed"
	SortByPlayCount  = "playcount"
	SortByRandom     = "random"
)

// Client talks to one Lyrion Music Server instance.
type Client struct {
	url      string // normalised base URL, no trailing slash
	user     string // empty unless the server is password protected
	password string

	// showUnplayable includes plugin-contributed tracks and playlists in
	// results instead of filtering them out. See isLocal.
	showUnplayable bool
}

// New creates a Client for the server at serverURL. user and password may be
// empty for a server without password protection.
func New(serverURL, user, password string) *Client {
	return &Client{
		url:      strings.TrimRight(strings.TrimSpace(serverURL), "/"),
		user:     user,
		password: password,
	}
}

// NewFromConfig creates a Client from a config.LyrionConfig value. It returns
// nil when no server URL is configured.
func NewFromConfig(cfg config.LyrionConfig) *Client {
	if !cfg.IsSet() {
		return nil
	}
	c := New(cfg.URL, cfg.User, cfg.Password)
	c.showUnplayable = cfg.ShowUnplayable
	return c
}

// NewFromEnv creates a Client from LYRION_URL, LYRION_USER, and LYRION_PASS.
// It returns nil when LYRION_URL is unset; the credentials are optional.
func NewFromEnv() *Client {
	u := os.Getenv("LYRION_URL")
	if u == "" {
		return nil
	}
	c := New(u, os.Getenv("LYRION_USER"), os.Getenv("LYRION_PASS"))
	c.showUnplayable = strings.EqualFold(os.Getenv("LYRION_SHOW_UNPLAYABLE"), "true")
	return c
}

func (c *Client) Name() string { return "lyrion" }

// Ping verifies the server is reachable and speaking JSON-RPC.
func (c *Client) Ping() error {
	var res struct {
		Version string `json:"_version"`
	}
	return c.request(context.Background(), []any{"version", "?"}, &res)
}

// request issues one JSON-RPC command and decodes result into out.
//
// The player slot of the envelope is deliberately empty: every command this
// provider sends is a server-scoped library query, and cliamp does not address
// an LMS player.
func (c *Client) request(ctx context.Context, command []any, out any) error {
	body, err := json.Marshal(map[string]any{
		"id":     1,
		"method": "slim.request",
		"params": []any{"", command},
	})
	if err != nil {
		return fmt.Errorf("lyrion: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+rpcPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("lyrion: %s: %w", c.url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cliamp/1.0 (https://github.com/bjarneo/cliamp)")
	if c.user != "" || c.password != "" {
		req.SetBasicAuth(c.user, c.password)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("lyrion: %s: %w", c.url, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("lyrion: %s: authentication failed (http %s) — check user and password", c.url, resp.Status)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("lyrion: %s: http status %s", c.url, resp.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("lyrion: %s: %w", c.url, err)
	}

	var env struct {
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("lyrion: %s: decode response: %w", c.url, err)
	}
	if env.Error != nil {
		return fmt.Errorf("lyrion: %s: server error: %v", c.url, env.Error)
	}
	if len(env.Result) == 0 {
		return fmt.Errorf("lyrion: %s: response contained no result", c.url)
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("lyrion: %s: decode result: %w", c.url, err)
	}
	return nil
}

// --- Browsing ---------------------------------------------------------------

func (c *Client) Playlists() ([]playlist.PlaylistInfo, error) {
	var res struct {
		Loop []struct {
			ID   flexString `json:"id"`
			Name string     `json:"playlist"`
			URL  string     `json:"url"`
		} `json:"playlists_loop"`
	}
	cmd := []any{"playlists", 0, pageSize, "tags:u"}
	if err := c.request(context.Background(), cmd, &res); err != nil {
		return nil, err
	}
	out := make([]playlist.PlaylistInfo, 0, len(res.Loop))
	for _, p := range res.Loop {
		// A plugin-imported playlist contains only that plugin's tracks, so
		// hiding those tracks would leave it empty — hide the playlist too.
		if !c.showUnplayable && !isLocal(p.URL) {
			continue
		}
		out = append(out, playlist.PlaylistInfo{ID: p.ID.String(), Name: p.Name})
	}
	return out, nil
}

func (c *Client) Tracks(playlistID string) ([]playlist.Track, error) {
	var res struct {
		Loop []song `json:"playlisttracks_loop"`
	}
	cmd := []any{"playlists", "tracks", 0, pageSize, "playlist_id:" + playlistID, "tags:" + songTags}
	if err := c.request(context.Background(), cmd, &res); err != nil {
		return nil, err
	}
	return c.toTracks(res.Loop), nil
}

func (c *Client) Artists() ([]provider.ArtistInfo, error) {
	var res struct {
		Loop []struct {
			ID   flexString `json:"id"`
			Name string     `json:"artist"`
		} `json:"artists_loop"`
	}
	if err := c.request(context.Background(), []any{"artists", 0, pageSize}, &res); err != nil {
		return nil, err
	}
	out := make([]provider.ArtistInfo, 0, len(res.Loop))
	for _, a := range res.Loop {
		out = append(out, provider.ArtistInfo{ID: a.ID.String(), Name: a.Name})
	}
	return out, nil
}

func (c *Client) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	return c.albums([]any{"albums", 0, pageSize, "artist_id:" + artistID, "tags:" + albumTags})
}

func (c *Client) AlbumList(sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	if sortType == "" {
		sortType = c.DefaultAlbumSort()
	}
	return c.albums([]any{"albums", offset, size, "sort:" + sortType, "tags:" + albumTags})
}

func (c *Client) albums(cmd []any) ([]provider.AlbumInfo, error) {
	var res struct {
		Loop []struct {
			ID       flexString `json:"id"`
			Name     string     `json:"album"`
			Artist   string     `json:"artist"`
			ArtistID flexString `json:"artist_id"`
			Year     flexInt    `json:"year"`
		} `json:"albums_loop"`
	}
	if err := c.request(context.Background(), cmd, &res); err != nil {
		return nil, err
	}
	out := make([]provider.AlbumInfo, 0, len(res.Loop))
	for _, a := range res.Loop {
		out = append(out, provider.AlbumInfo{
			ID:       a.ID.String(),
			Name:     a.Name,
			Artist:   a.Artist,
			ArtistID: a.ArtistID.String(),
			Year:     int(a.Year),
		})
	}
	return out, nil
}

func (c *Client) AlbumSortTypes() []provider.SortType {
	return []provider.SortType{
		{ID: SortByName, Label: "By Name"},
		{ID: SortByNew, Label: "Recently Added"},
		{ID: SortByChanged, Label: "Recently Changed"},
		{ID: SortByLastPlayed, Label: "Last Played"},
		{ID: SortByPlayCount, Label: "Most Played"},
		{ID: SortByRandom, Label: "Random"},
	}
}

func (c *Client) DefaultAlbumSort() string { return SortByName }

func (c *Client) AlbumTracks(albumID string) ([]playlist.Track, error) {
	var res struct {
		Loop []song `json:"titles_loop"`
	}
	// sort:albumtrack orders by disc then track number, which is the order an
	// album is meant to be heard in.
	cmd := []any{"titles", 0, pageSize, "album_id:" + albumID, "sort:albumtrack", "tags:" + songTags}
	if err := c.request(context.Background(), cmd, &res); err != nil {
		return nil, err
	}
	return c.toTracks(res.Loop), nil
}

// --- Search -----------------------------------------------------------------

func (c *Client) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	if limit <= 0 {
		limit = 100
	}
	var res struct {
		Loop []song `json:"titles_loop"`
	}
	cmd := []any{"titles", 0, limit, "search:" + query, "tags:" + songTags}
	if err := c.request(ctx, cmd, &res); err != nil {
		return nil, err
	}
	return c.toTracks(res.Loop), nil
}

// --- Track mapping ----------------------------------------------------------

// songTags is the tag set requested for track queries. LMS returns only the
// fields a query asks for, so every field consumed by toTrack must appear here.
// Verified against Slim/Control/Queries.pm (%tagMap):
//
//	a artist   l album   d duration   u url
//	t tracknum y year    g genre
//
// id and title need no tag: _songData sets them unconditionally.
const songTags = "aldtygu"

// localScheme marks tracks backed by a real file in the library. LMS serves
// only these from its download endpoint — see toTrack.
const localScheme = "file://"

// albumTags is the tag set requested for album queries. The albums query has
// its own tag handling separate from %tagMap — there uppercase S is artist_id,
// while lowercase s is a sort textkey.
//
//	l album   y year   a artist   S artist_id
const albumTags = "lyaS"

// song is one row of an LMS titles_loop or playlisttracks_loop response.
type song struct {
	ID       flexString `json:"id"`
	Title    string     `json:"title"`
	Artist   string     `json:"artist"`
	Album    string     `json:"album"`
	Genre    string     `json:"genre"`
	Year     flexInt    `json:"year"`
	TrackNum flexInt    `json:"tracknum"`
	Duration flexFloat  `json:"duration"`
	URL      string     `json:"url"`
}

func (c *Client) toTracks(songs []song) []playlist.Track {
	out := make([]playlist.Track, 0, len(songs))
	for _, s := range songs {
		t := c.toTrack(s)
		// Plugin-contributed tracks cannot be streamed to cliamp at all, so by
		// default they are omitted rather than listed as dead entries. Setting
		// show_unplayable surfaces them, flagged, for a complete view.
		if t.Unplayable && !c.showUnplayable {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (c *Client) toTrack(s song) playlist.Track {
	id := s.ID.String()
	return playlist.Track{
		Path:         TrackURIPrefix + id,
		Title:        s.Title,
		Artist:       s.Artist,
		Album:        s.Album,
		Genre:        s.Genre,
		Year:         int(s.Year),
		TrackNumber:  int(s.TrackNum),
		DurationSecs: int(s.Duration),
		Stream:       true,
		Unplayable:   !isLocal(s.URL),
		ProviderMeta: map[string]string{provider.MetaLyrionID: id},
	}
}

// isLocal reports whether an LMS track URL refers to a file in the library.
//
// A library can also hold tracks contributed by server plugins (Spotify via
// Spotty, and similar), which carry their own URL scheme. LMS accepts a
// download request for those but never sends any bytes — the connection just
// hangs — so they are marked Unplayable and cliamp skips past them instead of
// stalling. Playing them would mean speaking each plugin's protocol, which is
// what cliamp's own providers for those services already do.
//
// A track with no URL at all is treated as local: the caller may simply not
// have requested the url tag, and refusing to play everything would be worse
// than occasionally offering a track that fails.
func isLocal(rawURL string) bool {
	return rawURL == "" || strings.HasPrefix(rawURL, localScheme)
}

// StreamURL returns the playable HTTP URL for an LMS track ID.
//
// When credentials are configured they are embedded as URL userinfo: Go's
// net/http transport turns that into a Basic Authorization header, so the
// player authenticates without needing provider-specific knowledge.
//
// This URL is produced at play time by ResolveSource and never stored on a
// track — see TrackURIPrefix for why.
func (c *Client) StreamURL(trackID string) string {
	base := c.url
	if c.user != "" || c.password != "" {
		if u, err := url.Parse(c.url); err == nil {
			u.User = url.UserPassword(c.user, c.password)
			base = u.String()
		}
	}
	return base + musicPath + url.PathEscape(trackID) + downloadSuffix
}

// ResolveSource turns a lyrion://track/<id> URI into a playable URL when
// playback starts, applying credentials only at that moment.
func (c *Client) ResolveSource(uri string) (streamURL string, segments []string, err error) {
	id := strings.TrimPrefix(uri, TrackURIPrefix)
	if id == "" || id == uri {
		return "", nil, fmt.Errorf("lyrion: not a track URI: %q", uri)
	}
	return c.StreamURL(id), nil, nil
}

// IsStreamURL reports whether path is an LMS file-endpoint URL. The player uses
// it to select the buffered download pipeline, since these are finite files
// rather than live streams.
func IsStreamURL(path string) bool {
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	p := u.Path
	if !strings.HasPrefix(p, musicPath) {
		return false
	}
	// Accept /download and transcoding variants such as /download.mp3.
	rest := p[len(musicPath):]
	idx := strings.LastIndex(rest, downloadSuffix)
	if idx <= 0 {
		return false
	}
	// Nothing may follow "download" except a format extension.
	tail := rest[idx+len(downloadSuffix):]
	return tail == "" || strings.HasPrefix(tail, ".")
}

// --- Flexible JSON scalars --------------------------------------------------
//
// LMS is inconsistent about whether a scalar arrives as a JSON number or a
// quoted string, and it omits fields entirely rather than sending null. These
// types accept either form and treat anything unparseable as the zero value,
// so one oddly-typed field cannot fail a whole query.

type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if s, ok := unquote(b); ok {
		*f = flexString(s)
		return nil
	}
	*f = flexString(strings.TrimSpace(string(b)))
	return nil
}

func (f flexString) String() string { return string(f) }

type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s, _ := unquote(b)
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		*f = flexInt(v)
	}
	return nil
}

type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s, _ := unquote(b)
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		*f = flexFloat(v)
	}
	return nil
}

// unquote strips JSON string quoting when present, reporting whether b was a
// quoted string. Non-string input is returned as its raw literal.
func unquote(b []byte) (string, bool) {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		return s, true
	}
	return string(b), false
}
