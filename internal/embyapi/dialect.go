package embyapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/provider"
)

// dialect captures the handful of behaviors that differ between Emby and
// Jellyfin. Everything else in Client is shared.
type dialect interface {
	name() string                                                // error-wrapping prefix
	ping(c *Client) error                                        // reachability and token check
	metaKey() string                                             // playlist.Track ProviderMeta key
	applyAuth(req *http.Request, token, userID, deviceID string) // set auth headers
	discoverUserID(c *Client) (string, error)                    // user-id discovery strategy
}

// embyDialect speaks Emby's `Authorization: Emby ...` scheme and discovers the
// user id with an API-key fallback.
type embyDialect struct{}

func (embyDialect) name() string    { return "emby" }
func (embyDialect) metaKey() string { return provider.MetaEmbyID }

// ping checks /System/Info, which accepts a session token and an API key.
func (embyDialect) ping(c *Client) error {
	var raw json.RawMessage
	return c.get("/System/Info", nil, &raw)
}

func (embyDialect) applyAuth(req *http.Request, token, userID, deviceID string) {
	if token != "" {
		req.Header.Set("X-Emby-Token", token)
		req.Header.Set("Authorization", embyAuthHeader(userID, token, deviceID))
	} else {
		req.Header.Set("Authorization", embyUnauthHeader(deviceID))
	}
}

func (embyDialect) discoverUserID(c *Client) (string, error) {
	// Try /Users/Me first (works for session tokens from password auth).
	var me userDTO
	if err := c.get("/Users/Me", nil, &me); err == nil && me.ID != "" {
		c.setUserID(me.ID)
		return me.ID, nil
	}

	// Fall back to /Users for API key auth (server-level key has no "me").
	return c.userIDFromList()
}

// unauthHeader / authHeader build Emby's Authorization header values.
func embyUnauthHeader(deviceID string) string {
	return fmt.Sprintf(`Emby Client="%s", Device="%s", DeviceId="%s", Version="%s"`,
		appmeta.ClientName(), appmeta.DeviceName(), deviceID, appmeta.Version())
}

// embyAuthHeader builds Emby's Authorization header value for the given
// device, user, and token.
func embyAuthHeader(userID, token, deviceID string) string {
	if userID != "" {
		return fmt.Sprintf(`Emby UserId="%s", Client="%s", Device="%s", DeviceId="%s", Version="%s", Token="%s"`,
			userID, appmeta.ClientName(), appmeta.DeviceName(), deviceID, appmeta.Version(), token)
	}
	return fmt.Sprintf(`Emby Client="%s", Device="%s", DeviceId="%s", Version="%s", Token="%s"`,
		appmeta.ClientName(), appmeta.DeviceName(), deviceID, appmeta.Version(), token)
}

// jellyfinDialect speaks Jellyfin's `MediaBrowser ...` auth scheme. Newer
// servers (10.11.2+, 10.12) read the client from the standard Authorization
// header on /Users/AuthenticateByName; older servers read X-Emby-Authorization.
// We send both.
type jellyfinDialect struct{}

func (jellyfinDialect) name() string { return "jellyfin" }

// ping checks /Users/Me. API keys aren't owned by a user, so /Users/Me
// returns a 400 (documented as "Token is not owned by a user.", sent without
// a body), even when a username is configured alongside the key. For that
// case, ping lists /Users to prove the key is valid.
func (jellyfinDialect) ping(c *Client) error {
	var raw json.RawMessage
	err := c.get("/Users/Me", nil, &raw)
	if err != nil && c.password == "" && c.authToken() != "" && isHTTPStatus(err, http.StatusBadRequest) {
		return c.get("/Users", nil, &raw)
	}
	return err
}

// metaKey returns the ProviderMeta key Jellyfin item ids are stored under.
func (jellyfinDialect) metaKey() string { return provider.MetaJellyfinID }

// applyAuth sets Jellyfin authorization headers on req, using both the
// standard Authorization and legacy X-Emby-Authorization headers for
// compatibility across server versions.
func (jellyfinDialect) applyAuth(req *http.Request, token, _, deviceID string) {
	auth := fmt.Sprintf(`MediaBrowser Client="%s", Device="%s", DeviceId="%s", Version="%s"`,
		appmeta.ClientName(), appmeta.DeviceName(), deviceID, appmeta.Version())
	if token != "" {
		auth += fmt.Sprintf(`, Token="%s"`, token)
		req.Header.Set("X-Emby-Token", token)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("X-Emby-Authorization", auth)
}

// discoverUserID resolves the user id via /Users/Me, falling back to the
// /Users listing only for the API-key case where /Users/Me returns 400.
func (jellyfinDialect) discoverUserID(c *Client) (string, error) {
	// Try /Users/Me first (works for session tokens from password auth).
	var me userDTO
	meErr := c.get("/Users/Me", nil, &me)
	if meErr == nil {
		if me.ID != "" {
			c.setUserID(me.ID)
			return me.ID, nil
		}
		return "", fmt.Errorf("jellyfin: current user response missing id")
	}

	// API-key auth is the only case where /Users/Me legitimately fails (a key
	// isn't owned by a user, so it returns 400). Preserve every other error so
	// 401/403/5xx responses surface through UserID instead of being masked.
	if !isHTTPStatus(meErr, http.StatusBadRequest) {
		return "", fmt.Errorf("jellyfin: could not discover user id: %w", meErr)
	}

	// Fall back to /Users for API key auth (a key isn't owned by a user, so
	// /Users/Me returns 400).
	return c.userIDFromList()
}

// userIDFromList discovers the user id from the /Users listing, which is the
// API-key fallback of both dialects. It prefers the user that matches the
// configured user name. Without a configured name it takes the first entry.
func (c *Client) userIDFromList() (string, error) {
	name := c.dialect.name()
	var users []userDTO
	if err := c.get("/Users", nil, &users); err != nil {
		return "", fmt.Errorf("%s: could not discover user id (set user_id in config): %w", name, err)
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, c.user) {
			c.setUserID(u.ID)
			return u.ID, nil
		}
	}
	if c.user != "" {
		return "", fmt.Errorf("%s: user %q not found — check the user name in config", name, c.user)
	}
	if len(users) > 0 && users[0].ID != "" {
		c.setUserID(users[0].ID)
		return users[0].ID, nil
	}
	return "", fmt.Errorf("%s: could not discover user id — set user_id in config", name)
}

// isHTTPStatus reports whether err is an httpError with the given status code.
func isHTTPStatus(err error, code int) bool {
	var httpErr *httpError
	return errors.As(err, &httpErr) && httpErr.statusCode == code
}
