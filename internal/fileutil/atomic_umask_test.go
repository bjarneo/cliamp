//go:build unix

package fileutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestWriteFileAtomicInExistingDirUmask(t *testing.T) {
	const envKey = "CLIAMP_TEST_ATOMIC_UMASK"
	maskText, child := os.LookupEnv(envKey)
	if !child {
		// Umask is process-wide, so each mask gets its own test subprocess.
		for _, mask := range []string{"000", "022", "027", "077"} {
			t.Run(mask, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestWriteFileAtomicInExistingDirUmask$", "-test.v")
				cmd.Env = append(os.Environ(), envKey+"="+mask)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("umask %s: %v\n%s", mask, err, output)
				}
			})
		}
		return
	}

	mask, err := strconv.ParseUint(maskText, 8, 32)
	if err != nil {
		t.Fatal(err)
	}
	previous := syscall.Umask(int(mask))
	defer syscall.Umask(previous)

	for _, tc := range []struct {
		name string
		want os.FileMode
	}{
		{name: "new", want: 0o644 &^ os.FileMode(mask)},
		{name: "existing", want: 0o640},
		{name: "symlink", want: 0o640},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "playlist.toml")
			path := target
			if tc.name != "new" {
				if err := os.WriteFile(target, []byte("old"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(target, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "symlink" {
				path = filepath.Join(dir, "link.toml")
				if err := os.Symlink("playlist.toml", path); err != nil {
					t.Fatal(err)
				}
			}
			if err := WriteFileAtomicInExistingDir(path, []byte("new"), 0o644); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tc.want {
				t.Errorf("mode = %o, want %o", got, tc.want)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "new" {
				t.Errorf("contents = %q, want new", data)
			}
		})
	}
}
