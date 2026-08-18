// Package audiobookshelf implements a cliamp provider for an Audiobookshelf
// server, exposing its audiobooks and podcasts as playlists.
package audiobookshelf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

const maxResponseBody = 10 << 20

// Client speaks to an Audiobookshelf server over its HTTP API.
type Client struct {
	baseURL    string
	user       string
	password   string
	libraries  []string
	httpClient *http.Client

	// mu guards the lazily-populated fields below, which are read and written
	// from concurrent tea.Cmd goroutines. It is never held across network I/O.
	mu           sync.Mutex
	token        string
	libraryCache []Library
}

// NewClient returns a Client for the given server URL and credentials. When
// libraries is non-empty, only libraries with those names are visible.
func NewClient(baseURL, token, user, password string, libraries []string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		user:       user,
		password:   password,
		libraries:  libraries,
		httpClient: defaultHTTPClient,
	}
}

// SetHTTPClient overrides the HTTP client used for requests. Mainly for tests
// that inject a custom transport.
func (c *Client) SetHTTPClient(hc *http.Client) { c.httpClient = hc }

// ClearCache discards the cached library list.
func (c *Client) ClearCache() {
	c.mu.Lock()
	c.libraryCache = nil
	c.mu.Unlock()
}

// Ping checks that the server is reachable and the credentials are accepted.
func (c *Client) Ping() error {
	_, err := c.Libraries()
	return err
}

// Libraries returns the libraries visible to the user, filtered by the
// configured name list. Results are cached after the first successful call.
func (c *Client) Libraries() ([]Library, error) {
	c.mu.Lock()
	cached := c.libraryCache
	c.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	var resp librariesResponse
	if err := c.get("/api/libraries", nil, &resp); err != nil {
		return nil, err
	}

	out := make([]Library, 0, len(resp.Libraries))
	for _, lib := range resp.Libraries {
		if c.allowed(lib.Name) {
			out = append(out, lib)
		}
	}

	c.mu.Lock()
	c.libraryCache = out
	c.mu.Unlock()
	return out, nil
}

func (c *Client) allowed(name string) bool {
	if len(c.libraries) == 0 {
		return true
	}
	for _, want := range c.libraries {
		if strings.EqualFold(strings.TrimSpace(want), name) {
			return true
		}
	}
	return false
}

// StreamURL returns an authenticated audio URL for one file of an item.
func (c *Client) StreamURL(itemID, ino string) string {
	_ = c.ensureAuth()
	u := c.baseURL + "/api/items/" + url.PathEscape(itemID) + "/file/" + url.PathEscape(ino)
	if token := c.authToken(); token != "" {
		u += "?" + url.Values{"token": {token}}.Encode()
	}
	return u
}

// IsStreamURL reports whether the URL is an Audiobookshelf item-file endpoint.
// Used by the player to route these URLs through the buffered ffmpeg pipeline.
func IsStreamURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.Contains(p, "/api/items/") && strings.Contains(p, "/file/")
}

func (c *Client) authToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// get issues a GET, retrying once with a fresh login when the server rejects
// the cached token and credentials are configured. Password logins hand back a
// token that can expire, so a 401 is recoverable without user action.
func (c *Client) get(p string, params url.Values, out any) error {
	code, err := c.getOnce(p, params, out)
	if code == http.StatusUnauthorized && c.canRelogin() {
		c.clearToken()
		_, err = c.getOnce(p, params, out)
	}
	return err
}

// canRelogin reports whether a rejected request can be retried after logging
// in again. A token from config cannot be renewed.
func (c *Client) canRelogin() bool { return c.user != "" && c.password != "" }

func (c *Client) clearToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

