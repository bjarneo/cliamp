// Package netease implements a playlist.Provider for NetEase Cloud Music.
package netease

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"cliamp/playlist"
	"cliamp/provider"
	"cliamp/resolve"
)

var (
	_ playlist.Provider = (*Provider)(nil)
	_ provider.Searcher = (*Provider)(nil)
)

const (
	defaultAPIBase = "https://music.163.com"
	probeURL       = "https://music.163.com/#/playlist?id=3778678"
	apiTimeout     = 15 * time.Second
)

// ErrNotAuthenticated is returned when browser cookies do not contain a
// signed-in NetEase Cloud Music account.
var ErrNotAuthenticated = errors.New("netease: browser session is not signed in")

// Config holds settings for the NetEase provider.
type Config struct {
	Enabled     bool
	CookiesFrom string
	UserID      string
}

// IsSet reports whether the provider should be exposed.
func (c Config) IsSet() bool { return c.Enabled }

// Account describes the signed-in NetEase account visible through cookies.
type Account struct {
	UserID   string
	Nickname string
	VIPType  int
}

type chartPlaylist struct {
	id   string
	name string
}

var charts = []chartPlaylist{
	{id: "3778678", name: "Hot Songs"},
	{id: "3779629", name: "New Songs"},
	{id: "19723756", name: "Rising Songs"},
	{id: "2884035", name: "Original Songs"},
}

// Provider implements playlist.Provider and provider.Searcher.
type Provider struct {
	apiBase     string
	httpClient  *http.Client
	cookiesFrom string
	userID      string

	mu           sync.Mutex
	cookieHeader string
	playlists    []playlist.PlaylistInfo
	account      *Account
}

// NewFromConfig returns a provider, or nil when NetEase is not enabled.
// Sets resolve's yt-dlp cookies as a side effect when CookiesFrom is non-empty
// so URL resolution uses the same signed-in browser session.
func NewFromConfig(cfg Config) *Provider {
	if !cfg.Enabled {
		return nil
	}
	cfg.CookiesFrom = strings.TrimSpace(cfg.CookiesFrom)
	if cfg.CookiesFrom != "" {
		resolve.SetYTDLCookiesFrom(cfg.CookiesFrom)
	}
	return New(cfg)
}

// New creates a NetEase provider.
func New(cfg Config) *Provider {
	return &Provider{
		apiBase:     defaultAPIBase,
		httpClient:  &http.Client{Timeout: apiTimeout},
		cookiesFrom: strings.TrimSpace(cfg.CookiesFrom),
		userID:      strings.TrimSpace(cfg.UserID),
	}
}

func newWithBase(cfg Config, base string) *Provider {
	p := New(cfg)
	p.apiBase = strings.TrimRight(base, "/")
	return p
}

func (p *Provider) Name() string { return "NetEase Cloud Music" }

// Refresh clears cached account and playlist state.
func (p *Provider) Refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cookieHeader = ""
	p.playlists = nil
	p.account = nil
}

// CheckLogin verifies that the given browser has a signed-in NetEase account.
func CheckLogin(ctx context.Context, browser string) (Account, error) {
	p := New(Config{Enabled: true, CookiesFrom: browser})
	return p.Account(ctx)
}

// Account returns the signed-in account from browser cookies.
func (p *Provider) Account(ctx context.Context) (Account, error) {
	p.mu.Lock()
	if p.account != nil {
		acc := *p.account
		p.mu.Unlock()
		return acc, nil
	}
	p.mu.Unlock()

	var resp accountResponse
	if err := p.apiGet(ctx, "/api/nuser/account/get", nil, &resp); err != nil {
		return Account{}, err
	}
	if resp.Code != http.StatusOK {
		return Account{}, fmt.Errorf("netease: account request failed with code %d", resp.Code)
	}
	uid := resp.Account.ID
	if uid == 0 {
		uid = resp.Profile.UserID
	}
	if uid == 0 {
		return Account{}, ErrNotAuthenticated
	}
	acc := Account{
		UserID:   strconv.FormatInt(uid, 10),
		Nickname: resp.Profile.Nickname,
		VIPType:  firstNonZero(resp.Profile.VIPType, resp.Account.VIPType),
	}

	p.mu.Lock()
	p.account = &acc
	if p.userID == "" {
		p.userID = acc.UserID
	}
	p.mu.Unlock()
	return acc, nil
}

