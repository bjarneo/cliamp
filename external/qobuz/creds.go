package qobuz

import "github.com/bjarneo/cliamp/internal/credstore"

// storedCreds holds persisted Qobuz credentials so the user only signs in once.
// The app_id, secrets and private key are scraped from the Qobuz web player and
// cached here alongside the OAuth user token.
type storedCreds struct {
	AppID         string   `json:"app_id"`
	Secrets       []string `json:"secrets"`
	Secret        string   `json:"secret"` // validated signing secret
	PrivateKey    string   `json:"private_key"`
	UserAuthToken string   `json:"user_auth_token"`
	UserID        string   `json:"user_id"`
	Label         string   `json:"label"`
}

// credsFile holds the stored Qobuz credentials.
var credsFile = credstore.File[storedCreds]{Name: "qobuz_credentials.json"}

// CredsPath returns the absolute path to the stored Qobuz credentials file.
func CredsPath() (string, error) { return credsFile.Path() }

// DeleteCreds removes the stored Qobuz credentials file. Returns true if a file
// was removed, false if it did not exist.
func DeleteCreds() (bool, error) { return credsFile.Delete() }