func (c *Client) getOnce(p string, params url.Values, out any) (int, error) {
	if err := c.ensureAuth(); err != nil {
		return 0, err
	}

	u := c.baseURL + p
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("%s: http status %s", p, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("%s: %w", p, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("%s: %w", p, err)
	}
	return resp.StatusCode, nil
}

// sendJSON issues a request with a JSON body, retrying once with a fresh login
// on a 401 when credentials are configured.
func (c *Client) sendJSON(method, p string, payload any) error {
	code, err := c.sendJSONOnce(method, p, payload)
	if code == http.StatusUnauthorized && c.canRelogin() {
		c.clearToken()
		_, err = c.sendJSONOnce(method, p, payload)
	}
	return err
}

func (c *Client) sendJSONOnce(method, p string, payload any) (int, error) {
	if err := c.ensureAuth(); err != nil {
		return 0, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p, err)
	}

	req, err := http.NewRequest(method, c.baseURL+p, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", p, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return resp.StatusCode, fmt.Errorf("%s: http status %s", p, resp.Status)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	return resp.StatusCode, nil
}

const itemsPageLimit = 500
const itemsMaxPages = 1000

// Items returns every item in a library, following pagination.
func (c *Client) Items(libraryID string) ([]LibraryItem, error) {
	p := "/api/libraries/" + url.PathEscape(libraryID) + "/items"
	var out []LibraryItem
	for page := 0; page < itemsMaxPages; page++ {
		params := url.Values{
			"limit": {strconv.Itoa(itemsPageLimit)},
			"page":  {strconv.Itoa(page)},
			"sort":  {"media.metadata.title"},
		}
		var resp itemsResponse
		if err := c.get(p, params, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Results...)
		if len(resp.Results) < itemsPageLimit {
			return out, nil
		}
		if resp.Total > 0 && len(out) >= resp.Total {
			return out, nil
		}
	}
	return out, nil
}

// Item returns one library item with its audio files, chapters, and episodes.
// The expanded form is required: the plain response omits media.tracks and
// media.duration, which books are built from.
func (c *Client) Item(itemID string) (LibraryItem, error) {
	var item LibraryItem
	params := url.Values{"expanded": {"1"}}
	if err := c.get("/api/items/"+url.PathEscape(itemID), params, &item); err != nil {
		return LibraryItem{}, err
	}
	return item, nil
}

// Authors returns the authors in a book library.
func (c *Client) Authors(libraryID string) ([]Author, error) {
	var resp authorsResponse
	if err := c.get("/api/libraries/"+url.PathEscape(libraryID)+"/authors", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Authors, nil
}

// AuthorItems returns the books written by one author.
func (c *Client) AuthorItems(authorID string) ([]LibraryItem, error) {
	var a Author
	params := url.Values{"include": {"items"}}
	if err := c.get("/api/authors/"+url.PathEscape(authorID), params, &a); err != nil {
		return nil, err
	}
	return a.LibraryItems, nil
}

// Search returns library items matching query, books first.
func (c *Client) Search(libraryID, query string, limit int) ([]LibraryItem, error) {
	if limit <= 0 {
		limit = 10
	}
	params := url.Values{"q": {query}, "limit": {strconv.Itoa(limit)}}
	var resp searchResponse
	if err := c.get("/api/libraries/"+url.PathEscape(libraryID)+"/search", params, &resp); err != nil {
		return nil, err
	}
	out := make([]LibraryItem, 0, len(resp.Book)+len(resp.Podcast))
	for _, hit := range resp.Book {
		out = append(out, hit.LibraryItem)
	}
	for _, hit := range resp.Podcast {
		out = append(out, hit.LibraryItem)
	}
	return out, nil
}

// Progress returns every stored listening position for the current user.
func (c *Client) Progress() ([]MediaProgress, error) {
	var me meResponse
	if err := c.get("/api/me", nil, &me); err != nil {
		return nil, err
	}
	return me.MediaProgress, nil
}

// UpdateProgress stores the listening position for an item, or for one podcast
// episode when episodeID is non-empty.
func (c *Client) UpdateProgress(itemID, episodeID string, currentTime, duration float64, isFinished bool) error {
	p := "/api/me/progress/" + url.PathEscape(itemID)
	if episodeID != "" {
		p += "/" + url.PathEscape(episodeID)
	}
	payload := map[string]any{
		"currentTime": currentTime,
		"isFinished":  isFinished,
	}
	if duration > 0 {
		payload["duration"] = duration
		payload["progress"] = currentTime / duration
	}
	return c.sendJSON(http.MethodPatch, p, payload)
}

func (c *Client) ensureAuth() error {
	c.mu.Lock()
	have := c.token != ""
	c.mu.Unlock()
	if have {
		return nil
	}
	if c.user == "" || c.password == "" {
		return fmt.Errorf("missing token or user/password")
	}

	body, err := json.Marshal(map[string]string{"username": c.user, "password": c.password})
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login: http status %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	var out loginResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	token := out.User.Token
	if token == "" {
		token = out.AccessToken
	}
	if token == "" {
		return fmt.Errorf("login: missing token")
	}

	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
	return nil
}
