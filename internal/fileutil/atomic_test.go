package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileAtomicPreservesStricterMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits (0400) have no equivalent on Windows")
	}
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("old"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o400 {
		t.Errorf("mode = %o, want 400", got)
	}
}

func TestWriteFileAtomicPreservesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated privileges on Windows")
	}

	root := t.TempDir()
	targetDir := filepath.Join(root, "managed")
	linkDir := filepath.Join(root, "config")
	for _, dir := range []string{targetDir, linkDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Ensure the test exercises a permission change even with a strict umask.
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(targetDir, "settings.toml")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "settings.toml")
	if err := os.Symlink(filepath.Join("..", "managed", "settings.toml"), link); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("destination symlink was replaced")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "new" {
		t.Errorf("target contents = %q, want %q", got, "new")
	}
	for dir, want := range map[string]os.FileMode{targetDir: 0o755, linkDir: 0o700} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("directory %q mode = %o, want %o", dir, got, want)
		}
	}
}

func TestWriteFileAtomicMissingSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated privileges on Windows")
	}

	for _, writer := range []struct {
		name  string
		write func(string, []byte, os.FileMode) error
	}{
		{"secure", WriteFileAtomic},
		{"existing-directory", WriteFileAtomicInExistingDir},
	} {
		for _, tc := range []struct {
			name          string
			missingParent bool
			readOnly      bool
			existingFile  bool
		}{
			{name: "missing-file"},
			{name: "missing-parent", missingParent: true},
			{name: "read-only-parent", readOnly: true},
			{name: "read-only-parent-existing-file", readOnly: true, existingFile: true},
		} {
			t.Run(writer.name+"/"+tc.name, func(t *testing.T) {
				if tc.readOnly && os.Geteuid() == 0 {
					t.Skip("root bypasses directory write permissions")
				}
				root := t.TempDir()
				configDir := filepath.Join(root, "config")
				managedDir := filepath.Join(root, "managed")
				for _, dir := range []string{configDir, managedDir} {
					if err := os.Mkdir(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				target := filepath.Join(managedDir, "settings.toml")
				if tc.missingParent {
					target = filepath.Join(managedDir, "missing", "settings.toml")
				}
				if tc.existingFile {
					if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				mode := os.FileMode(0o755)
				if tc.readOnly {
					mode = 0o555
				}
				if err := os.Chmod(managedDir, mode); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(managedDir, 0o755) })
				link := filepath.Join(configDir, "settings.toml")
				relative, err := filepath.Rel(configDir, target)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(relative, link); err != nil {
					t.Fatal(err)
				}

				err = writer.write(link, []byte("new"), 0o600)
				switch {
				case tc.missingParent:
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("save error = %v, want missing directory", err)
					}
					if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
						t.Fatalf("target directory was created: %v", err)
					}
				case tc.readOnly:
					if !errors.Is(err, os.ErrPermission) {
						t.Fatalf("save error = %v, want permission denied", err)
					}
				case err != nil:
					t.Fatal(err)
				}
				if got, err := os.Readlink(link); err != nil || got != relative {
					t.Fatalf("symlink = %q, %v; want %q", got, err, relative)
				}
				info, err := os.Stat(managedDir)
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode().Perm(); got != mode {
					t.Errorf("target directory mode = %o, want %o", got, mode)
				}
				data, err := os.ReadFile(target)
				if (tc.readOnly || tc.missingParent) && !tc.existingFile {
					if !os.IsNotExist(err) {
						t.Fatalf("target was created after failed save: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := "new"
				if tc.existingFile {
					want = "old"
				}
				if string(data) != want {
					t.Errorf("target contents = %q, want %q", data, want)
				}
			})
		}
	}
}

func TestResolveAtomicDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated privileges on Windows")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if err := os.MkdirAll("managed/child", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("managed/existing.toml", []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, link := range [][2]string{
		{"relative", "managed/settings.toml"},
		{"absolute", filepath.Join(root, "managed", "settings.toml")},
		{"chain", "relative"},
		{"existing", "managed/existing.toml"},
		{"alias", "managed/child"},
		{"managed/child/link", "../settings.toml"},
		{"dotdot", "alias/../settings.toml"},
		{"self", "self"},
		{"cycle-a", "cycle-b"},
		{"cycle-b", "cycle-a"},
		{"missing-parent", "managed/missing/settings.toml"},
	} {
		if err := os.Symlink(link[1], link[0]); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "plain-existing", path: "managed/existing.toml", want: "managed/existing.toml"},
		{name: "plain-missing", path: "new.toml", want: "new.toml"},
		{name: "relative", path: "relative", want: "managed/settings.toml"},
		{name: "absolute", path: "absolute", want: "managed/settings.toml"},
		{name: "chain", path: "chain", want: "managed/settings.toml"},
		{name: "existing", path: "existing", want: "managed/existing.toml"},
		{name: "symlinked-parent", path: "alias/link", want: "managed/settings.toml"},
		{name: "symlink-before-dotdot", path: "dotdot", want: "managed/settings.toml"},
		{name: "self-loop", path: "self", wantErr: true},
		{name: "cycle", path: "cycle-a", wantErr: true},
		{name: "missing-parent", path: "missing-parent", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveAtomicDestination(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolved to %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err = filepath.Abs(got)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(root, tc.want); got != want {
				t.Errorf("resolved path = %q, want %q", got, want)
			}
		})
	}
}

func TestWriteFileAtomicRepairsUnsearchableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions and a non-root user")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %o, want 700", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Errorf("contents = %q, want new", data)
	}
}
