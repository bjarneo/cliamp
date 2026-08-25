package tidal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
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

// CredsPath returns the absolute path to the stored Tidal credentials file.
func CredsPath() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", fmt.Errorf("tidal: config dir: %w", err)
	}
	return filepath.Join(dir, "tidal_credentials.json"), nil
}

// DeleteCreds removes the stored Tidal credentials file. Returns true if a
// file was removed, false if it did not exist.
func DeleteCreds() (bool, error) {
	path, err := CredsPath()
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("tidal: remove credentials: %w", err)
	}
	return true, nil
}

func loadCreds() (*storedCreds, error) {
	path, err := CredsPath()
	if err != nil {
		return nil, fmt.Errorf("tidal: credentials path: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tidal: read credentials: %w", err)
	}
	var creds storedCreds
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("tidal: parse credentials: %w", err)
	}
	return &creds, nil
}

func saveCreds(creds *storedCreds) error {
	path, err := CredsPath()
	if err != nil {
		return fmt.Errorf("tidal: credentials path: %w", err)
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("tidal: encode credentials: %w", err)
	}
	// Atomic write: Tidal rotates tokens, so this file is rewritten during
	// normal use — a torn write would force a fresh device-flow sign-in.
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("tidal: write credentials: %w", err)
	}
	return nil
}
