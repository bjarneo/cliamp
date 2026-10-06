package spotify

import "github.com/bjarneo/cliamp/internal/credstore"

// DefaultClientID is the librespot keymaster client_id, shared by spotify-player
// and other librespot-based players. Used when the user hasn't configured their
// own client_id — Spotify's loopback exception lets it work with any 127.0.0.1
// port, and it predates the Nov 27, 2024 dev-mode quota restriction so /v1/search
// and other catalog endpoints stay accessible.
const DefaultClientID = "65b708073fc0480ea92a077233ca87bd"

// credsFile holds the stored Spotify credentials.
var credsFile = credstore.File[storedCreds]{Name: "spotify_credentials.json"}

// CredsPath returns the absolute path to the stored Spotify credentials file.
func CredsPath() (string, error) { return credsFile.Path() }

// DeleteCreds removes the stored Spotify credentials file.
// Returns true if a file was removed, false if it did not exist.
func DeleteCreds() (bool, error) { return credsFile.Delete() }
