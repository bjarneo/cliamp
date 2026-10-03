package tidal

import (
	"time"

	"github.com/bjarneo/cliamp/internal/credstore"
)

// Built-in fallback OAuth client credentials: the device ("TV") client pair
// that the python-tidal ecosystem ships. Tidal revokes leaked client IDs
// periodically; when that happens, users can set client_id/client_secret in
// the [tidal] config section to a fresh pair without waiting for a cliamp
// release.
const (
	fallbackClientID     = "fX2JxdmntZWK0ixT"
	fallbackClientSecret = "1Nn9AfDAjxrgJFJbKNWLeAyKGVGmINuXPPLHVXAvxAg="
)

// storedCreds holds persisted Tidal OAuth tokens so the user only signs in
// once. The client credentials that minted the tokens are stored alongside
// them because token refresh must use the same client.
type storedCreds struct {
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserID       string    `json:"user_id"`
	CountryCode  string    `json:"country_code"`
}

// credsFile holds the stored Tidal credentials. Tidal rotates tokens, so
// cliamp rewrites this file during normal use. credstore writes it atomically,
// so a torn write cannot force a fresh device-flow sign-in.
var credsFile = credstore.File[storedCreds]{Name: "tidal_credentials.json"}

// CredsPath returns the absolute path to the stored Tidal credentials file.
func CredsPath() (string, error) { return credsFile.Path() }

// DeleteCreds removes the stored Tidal credentials file. Returns true if a
// file was removed, false if it did not exist.
func DeleteCreds() (bool, error) { return credsFile.Delete() }
