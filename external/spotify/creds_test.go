package spotify

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func writeCredsFile(t *testing.T, contents string) string {
	t.Helper()
	path, err := CredsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSaveLoadCredsWritesEnvelope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	stored := storedCreds{
		Username:     "fake-user",
		Data:         []byte("fake-credential-blob"),
		DeviceID:     "fake-device",
		RefreshToken: "fake-refresh-token",
	}
	if err := saveCreds(&stored); err != nil {
		t.Fatal(err)
	}

	path, err := CredsPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file storedCredsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("on-disk file is not a JSON envelope: %v", err)
	}
	if file.Version != 1 {
		t.Errorf("envelope version = %d, want 1", file.Version)
	}
	if file.DPAPI == "" {
		t.Fatal("envelope dpapi field is empty")
	}
	if bytes.Contains(raw, []byte("fake-refresh-token")) {
		t.Error("credentials file contains plaintext refresh token")
	}

	got, err := loadCreds()
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != stored.Username || got.DeviceID != stored.DeviceID ||
		got.RefreshToken != stored.RefreshToken || !slices.Equal(got.Data, stored.Data) {
		t.Errorf("loadCreds() = %+v, want %+v", got, stored)
	}
}

func TestLoadCredsMigratesLegacyPlaintext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Legacy fixture: pre-envelope plaintext storedCreds JSON, fake tokens only.
	legacy := `{"username":"legacy-user","data":"ZmFrZS1jcmVkZW50aWFsLWJsb2I=","device_id":"legacy-device","refresh_token":"fake-legacy-refresh"}`
	path := writeCredsFile(t, legacy)

	got, err := loadCreds()
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "legacy-user" || got.DeviceID != "legacy-device" ||
		got.RefreshToken != "fake-legacy-refresh" || !slices.Equal(got.Data, []byte("fake-credential-blob")) {
		t.Errorf("loadCreds() = %+v, want decoded legacy creds", got)
	}

	// The same load must have re-saved the file as a protected envelope.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file storedCredsFile
	if err := json.Unmarshal(raw, &file); err != nil || file.Version != 1 || file.DPAPI == "" {
		t.Fatalf("migrated file = %q, want {version:1,dpapi:<b64>} envelope", raw)
	}
	if bytes.Contains(raw, []byte("legacy-user")) || bytes.Contains(raw, []byte("fake-legacy-refresh")) {
		t.Error("migrated file still contains plaintext credential fields")
	}

	// And it must keep loading through the envelope path.
	got2, err := loadCreds()
	if err != nil {
		t.Fatal(err)
	}
	if got2.Username != got.Username || got2.RefreshToken != got.RefreshToken {
		t.Errorf("second loadCreds() = %+v, want same creds as %+v", got2, got)
	}
}

func TestLoadCredsCorruptReturnsError(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{"not json", `this is not json`},
		{"bad base64 blob", `{"version":1,"dpapi":"!!!not-base64!!!"}`},
		{"blob is not creds json", `{"version":1,"dpapi":"` + base64.StdEncoding.EncodeToString([]byte("not-a-creds-json")) + `"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			writeCredsFile(t, tt.contents)
			if _, err := loadCreds(); err == nil {
				t.Errorf("loadCreds() on %q returned nil error, want error", tt.contents)
			}
		})
	}
}

func TestProtectUnprotectCredsRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI is Windows-only; other platforms use the identity shim")
	}

	plaintext := []byte(`{"username":"fake-user","data":"ZmFrZQ==","refresh_token":"fake-token"}`)
	blob, err := protectCreds(plaintext)
	if err != nil {
		t.Fatalf("protectCreds() = %v", err)
	}
	if bytes.Equal(blob, plaintext) {
		t.Fatal("protectCreds() returned plaintext unchanged on Windows")
	}
	got, err := unprotectCreds(blob)
	if err != nil {
		t.Fatalf("unprotectCreds() = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round trip = %q, want %q", got, plaintext)
	}
	if _, err := unprotectCreds([]byte("not a dpapi blob")); err == nil {
		t.Error("unprotectCreds(garbage) = nil error, want error")
	}
}
