// Package emby adapts the shared Emby/Jellyfin client (internal/embyapi) to an
// Emby server and exposes it as a playlist provider.
package emby

import "cliamp/internal/embyapi"

// Client and Track alias the shared embyapi types so the provider layer reads
// naturally and external callers keep using emby.Client.
type (
	Client = embyapi.Client
	Track  = embyapi.Track
)

// NewClient returns a Client for the given Emby server URL and credentials.
func NewClient(baseURL, token, userID, user, password string) *Client {
	return embyapi.NewEmbyClient(baseURL, token, userID, user, password)
}

// IsStreamURL reports whether the URL is an Emby item download endpoint.
// Used by the player to route these URLs through the buffered ffmpeg pipeline.
func IsStreamURL(path string) bool { return embyapi.IsStreamURL(path) }
