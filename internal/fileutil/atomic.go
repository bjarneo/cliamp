package fileutil

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path only after data has been written and synced.
// Preserves destination symlinks, creating missing target files.
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

	// Harden only the original directory, not an external symlink target's parent.
	if secureDir {
		if err := os.MkdirAll(originalDir, 0o700); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
		if err := os.Chmod(originalDir, 0o700); err != nil {
			return fmt.Errorf("secure directory: %w", err)
		}
	}
	path, err = resolveAtomicDestination(path)
	if err != nil {
		return err
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

func resolveAtomicDestination(path string) (string, error) {
	for links := 0; ; links++ {
		dir, name := filepath.Split(path)
		if dir == "" {
			dir = "."
		}
		dir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return "", fmt.Errorf("resolve destination directory: %w", err)
		}
		path = filepath.Join(dir, name)

		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return path, nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect destination: %w", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		if links >= 255 {
			return "", fmt.Errorf("resolve destination: too many symbolic links")
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("read destination symlink: %w", err)
		}
		path = target
		if !filepath.IsAbs(target) {
			// Resolve directory symlinks before cleaning any ".." components.
			path = dir + string(os.PathSeparator) + target
		}
	}
}
