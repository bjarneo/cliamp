package embyapi

import (
	"fmt"
	"net/http"
	"strings"

	"cliamp/internal/appmeta"
	"cliamp/provider"
)

// dialect captures the handful of behaviors that differ between Emby and
// Jellyfin. Everything else in Client is shared.
type dialect interface {
	name() string                                                // error-wrapping prefix
	pingPath() string                                            // endpoint Ping hits
	metaKey() string                                             // playlist.Track ProviderMeta key
	applyAuth(req *http.Request, token, userID, deviceID string) // set auth headers
	discoverUserID(c *Client) (string, error)                    // user-id discovery strategy
}

// embyDialect speaks Emby's `Authorization: Emby ...` scheme and discovers the
// user id with an API-key fallback.
type embyDialect struct{}

func (embyDialect) name() string     { return "emby" }
func (embyDialect) pingPath() string { return "/System/Info" }
func (embyDialect) metaKey() string  { return provider.MetaEmbyID }

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
	var users []userDTO
	if err := c.get("/Users", nil, &users); err != nil {
		return "", fmt.Errorf("emby: could not discover user id (set user_id in config): %w", err)
	}
	// Prefer user matching the configured username; otherwise take first entry.
	for _, u := range users {
		if strings.EqualFold(u.Name, c.user) {
			c.setUserID(u.ID)
			return u.ID, nil
		}
	}
	if c.user != "" {
		return "", fmt.Errorf("emby: user %q not found — check the user name in config", c.user)
	}
	if len(users) > 0 && users[0].ID != "" {
		c.setUserID(users[0].ID)
		return users[0].ID, nil
	}
	return "", fmt.Errorf("emby: could not discover user id — set user_id in config")
}

// unauthHeader / authHeader build Emby's Authorization header values.
func embyUnauthHeader(deviceID string) string {
	return fmt.Sprintf(`Emby Client="%s", Device="%s", DeviceId="%s", Version="%s"`,
		appmeta.ClientName(), appmeta.DeviceName(), deviceID, appmeta.Version())
}

func embyAuthHeader(userID, token, deviceID string) string {
	if userID != "" {
		return fmt.Sprintf(`Emby UserId="%s", Client="%s", Device="%s", DeviceId="%s", Version="%s", Token="%s"`,
			userID, appmeta.ClientName(), appmeta.DeviceName(), deviceID, appmeta.Version(), token)
	}
	return fmt.Sprintf(`Emby Client="%s", Device="%s", DeviceId="%s", Version="%s", Token="%s"`,
		appmeta.ClientName(), appmeta.DeviceName(), deviceID, appmeta.Version(), token)
}

// jellyfinDialect speaks Jellyfin's `X-Emby-Authorization: MediaBrowser ...`
// scheme and discovers the user id from /Users/Me only.
type jellyfinDialect struct{}

func (jellyfinDialect) name() string     { return "jellyfin" }
func (jellyfinDialect) pingPath() string { return "/Users/Me" }
func (jellyfinDialect) metaKey() string  { return provider.MetaJellyfinID }

func (jellyfinDialect) applyAuth(req *http.Request, token, _, deviceID string) {
	if token != "" {
		req.Header.Set("X-Emby-Token", token)
	}
	req.Header.Set("X-Emby-Authorization",
		fmt.Sprintf(`MediaBrowser Client="%s", Device="%s", DeviceId="%s", Version="%s"`,
			appmeta.ClientName(), appmeta.DeviceName(), deviceID, appmeta.Version()))
}

func (jellyfinDialect) discoverUserID(c *Client) (string, error) {
	var u userDTO
	if err := c.get("/Users/Me", nil, &u); err != nil {
		return "", err
	}
	if u.ID == "" {
		return "", fmt.Errorf("jellyfin: current user response missing id")
	}
	c.setUserID(u.ID)
	return u.ID, nil
}
