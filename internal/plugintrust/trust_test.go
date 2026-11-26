package plugintrust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
)

func TestApprovalLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.lua")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(m, "example", path); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("Verify before approval = %v, want ErrUntrusted", err)
	}
	if _, err := Approve(dir, "example", path); err != nil {
		t.Fatal(err)
	}
	m, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(m, "example", path); err != nil {
		t.Fatalf("Verify approved plugin: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(m, "example", path); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("Verify changed plugin = %v, want ErrHashMismatch", err)
	}

	info, err := os.Stat(filepath.Join(dir, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permission bits; os.Stat reports 0666 for any
	// writable file, so the 0600 check only applies on Unix.
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Errorf("manifest mode = %o, want 600", got)
	}
}

func TestLoadRejectsTamperedManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte(`{"version":99,"plugins":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted unsupported manifest")
	}
}

func TestRevoke(t *testing.T) {
	tests := []struct {
		name     string
		approved []string
		revoke   string
		want     []string // approvals left
		noFile   bool     // the manifest must not exist afterwards
	}{
		{name: "approved plugin", approved: []string{"a", "b"}, revoke: "a", want: []string{"b"}},
		{name: "plugin without approval", approved: []string{"b"}, revoke: "a", want: []string{"b"}},
		{name: "no manifest", revoke: "a", noFile: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range tt.approved {
				path := filepath.Join(dir, name+".lua")
				if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := ApproveHash(dir, name, path, Hash([]byte(name)), []string{"control"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := Revoke(dir, tt.revoke); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, manifestName)); tt.noFile && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("manifest stat error = %v, want no manifest", err)
			}
			m, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(m.Plugins) != len(tt.want) || len(m.Permissions) != len(tt.want) {
				t.Fatalf("approvals = %v, permissions = %v, want %v", m.Plugins, m.Permissions, tt.want)
			}
			for _, name := range tt.want {
				if _, ok := m.Plugins[name]; !ok {
					t.Errorf("approval of %s is gone, want it kept", name)
				}
				if _, ok := m.Permissions[name]; !ok {
					t.Errorf("permissions of %s are gone, want them kept", name)
				}
			}
		})
	}
}

// ApproveHash approves only the content and the permissions that the user
// saw. A change to the file after the hash was shown leaves the manifest as
// it was.
func TestApproveHash(t *testing.T) {
	tests := []struct {
		name    string
		content string // the file content when ApproveHash runs
		wantErr error
	}{
		{"unchanged", "shown", nil},
		{"changed after the prompt", "changed", ErrHashMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "example.lua")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			shown := Hash([]byte("shown"))
			if err := ApproveHash(dir, "example", path, shown, []string{"exec"}); !errors.Is(err, tt.wantErr) {
				t.Fatalf("ApproveHash() = %v, want %v", err, tt.wantErr)
			}
			m, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			got, approved := m.Plugins["example"]
			if approved != (tt.wantErr == nil) || (approved && got != shown) {
				t.Errorf("manifest approval = %q, %v, want the shown hash only without an error", got, approved)
			}
			perms, recorded := m.Permissions["example"]
			if recorded != approved || (recorded && !slices.Equal(perms, []string{"exec"})) {
				t.Errorf("recorded permissions = %v, %v, want [exec] only with the approval", perms, recorded)
			}
		})
	}
}

// Approve records no permissions, so it drops a list that an earlier
// approval recorded. The player then reads the permissions from the content.
func TestApproveDropsRecordedPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.lua")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApproveHash(dir, "example", path, Hash([]byte("content")), []string{"exec"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Approve(dir, "example", path); err != nil {
		t.Fatal(err)
	}
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perms, ok := m.Permissions["example"]; ok {
		t.Errorf("recorded permissions = %v, want none", perms)
	}
}

func TestVerifyBytesAndHashBytes(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`plugin.register({name = "bytes", type = "hook"})`)
	path := filepath.Join(dir, "bytes.lua")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	fileHash, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := HashBytes(content); got != fileHash {
		t.Fatalf("HashBytes = %q, want HashFile %q", got, fileHash)
	}

	m := Manifest{
		Version: 1,
		Plugins: map[string]string{"bytes": HashBytes(content)},
	}
	tests := []struct {
		name    string
		plugin  string
		data    []byte
		wantErr error
	}{
		{"matching bytes", "bytes", content, nil},
		{"modified bytes", "bytes", []byte("tampered"), ErrHashMismatch},
		{"untrusted plugin", "other", content, ErrUntrusted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := VerifyBytes(m, tt.plugin, tt.data); !errors.Is(err, tt.wantErr) {
				t.Errorf("VerifyBytes() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestConcurrentApproveAndRevoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("LockFile is a no-op placeholder on Windows")
	}
	dir := t.TempDir()
	const count = 12
	paths := make([]string, count)
	for i := range count {
		name := fmt.Sprintf("plug-%d", i)
		p := filepath.Join(dir, name+".lua")
		if err := os.WriteFile(p, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		paths[i] = p
	}

	var wg sync.WaitGroup
	errs := make(chan error, count*2)
	for i := range count {
		idx := i
		name := fmt.Sprintf("plug-%d", idx)
		wg.Go(func() {
			if err := ApproveHash(dir, name, paths[idx], HashBytes([]byte(name)), []string{"control"}); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ApproveHash: %v", err)
	}

	// Revoke the even plugins concurrently while keeping the odd ones.
	var rwg sync.WaitGroup
	rerrs := make(chan error, count)
	for i := 0; i < count; i += 2 {
		name := fmt.Sprintf("plug-%d", i)
		rwg.Go(func() {
			if err := Revoke(dir, name); err != nil {
				rerrs <- err
			}
		})
	}
	rwg.Wait()
	close(rerrs)
	for err := range rerrs {
		t.Fatalf("concurrent Revoke: %v", err)
	}

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after concurrent updates: %v", err)
	}
	for i := range count {
		name := fmt.Sprintf("plug-%d", i)
		_, ok := m.Plugins[name]
		want := i%2 == 1
		if ok != want {
			t.Errorf("manifest has %s = %v, want %v", name, ok, want)
		}
	}
	if _, err := os.Stat(ManifestPath(dir) + ".lock"); err != nil {
		t.Errorf("expected lock file to exist: %v", err)
	}
}
