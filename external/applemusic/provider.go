// Package applemusic plays Apple Music through Music.app on macOS.
//
// Music.app holds the subscription and decodes the stream, so cliamp never
// touches the protected data. A muted process tap hands the decoded audio to
// cliamp, where it goes through the normal pipeline: volume in dB, the EQ, the
// visualizer, and lyric timing all work as they do for any other track. Music's
// own volume stays at 100% during playback and its EQ is left alone.
//
// The tap is not a recorder: samples are held only in a short playback buffer.
package applemusic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/internal/httpclient"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

const (
	trackPrefix   = "applemusic:track:"
	libraryPrefix = "applemusic:lib:"
	libraryID     = "library"
	searchURL     = "https://itunes.apple.com/search"
)

var (
	_ playlist.Provider       = (*Provider)(nil)
	_ provider.Searcher       = (*Provider)(nil)
	_ provider.CustomStreamer = (*Provider)(nil)
	_ provider.Closer         = (*Provider)(nil)
)

var apiClient = httpclient.NewAPI(15 * time.Second)

// Provider exposes the Apple Music catalog and the user's Music library.
type Provider struct {
	country string
	engine  *engine
}

// New returns a Provider, or nil when this is not macOS or the helpers are not
// installed next to the cliamp binary.
func New() *Provider {
	if runtime.GOOS != "darwin" {
		return nil
	}
	dir := helperDir()
	if dir == "" {
		return nil
	}
	country := strings.ToLower(os.Getenv("CLIAMP_APPLEMUSIC_COUNTRY"))
	if country == "" {
		country = "us"
	}
	return &Provider{country: country, engine: newEngine(dir)}
}

func helperDir() string {
	if dir := os.Getenv("CLIAMP_APPLEMUSIC_HELPER"); dir != "" {
		return dir
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Join(filepath.Dir(exe), "applemusic-helper")
	if _, err := os.Stat(filepath.Join(dir, "cliamp-am-ctl")); err != nil {
		return ""
	}
	return dir
}

// Name returns the display name.
func (p *Provider) Name() string { return "Apple Music" }

// Playlists lists the Music library as one playlist. Catalog songs come from
// search.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	return []playlist.PlaylistInfo{{ID: libraryID, Name: "Music Library"}}, nil
}

// Tracks returns the tracks of the Music library. They play from their catalog
// page, found when playback starts.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	if playlistID != libraryID {
		return nil, fmt.Errorf("apple music: unknown playlist %q", playlistID)
	}
	out, err := osascript(context.Background(), scriptLibrary)
	if err != nil {
		return nil, fmt.Errorf("apple music: read library: %w", err)
	}
	var tracks []playlist.Track
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			continue
		}
		secs, _ := parseFloat(f[4])
		tracks = append(tracks, libraryTrack(f[0], f[1], f[2], f[3], int(secs)))
	}
	return tracks, nil
}

func libraryTrack(id, title, artist, album string, secs int) playlist.Track {
	q := url.Values{"t": {title}, "a": {artist}, "d": {strconv.Itoa(secs)}}
	return playlist.Track{
		Path:         libraryPrefix + id + "?" + q.Encode(),
		Title:        title,
		Artist:       artist,
		Album:        album,
		DurationSecs: secs,
		Stream:       true,
		ProviderMeta: map[string]string{provider.MetaAppleMusicID: id},
	}
}

type searchResponse struct {
	Results []struct {
		TrackID        int64  `json:"trackId"`
		TrackName      string `json:"trackName"`
		ArtistName     string `json:"artistName"`
		CollectionName string `json:"collectionName"`
		TrackViewURL   string `json:"trackViewUrl"`
		TrackTimeMs    int64  `json:"trackTimeMillis"`
		TrackNumber    int    `json:"trackNumber"`
		Genre          string `json:"primaryGenreName"`
		ReleaseDate    string `json:"releaseDate"`
	} `json:"results"`
}

