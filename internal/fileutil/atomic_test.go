package fileutil

import (
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

func TestWriteFileAtomicRejectsDanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated privileges on Windows")
	}

	link := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.Symlink("missing.toml", link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(link, []byte("new"), 0o600); err == nil {
		t.Fatal("WriteFileAtomic succeeded for a dangling symlink")
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("dangling destination symlink was replaced")
	}
}
