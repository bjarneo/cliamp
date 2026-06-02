// Package jellyfin adapts the shared Emby/Jellyfin client (internal/embyapi)
// to a Jellyfin server and exposes it as a playlist provider.
package jellyfin

import "cliamp/internal/embyapi"

// Client and Track alias the shared embyapi types so the provider layer reads
// naturally and external callers keep using jellyfin.Client.
type (
	Client = embyapi.Client
	Track  = embyapi.Track
)

// NewClient returns a Client for the given Jellyfin server URL and credentials.
func NewClient(baseURL, token, userID, user, password string) *Client {
	return embyapi.NewJellyfinClient(baseURL, token, userID, user, password)
}

// IsStreamURL reports whether the URL is a Jellyfin item download endpoint.
// Used by the player to route these URLs through the buffered ffmpeg pipeline.
func IsStreamURL(path string) bool { return embyapi.IsStreamURL(path) }
