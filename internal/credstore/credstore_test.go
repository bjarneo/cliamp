package credstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type testCreds struct {
	Token string `json:"token"`
}

var testFile = File[testCreds]{Name: "test_credentials.json"}

// useTempDir points the cliamp config directory at a new temporary directory
// and returns the path of testFile in it.
func useTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	return filepath.Join(dir, testFile.Name)
}

func TestPath(t *testing.T) {
	want := useTempDir(t)
	got, err := testFile.Path()
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name         string
		content      string // "" means the file does not exist
		want         string
		wantErr      bool
		wantNotExist bool
	}{
		{name: "missing file", wantErr: true, wantNotExist: true},
		{name: "corrupt JSON", content: `{"token":`, wantErr: true},
		{name: "valid file", content: `{"token":"abc"}`, want: "abc"},
		{name: "unknown fields", content: `{"token":"abc","other":1}`, want: "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := useTempDir(t)
			if tt.content != "" {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			got, err := testFile.Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() = %+v, want an error", got)
				}
				if notExist := errors.Is(err, fs.ErrNotExist); notExist != tt.wantNotExist {
					t.Errorf("errors.Is(%v, fs.ErrNotExist) = %v, want %v", err, notExist, tt.wantNotExist)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.Token != tt.want {
				t.Errorf("Load().Token = %q, want %q", got.Token, tt.want)
			}
		})
	}
}

func TestSave(t *testing.T) {
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
			path := useTempDir(t)
			if tt.existing != 0 {
				if err := os.WriteFile(path, []byte(`{"token":"old","other":1}`), tt.existing); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, tt.existing); err != nil {
					t.Fatal(err)
				}
			}

			if err := testFile.Save(&testCreds{Token: "new"}); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(data), `{"token":"new"}`; got != want {
				t.Errorf("file content = %s, want %s", got, want)
			}
			if runtime.GOOS == "windows" {
				return // Windows does not report Unix permission bits.
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Errorf("file mode = %o, want 600", perm)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name        string
		exists      bool
		wantRemoved bool
	}{
		{name: "missing file"},
		{name: "existing file", exists: true, wantRemoved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := useTempDir(t)
			if tt.exists {
				if err := os.WriteFile(path, []byte(`{"token":"abc"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			removed, err := testFile.Delete()
			if err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if removed != tt.wantRemoved {
				t.Errorf("Delete() removed = %v, want %v", removed, tt.wantRemoved)
			}
			if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("file still exists after Delete(): stat error = %v", err)
			}
		})
	}
}