// SearchTracks searches the Apple Music catalog. The API needs no token.
func (p *Provider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	q := url.Values{
		"term": {query}, "entity": {"song"}, "country": {p.country},
		"limit": {strconv.Itoa(limit)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apple music: search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("apple music: search: %s", resp.Status)
	}
	var body searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("apple music: search: %w", err)
	}
	tracks := make([]playlist.Track, 0, len(body.Results))
	for _, r := range body.Results {
		if r.TrackViewURL == "" {
			continue
		}
		secs := int(r.TrackTimeMs / 1000)
		v := url.Values{
			"t": {r.TrackName}, "a": {r.ArtistName}, "d": {strconv.Itoa(secs)},
			"u": {musicURL(r.TrackViewURL)},
		}
		tracks = append(tracks, playlist.Track{
			Path:         trackPrefix + strconv.FormatInt(r.TrackID, 10) + "?" + v.Encode(),
			Title:        r.TrackName,
			Artist:       r.ArtistName,
			Album:        r.CollectionName,
			Genre:        r.Genre,
			TrackNumber:  r.TrackNumber,
			Year:         provider.YearFromDate(r.ReleaseDate),
			DurationSecs: secs,
			Stream:       true,
			ProviderMeta: map[string]string{provider.MetaAppleMusicID: strconv.FormatInt(r.TrackID, 10)},
		})
	}
	return tracks, nil
}

// musicURL turns a web link into the music:// form Music.app opens, without the
// tracking parameter.
func musicURL(web string) string {
	u, err := url.Parse(web)
	if err != nil {
		return web
	}
	q := u.Query()
	q.Del("uo")
	u.RawQuery = q.Encode()
	u.Scheme = "music"
	return u.String()
}

// request is one parsed track URI.
type request struct {
	id, title, artist, link string
	library                 bool
	secs                    int
}

func parseURI(uri string) (request, error) {
	var r request
	rest, ok := strings.CutPrefix(uri, trackPrefix)
	if !ok {
		rest, ok = strings.CutPrefix(uri, libraryPrefix)
		r.library = true
	}
	if !ok {
		return r, fmt.Errorf("apple music: invalid URI %q", uri)
	}
	id, query, _ := strings.Cut(rest, "?")
	v, err := url.ParseQuery(query)
	if err != nil {
		return r, fmt.Errorf("apple music: invalid URI %q: %w", uri, err)
	}
	r.id, r.title, r.artist, r.link = id, v.Get("t"), v.Get("a"), v.Get("u")
	r.secs, _ = strconv.Atoi(v.Get("d"))
	if r.id == "" || r.title == "" {
		return r, errors.New("apple music: URI has no track")
	}
	if !r.library && r.link == "" {
		return r, errors.New("apple music: URI has no link")
	}
	return r, nil
}

// resolveLink finds the catalog page of a library track. Music can only be
// asked to play a song from its page, and a catalog page also plays a song the
// user owns. A library file with no catalog match cannot play here.
func (p *Provider) resolveLink(ctx context.Context, req request) (string, error) {
	tracks, err := p.SearchTracks(ctx, req.artist+" "+req.title, 25)
	if err != nil {
		return "", err
	}
	for _, t := range tracks {
		if !strings.EqualFold(t.Title, req.title) || !strings.EqualFold(t.Artist, req.artist) {
			continue
		}
		if found, err := parseURI(t.Path); err == nil {
			return found.link, nil
		}
	}
	return "", fmt.Errorf("apple music: %q is not in the %s catalog", req.title, p.country)
}

// URISchemes returns the URI prefix this provider plays.
func (p *Provider) URISchemes() []string { return []string{"applemusic:"} }

// NewStreamer returns a streamer that starts Music on its first audio pull, so
// preloading the next track does not interrupt the current one.
func (p *Provider) NewStreamer(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	req, err := parseURI(uri)
	if err != nil {
		return nil, beep.Format{}, 0, err
	}
	rate, err := p.engine.ensureTap()
	if err != nil {
		return nil, beep.Format{}, 0, err
	}
	s := newStreamer(p.engine, req, rate, p.resolveLink)
	format := beep.Format{SampleRate: beep.SampleRate(rate), NumChannels: 2, Precision: 4}
	return s, format, time.Duration(req.secs) * time.Second, nil
}

// Close stops Music, restores its volume, and ends the tap.
func (p *Provider) Close() { p.engine.shutdown() }
