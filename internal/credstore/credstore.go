// Package credstore reads and writes the JSON credentials files that provider
// sign-ins keep in the cliamp config directory.
package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
)

// File is one credentials file in the cliamp config directory. T is the JSON
// shape of the file.
type File[T any] struct {
	Name string // base name, for example "spotify_credentials.json"
}

// Path returns the absolute path of the file.
func (f File[T]) Path() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	return filepath.Join(dir, f.Name), nil
}

// Load reads and decodes the file. When the file does not exist, the error
// matches fs.ErrNotExist.
func (f File[T]) Load() (*T, error) {
	path, err := f.Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	v := new(T)
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return v, nil
}

// Save encodes v and replaces the file atomically with mode 0600, so a crash
// never leaves a torn file and other users cannot read the tokens.
func (f File[T]) Save(v *T) error {
	path, err := f.Path()
	if err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", f.Name, err)
	}
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Delete removes the file. It reports whether a file was removed, and it
// returns false with no error when the file does not exist.
func (f File[T]) Delete() (bool, error) {
	path, err := f.Path()
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