// Playlists returns account playlists followed by public chart playlists.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	if p.playlists != nil {
		out := append([]playlist.PlaylistInfo(nil), p.playlists...)
		p.mu.Unlock()
		return out, nil
	}
	userID := p.userID
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	var infos []playlist.PlaylistInfo
	if userID == "" && p.cookiesFrom != "" {
		acc, err := p.Account(ctx)
		if err != nil {
			return nil, err
		}
		userID = acc.UserID
	}
	if userID != "" {
		userLists, err := p.userPlaylists(ctx, userID)
		if err != nil {
			return nil, err
		}
		infos = append(infos, userLists...)
	}
	infos = append(infos, chartPlaylists()...)

	p.mu.Lock()
	p.playlists = append([]playlist.PlaylistInfo(nil), infos...)
	p.mu.Unlock()
	return infos, nil
}

// Tracks returns tracks for a user playlist or built-in chart playlist.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	id, err := cleanPlaylistID(playlistID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()

	params := url.Values{"id": {id}}
	var resp playlistDetailResponse
	if err := p.apiGet(ctx, "/api/playlist/detail", params, &resp); err != nil {
		return nil, err
	}
	if resp.Code != http.StatusOK {
		return nil, fmt.Errorf("netease: playlist detail failed with code %d", resp.Code)
	}
	return songsToTracks(resp.Result.Tracks), nil
}

// SearchTracks searches NetEase songs.
func (p *Provider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	params := url.Values{
		"s":      {q},
		"type":   {"1"},
		"offset": {"0"},
		"limit":  {strconv.Itoa(limit)},
	}
	var resp searchResponse
	if err := p.apiGet(ctx, "/api/search/get/web", params, &resp); err != nil {
		return nil, err
	}
	if resp.Code != http.StatusOK {
		return nil, fmt.Errorf("netease: search failed with code %d", resp.Code)
	}
	return songsToTracks(resp.Result.Songs), nil
}

