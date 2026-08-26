package mixcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIBase = "https://api.mixcloud.com"
	apiPageSize    = 50
	maxAPIPageSize = 100
	maxErrorBody   = 1 << 20
)

// APIError is returned for a non-successful response from Mixcloud.
// RetryAfter is populated for rate-limit responses when Mixcloud supplies it.
type APIError struct {
	StatusCode int
	Type       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e == nil {
		return "mixcloud: API error"
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("mixcloud: API returned %d: %s (retry after %s)", e.StatusCode, msg, e.RetryAfter)
	}
	return fmt.Sprintf("mixcloud: API returned %d: %s", e.StatusCode, msg)
}

type client struct {
	baseURL     string
	accessToken string
	httpClient  *http.Client
}

func newClient(accessToken string) *client {
	return &client{
		baseURL:     defaultAPIBase,
		accessToken: strings.TrimSpace(accessToken),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *client) requestURL(apiPath string, query url.Values) (string, error) {
	if c == nil {
		return "", errors.New("mixcloud: nil API client")
	}
	if !validAPIPath(apiPath) {
		return "", fmt.Errorf("mixcloud: invalid API path %q", apiPath)
	}
	u, err := url.Parse(strings.TrimRight(c.baseURL, "/") + apiPath)
	if err != nil {
		return "", fmt.Errorf("mixcloud: build API URL: %w", err)
	}
	q := u.Query()
	for key, values := range query {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	if c.accessToken != "" {
		q.Set("access_token", c.accessToken)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *client) getJSON(ctx context.Context, apiPath string, query url.Values, dst any) error {
	requestURL, err := c.requestURL(apiPath, query)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("mixcloud: create request for %s: %w", apiPath, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cliamp-mixcloud/1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mixcloud: GET %s: %w", apiPath, redactRequestURL(err, apiPath))
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeAPIError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("mixcloud: decode %s: %w", apiPath, err)
	}
	return nil
}

// redactRequestURL prevents net/http's *url.Error from including an OAuth
// token embedded in the query string while retaining errors.Is/errors.As
// behavior for the underlying transport or context error.
func redactRequestURL(err error, safeURL string) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	redacted := *urlErr
	redacted.URL = safeURL
	return &redacted
}

func decodeAPIError(resp *http.Response) error {
	var envelope apiErrorEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&envelope); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("mixcloud: API returned %s: decode error response: %w", resp.Status, err)
	}
	retrySeconds := envelope.Error.RetryAfter
	if header := strings.TrimSpace(resp.Header.Get("Retry-After")); header != "" {
		if n, err := strconv.Atoi(header); err == nil && n >= 0 {
			retrySeconds = n
		}
	}
	return &APIError{
		StatusCode: resp.StatusCode,
		Type:       envelope.Error.Type,
		Message:    envelope.Error.Message,
		RetryAfter: time.Duration(retrySeconds) * time.Second,
	}
}

func pageQuery(offset, limit int) url.Values {
	q := make(url.Values)
	q.Set("offset", strconv.Itoa(max(offset, 0)))
	q.Set("limit", strconv.Itoa(max(limit, 1)))
	return q
}

func (c *client) cloudcastPage(ctx context.Context, apiPath string, offset, limit int) ([]apiCloudcast, bool, error) {
	var page apiPage[apiCloudcast]
	if err := c.getJSON(ctx, apiPath, pageQuery(offset, min(max(limit, 1), maxAPIPageSize)), &page); err != nil {
		return nil, false, err
	}
	return page.Data, page.Paging.Next != "", nil
}

func (c *client) cloudcasts(ctx context.Context, apiPath string, limit int) ([]apiCloudcast, error) {
	return pagedItems[apiCloudcast](ctx, c, apiPath, limit)
}

func (c *client) categories(ctx context.Context) ([]apiCategory, error) {
	var page apiPage[apiCategory]
	if err := c.getJSON(ctx, "/categories/", nil, &page); err != nil {
		return nil, err
	}
	return page.Data, nil
}

func (c *client) users(ctx context.Context, apiPath string, limit int) ([]apiUser, error) {
	return pagedItems[apiUser](ctx, c, apiPath, limit)
}

func (c *client) playlists(ctx context.Context, apiPath string, limit int) ([]apiPlaylist, error) {
	return pagedItems[apiPlaylist](ctx, c, apiPath, limit)
}

func (c *client) activities(ctx context.Context, apiPath string, limit int) ([]apiActivity, error) {
	return pagedItems[apiActivity](ctx, c, apiPath, limit)
}

func pagedItems[T any](ctx context.Context, c *client, apiPath string, limit int) ([]T, error) {
	limit = max(limit, 1)
	items := make([]T, 0, min(limit, apiPageSize))
	for offset := 0; len(items) < limit; {
		var page apiPage[T]
		pageLimit := min(apiPageSize, limit-len(items))
		if err := c.getJSON(ctx, apiPath, pageQuery(offset, pageLimit), &page); err != nil {
			return nil, err
		}
		items = append(items, page.Data...)
		if len(items) > limit {
			items = items[:limit]
			break
		}
		if page.Paging.Next == "" || len(page.Data) == 0 {
			break
		}
		offset += len(page.Data)
	}
	return items, nil
}

func (c *client) cloudcast(ctx context.Context, key string) (apiCloudcast, error) {
	var item apiCloudcast
	if err := c.getJSON(ctx, ensureTrailingSlash(key), nil, &item); err != nil {
		return apiCloudcast{}, err
	}
	return item, nil
}

func (c *client) user(ctx context.Context, key string) (apiUser, error) {
	var item apiUser
	if err := c.getJSON(ctx, ensureTrailingSlash(key), nil, &item); err != nil {
		return apiUser{}, err
	}
	return item, nil
}

func (c *client) searchCloudcasts(ctx context.Context, query string, limit int) ([]apiCloudcast, error) {
	q := pageQuery(0, min(max(limit, 1), maxAPIPageSize))
	q.Set("q", query)
	q.Set("type", "cloudcast")
	var page apiPage[apiCloudcast]
	if err := c.getJSON(ctx, "/search/", q, &page); err != nil {
		return nil, err
	}
	return page.Data, nil
}

func (c *client) searchTags(ctx context.Context, query string, limit int) ([]apiTag, error) {
	q := pageQuery(0, min(max(limit, 1), maxAPIPageSize))
	q.Set("q", query)
	q.Set("type", "tag")
	var page apiPage[apiTag]
	if err := c.getJSON(ctx, "/search/", q, &page); err != nil {
		return nil, err
	}
	return page.Data, nil
}

func ensureTrailingSlash(s string) string {
	s = "/" + strings.Trim(s, "/") + "/"
	return s
}

func validAPIPath(apiPath string) bool {
	if !strings.HasPrefix(apiPath, "/") || strings.Contains(apiPath, "://") || strings.ContainsAny(apiPath, "?#") {
		return false
	}
	for _, segment := range strings.Split(apiPath, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validAPIKey(key string) bool {
	return strings.TrimSpace(key) == key && key != "" && validAPIPath(key)
}
