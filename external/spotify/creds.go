package spotify

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
)

// DefaultClientID is the librespot keymaster client_id, shared by spotify-player
// and other librespot-based players. Used when the user hasn't configured their
// own client_id — Spotify's loopback exception lets it work with any 127.0.0.1
// port, and it predates the Nov 27, 2024 dev-mode quota restriction so /v1/search
// and other catalog endpoints stay accessible.
const DefaultClientID = "65b708073fc0480ea92a077233ca87bd"

// storedCreds holds persisted Spotify credentials for re-authentication.
type storedCreds struct {
	Username     string `json:"username"`
	Data         []byte `json:"data"`
	DeviceID     string `json:"device_id"`
	RefreshToken string `json:"refresh_token,omitempty"` // OAuth2 refresh token for silent re-auth
}

// storedCredsFile is the on-disk envelope for spotify_credentials.json.
// dpapi carries base64 of protectCreds() output — CryptProtectData ciphertext
// on Windows, plaintext storedCreds JSON elsewhere (the envelope stays uniform
// so version detection is identical on every OS).
//
// Threat model (Windows): DPAPI user scope protects the file against other
// users on the machine and against offline disk theft. It cannot protect
// against malware running as the same user — DPAPI silently decrypts for it.
type storedCredsFile struct {
	Version int    `json:"version"`
	DPAPI   string `json:"dpapi"`
}

// CredsPath returns the absolute path to the stored Spotify credentials file.
func CredsPath() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spotify_credentials.json"), nil
}

func loadCreds() (*storedCreds, error) {
	path, err := CredsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var file storedCredsFile
	if err := json.Unmarshal(data, &file); err == nil && file.DPAPI != "" {
		blob, err := base64.StdEncoding.DecodeString(file.DPAPI)
		if err != nil {
			return nil, err
		}
		plaintext, err := unprotectCreds(blob)
		if err != nil {
			return nil, err
		}
		var creds storedCreds
		if err := json.Unmarshal(plaintext, &creds); err != nil {
			return nil, err
		}
		return &creds, nil
	}

	// Legacy plaintext format: parse directly, then immediately re-save in
	// the envelope — migration is one atomic rewrite at the same path.
	var creds storedCreds
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	if err := saveCreds(&creds); err != nil {
		applog.Warn("spotify: failed to migrate credentials to protected format: %v", err)
	}
	return &creds, nil
}

func saveCreds(creds *storedCreds) error {
	path, err := CredsPath()
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	blob, err := protectCreds(plaintext)
	if err != nil {
		return err
	}
	data, err := json.Marshal(storedCredsFile{
		Version: 1,
		DPAPI:   base64.StdEncoding.EncodeToString(blob),
	})
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600)
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
