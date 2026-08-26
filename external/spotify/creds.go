package spotify

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// PlaybackClientID is the librespot keymaster client_id. It is the identity
// librespot itself authenticates with, and the only one that mints the
// "streaming" grant, so it is always used for the playback flow.
//
// It is a poor Web API identity: every librespot-based player on the planet
// shares it, and Spotify applies its quota per client_id globally, so its Web
// API pool runs hot and returns 429 Too Many Requests. Use DefaultClientID for
// Web API calls instead.
const PlaybackClientID = "65b708073fc0480ea92a077233ca87bd"

// DefaultClientID is the Web API identity used when the user hasn't configured
// their own client_id. This is ncspot's client_id, registered in extended quota
// mode: it predates the Nov 27, 2024 dev-mode restrictions and is exempt from
// the February 2026 ones, so followed playlists, search and browse stay
// available and the rate limit is far higher than a Development Mode app's.
// spotify-player defaults to the same client_id.
//
// Spotify's loopback exception lets it work with any 127.0.0.1 port — ncspot
// binds a random free port on every login.
const DefaultClientID = "d420a117a32841c2b3474932e49fb54b"

// isExtendedQuotaClient reports whether clientID is one of the built-in
// identities known to carry an extended quota, and so can request a full page
// of search results instead of paging around the Development Mode cap.
func isExtendedQuotaClient(clientID string) bool {
	return clientID == DefaultClientID || clientID == PlaybackClientID
}

// CredsPath returns the absolute path to the stored Spotify credentials file.
func CredsPath() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spotify_credentials.json"), nil
}

// DeleteCreds removes the stored Spotify credentials file.
// Returns true if a file was removed, false if it did not exist.
func DeleteCreds() (bool, error) {
	path, err := CredsPath()
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
