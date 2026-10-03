package tidal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadStoredCredsFile checks that credentials an earlier release wrote
// still load, so an upgrade keeps the user signed in.
func TestLoadStoredCredsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	data := `{"client_id":"id","client_secret":"secret","access_token":"access","refresh_token":"refresh",` +
		`"token_type":"Bearer","expires_at":"2026-01-02T03:04:05Z","user_id":"7","country_code":"NO"}`
	if err := os.WriteFile(filepath.Join(dir, "tidal_credentials.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := credsFile.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := storedCreds{
		ClientID:     "id",
		ClientSecret: "secret",
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		UserID:       "7",
		CountryCode:  "NO",
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
	got.ExpiresAt = want.ExpiresAt
	if *got != want {
		t.Errorf("Load() = %+v, want %+v", *got, want)
	}
}