func (p *Provider) userPlaylists(ctx context.Context, userID string) ([]playlist.PlaylistInfo, error) {
	uid, err := strconv.ParseInt(strings.TrimSpace(userID), 10, 64)
	if err != nil || uid <= 0 {
		return nil, fmt.Errorf("netease: invalid user_id %q", userID)
	}
	const pageSize = 100
	var out []playlist.PlaylistInfo
	for offset := 0; ; offset += pageSize {
		params := url.Values{
			"uid":    {strconv.FormatInt(uid, 10)},
			"limit":  {strconv.Itoa(pageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		var resp userPlaylistsResponse
		if err := p.apiGet(ctx, "/api/user/playlist", params, &resp); err != nil {
			return nil, err
		}
		if resp.Code != http.StatusOK {
			return nil, fmt.Errorf("netease: playlist request failed with code %d", resp.Code)
		}
		for _, item := range resp.Playlist {
			section := "Saved Playlists"
			if item.UserID == uid {
				section = "My Playlists"
			}
			name := strings.TrimSpace(item.Name)
			if item.SpecialType == 5 {
				name = "Liked Songs"
				section = "My Playlists"
			}
			if name == "" {
				name = "Untitled Playlist"
			}
			out = append(out, playlist.PlaylistInfo{
				ID:         "user:" + strconv.FormatInt(item.ID, 10),
				Name:       name,
				TrackCount: item.TrackCount,
				Section:    section,
			})
		}
		if len(resp.Playlist) < pageSize {
			break
		}
	}
	return out, nil
}

func chartPlaylists() []playlist.PlaylistInfo {
	out := make([]playlist.PlaylistInfo, 0, len(charts))
	for _, chart := range charts {
		out = append(out, playlist.PlaylistInfo{
			ID:      "chart:" + chart.id,
			Name:    chart.name,
			Section: "Charts",
		})
	}
	return out
}

func cleanPlaylistID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("netease: empty playlist id")
	}
	if v, ok := strings.CutPrefix(id, "user:"); ok {
		id = v
	} else if v, ok := strings.CutPrefix(id, "chart:"); ok {
		id = v
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return "", fmt.Errorf("netease: invalid playlist id %q", id)
	}
	return id, nil
}

func songsToTracks(songs []song) []playlist.Track {
	tracks := make([]playlist.Track, 0, len(songs))
	for _, s := range songs {
		if s.ID == 0 {
			continue
		}
		tracks = append(tracks, playlist.Track{
			Path:         songURL(s.ID),
			Title:        s.Name,
			Artist:       joinArtists(s.Artists),
			Album:        s.Album.Name,
			TrackNumber:  s.TrackNumber,
			Stream:       true,
			DurationSecs: millisToSeconds(s.DurationMS),
			ProviderMeta: map[string]string{provider.MetaNetEaseID: strconv.FormatInt(s.ID, 10)},
		})
	}
	return tracks
}

func songURL(id int64) string {
	return "https://music.163.com/#/song?id=" + strconv.FormatInt(id, 10)
}

func joinArtists(artists []artist) string {
	if len(artists) == 0 {
		return ""
	}
	names := make([]string, 0, len(artists))
	for _, a := range artists {
		if name := strings.TrimSpace(a.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func millisToSeconds(ms int) int {
	if ms <= 0 {
		return 0
	}
	return (ms + 999) / 1000
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

func (p *Provider) apiGet(ctx context.Context, path string, params url.Values, out any) error {
	endpoint, err := url.Parse(p.apiBase + path)
	if err != nil {
		return err
	}
	if params != nil {
		endpoint.RawQuery = params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", p.apiBase+"/")
	if header, err := p.ensureCookieHeader(ctx); err != nil {
		return err
	} else if header != "" {
		req.Header.Set("Cookie", header)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("netease: http status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("netease: decode response: %w", err)
	}
	return nil
}

func (p *Provider) ensureCookieHeader(ctx context.Context) (string, error) {
	if p.cookiesFrom == "" {
		return "", nil
	}
	p.mu.Lock()
	if p.cookieHeader != "" {
		header := p.cookieHeader
		p.mu.Unlock()
		return header, nil
	}
	p.mu.Unlock()

	header, err := extractBrowserCookieHeader(ctx, p.cookiesFrom)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	p.cookieHeader = header
	p.mu.Unlock()
	return header, nil
}

func extractBrowserCookieHeader(ctx context.Context, browser string) (string, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return "", fmt.Errorf("yt-dlp not found. Install with: %s", ytDLPInstallHint())
	}
	tmp, err := os.CreateTemp("", "cliamp-netease-cookies-*.txt")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	tmp.Close()
	os.Remove(path)
	defer os.Remove(path)

	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "yt-dlp",
		"--cookies-from-browser", browser,
		"--cookies", path,
		"--flat-playlist",
		"--playlist-end", "1",
		"--socket-timeout", "15",
		"--print", "title",
		probeURL,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("netease: load browser cookies: %s: %w", msg, err)
		}
		return "", fmt.Errorf("netease: load browser cookies: %w", err)
	}
	header, err := cookieHeaderFromNetscapeFile(path)
	if err != nil {
		return "", err
	}
	if header == "" {
		return "", fmt.Errorf("netease: no NetEase cookies found in browser session")
	}
	return header, nil
}

func ytDLPInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install yt-dlp"
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			return "sudo apt install yt-dlp"
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			return "sudo pacman -S yt-dlp"
		}
		return "pip install yt-dlp"
	case "windows":
		return "winget install yt-dlp"
	default:
		return "pip install yt-dlp"
	}
}

func cookieHeaderFromNetscapeFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	seen := map[string]bool{}
	var pairs []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "# Netscape") || strings.HasPrefix(line, "# This file") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		domain := strings.TrimPrefix(fields[0], "#HttpOnly_")
		if !isNetEaseCookieDomain(domain) {
			continue
		}
		name := fields[5]
		value := fields[6]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		pairs = append(pairs, name+"="+value)
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(pairs, "; "), nil
}

func isNetEaseCookieDomain(domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(domain), ".")
	return domain == "163.com" || domain == "music.163.com" || strings.HasSuffix(domain, ".music.163.com")
}

type accountResponse struct {
	Code    int `json:"code"`
	Account struct {
		ID      int64 `json:"id"`
		VIPType int   `json:"vipType"`
	} `json:"account"`
	Profile struct {
		UserID   int64  `json:"userId"`
		Nickname string `json:"nickname"`
		VIPType  int    `json:"vipType"`
	} `json:"profile"`
}

type userPlaylistsResponse struct {
	Code     int            `json:"code"`
	Playlist []playlistItem `json:"playlist"`
}

type playlistItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	UserID      int64  `json:"userId"`
	TrackCount  int    `json:"trackCount"`
	SpecialType int    `json:"specialType"`
}

type playlistDetailResponse struct {
	Code   int `json:"code"`
	Result struct {
		Tracks []song `json:"tracks"`
	} `json:"result"`
}

type searchResponse struct {
	Code   int `json:"code"`
	Result struct {
		Songs []song `json:"songs"`
	} `json:"result"`
}

type song struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	DurationMS  int      `json:"duration"`
	TrackNumber int      `json:"no"`
	Artists     []artist `json:"artists"`
	Album       album    `json:"album"`
}

type artist struct {
	Name string `json:"name"`
}

type album struct {
	Name string `json:"name"`
}
