package qobuz

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestSaveCredsWritesPrivateFile(t *testing.T) {
	tests := []struct {
		name     string
		existing os.FileMode // 0 means no file exists before the save
	}{
		{name: "new file"},
		{name: "replace private file", existing: 0o600},
		{name: "replace readable file", existing: 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			path, err := CredsPath()
			if err != nil {
				t.Fatal(err)
			}
			if tt.existing != 0 {
				if err := os.WriteFile(path, []byte(`{"app_id":"old"}`), tt.existing); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, tt.existing); err != nil {
					t.Fatal(err)
				}
			}

			want := storedCreds{AppID: "app", Secrets: []string{"s1"}, UserAuthToken: "token", UserID: "7"}
			if err := credsFile.Save(&want); err != nil {
				t.Fatalf("credsFile.Save() error = %v", err)
			}
			got, err := credsFile.Load()
			if err != nil {
				t.Fatalf("credsFile.Load() error = %v", err)
			}
			if got.AppID != want.AppID || got.UserAuthToken != want.UserAuthToken || got.UserID != want.UserID {
				t.Errorf("credsFile.Load() = %+v, want %+v", got, want)
			}
			if runtime.GOOS == "windows" {
				return // Windows does not report Unix permission bits.
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Errorf("credentials mode = %o, want 600", perm)
			}
		})
	}
}

// TestLoadStoredCredsFile checks that credentials an earlier release wrote
// still load, so an upgrade keeps the user signed in.
func TestLoadStoredCredsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	data := `{"app_id":"app","secrets":["s1","s2"],"secret":"s2","private_key":"key",` +
		`"user_auth_token":"token","user_id":"7","label":"Studio"}`
	if err := os.WriteFile(filepath.Join(dir, "qobuz_credentials.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := credsFile.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := storedCreds{AppID: "app", Secrets: []string{"s1", "s2"}, Secret: "s2", PrivateKey: "key", UserAuthToken: "token", UserID: "7", Label: "Studio"}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("Load() = %+v, want %+v", *got, want)
	}
}
