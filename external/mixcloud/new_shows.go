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

const newShowsPageSize = 20

// This is the connection used by the website's NewUploadsQuery. Keep its edge
// order: sorting uploads or merging creator catalogs is not the same feed.
// Reading it must not invoke markUploadNotificationsRead or any other mutation.
const newShowsQuery = `query CliampNewShows($count: Int!, $cursor: String) {
  viewer {
    me { username }
    uploadNotifications(first: $count, after: $cursor) {
      edges { node { slug name audio_length: audioLength is_exclusive: isExclusive created_time: publishDate user: owner { username name: displayName } } }
      pageInfo { endCursor hasNextPage }
    }
  }
}`

var errNewShowsSession = errors.New("mixcloud: Stream requires cookies_from with a signed-in Mixcloud browser session")

type newShowsResponse struct {
	Data struct {
		Viewer *struct {
			Me *struct {
				Username string `json:"username"`
			} `json:"me"`
			Uploads *struct {
				Edges []struct {
					Node *struct {
						apiCloudcast
						Slug string `json:"slug"`
					} `json:"node"`
				} `json:"edges"`
				PageInfo *struct {
					EndCursor   string `json:"endCursor"`
					HasNextPage bool   `json:"hasNextPage"`
				} `json:"pageInfo"`
			} `json:"uploadNotifications"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

func (c *client) newShows(ctx context.Context, browser string, limit int) ([]apiCloudcast, error) {
	if browser == "" {
		return nil, errNewShowsSession
	}
	jar, err := c.loadCookies(ctx, browser)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: load New Shows session: %w", err)
	}
	endpoint, err := url.Parse(c.feedURL)
	if err != nil {
		return nil, errors.New("mixcloud: invalid New Shows endpoint")
	}
	if jar == nil || len(jar.Cookies(endpoint)) == 0 {
		return nil, errNewShowsSession
	}
	// Isolate website cookies from REST requests and never follow a redirect
	// with them. OAuth credentials are not sent to this endpoint.
	session := *c.httpClient
	session.Jar = jar
	session.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	limit = max(limit, 1)
	shows := make([]apiCloudcast, 0, min(limit, newShowsPageSize))
	var cursor *string
	seenCursors := make(map[string]bool)
	username := ""
	for len(shows) < limit {
		variables, _ := json.Marshal(map[string]any{"count": min(newShowsPageSize, limit-len(shows)), "cursor": cursor})
		u := *endpoint
		q := u.Query()
		q.Set("query", newShowsQuery)
		q.Set("variables", string(variables))
		q.Set("operationName", "CliampNewShows")
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("mixcloud: create New Shows request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "cliamp-mixcloud/1")
		resp, err := session.Do(req)
		if err != nil {
			return nil, fmt.Errorf("mixcloud: request New Shows: %w", redactRequestURL(err, "Mixcloud New Shows"))
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			retry, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			return nil, &APIError{StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode), RetryAfter: time.Duration(max(retry, 0)) * time.Second}
		}
		var page newShowsResponse
		err = json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("mixcloud: decode New Shows: %w", err)
		}
		if len(page.Errors) != 0 {
			// GraphQL errors can accompany partial data. Do not silently return
			// a truncated feed, or echo server text that might contain secrets.
			return nil, errors.New("mixcloud: New Shows GraphQL request failed; no partial feed loaded")
		}
		viewer := page.Data.Viewer
		if viewer == nil || viewer.Me == nil || viewer.Me.Username == "" || viewer.Uploads == nil {
			return nil, errNewShowsSession
		}
		if viewer.Uploads.Edges == nil || viewer.Uploads.PageInfo == nil {
			return nil, errors.New("mixcloud: New Shows response is incomplete; no partial feed loaded")
		}
		if username != "" && username != viewer.Me.Username {
			return nil, errors.New("mixcloud: New Shows account changed during pagination; reload the stream")
		}
		username = viewer.Me.Username
		for _, edge := range viewer.Uploads.Edges {
			if edge.Node == nil {
				continue // Deleted shows can have null nodes, with no playable identity.
			}
			node := edge.Node
			if node.User.Username == "" || node.Slug == "" || strings.ContainsAny(node.User.Username+node.Slug, "/?#") {
				return nil, errors.New("mixcloud: New Shows returned an invalid show identity")
			}
			node.Key = "/" + node.User.Username + "/" + node.Slug + "/"
			if !validAPIKey(node.Key) {
				return nil, errors.New("mixcloud: New Shows returned an invalid show key")
			}
			shows = append(shows, node.apiCloudcast)
			if len(shows) == limit {
				return shows, nil
			}
		}
		info := viewer.Uploads.PageInfo
		if !info.HasNextPage {
			return shows, nil
		}
		if info.EndCursor == "" || seenCursors[info.EndCursor] {
			return nil, errors.New("mixcloud: New Shows pagination did not advance; no partial feed loaded")
		}
		seenCursors[info.EndCursor] = true
		cursor = &info.EndCursor
	}
	return shows, nil
}
