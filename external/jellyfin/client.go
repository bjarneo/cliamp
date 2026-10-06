// Package jellyfin adapts the shared Emby/Jellyfin client (internal/embyapi)
// to a Jellyfin server and exposes it as a playlist provider.
package jellyfin

import "github.com/bjarneo/cliamp/internal/embyapi"

// Client aliases the shared embyapi type so external callers keep using
// jellyfin.Client.
type Client = embyapi.Client

// NewClient returns a Client for the given Jellyfin server URL and credentials.
func NewClient(baseURL, token, userID, user, password string) *Client {
	return embyapi.NewJellyfinClient(baseURL, token, userID, user, password)
}

// IsStreamURL reports whether the URL is a Jellyfin item download endpoint.
// Used by the player to route these URLs through the buffered ffmpeg pipeline.
func IsStreamURL(path string) bool { return embyapi.IsStreamURL(path) }
