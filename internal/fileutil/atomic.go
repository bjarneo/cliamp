package fileutil

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path only after data has been written and synced.
// Preserves destination symlinks; dangling links fail.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return replaceFileAtomic(path, data, perm, true)
}

// WriteFileAtomicInExistingDir replaces path without changing its parent
// directory's permissions. The parent directory must already exist.
func WriteFileAtomicInExistingDir(path string, data []byte, perm os.FileMode) error {
	return replaceFileAtomic(path, data, perm, false)
}

func replaceFileAtomic(path string, data []byte, perm os.FileMode, secureDir bool) (err error) {
	originalDir := filepath.Dir(path)
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve destination symlink: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect destination: %w", err)
	}

	// Harden only the original directory, not an external symlink target's parent.
	if secureDir {
		if err := os.MkdirAll(originalDir, 0o700); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
		if err := os.Chmod(originalDir, 0o700); err != nil {
			return fmt.Errorf("secure directory: %w", err)
		}
	}
	dir := filepath.Dir(path)
	respectUmask := false
	if info, statErr := os.Stat(path); statErr == nil {
		perm &= info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect existing file: %w", statErr)
	} else {
		respectUmask = !secureDir
	}

	var tmp *os.File
	if respectUmask {
		// Apply umask at creation; a later Chmod would bypass it. O_EXCL
		// prevents following or overwriting anything at the random temp path.
		tmp, err = os.OpenFile(filepath.Join(dir, ".tmp-"+rand.Text()), os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
	} else {
		tmp, err = os.CreateTemp(dir, ".tmp-*")
	}
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if tmp != nil {
			if closeErr := tmp.Close(); err == nil && closeErr != nil {
				err = fmt.Errorf("close temporary file: %w", closeErr)
			}
		}
		_ = os.Remove(tmpPath)
	}()

	if !respectUmask {
		if err = tmp.Chmod(perm); err != nil {
			return fmt.Errorf("set temporary file permissions: %w", err)
		}
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	tmp = nil
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}

	if err = syncDir(dir); err != nil {
		return fmt.Errorf("sync parent directory: %w", err)
	}
	return nil
}
