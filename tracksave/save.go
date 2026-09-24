// Package tracksave persists downloaded tracks in the user's music directory.
package tracksave

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

// Save downloads or copies track into ~/Music/cliamp and returns its path.
func Save(track playlist.Track) (string, error) {
	return SaveTo(track, "")
}

// Directory resolves the configured directory, falling back to ~/Music/cliamp.
func Directory(directory string) (string, error) {
	if directory != "" {
		if !filepath.IsAbs(directory) {
			return "", fmt.Errorf("download directory must be an absolute path")
		}
		resolved, err := filepath.Abs(directory)
		if err != nil {
			return "", fmt.Errorf("resolve download directory: %w", err)
		}
		return resolved, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Music", "cliamp"), nil
}

// SaveTo downloads or copies a track to the configured directory.
func SaveTo(track playlist.Track, directory string) (string, error) {
	saveDir, err := Directory(directory)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return "", err
	}
	if playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path) {
		return resolve.DownloadYTDL(track.Path, saveDir)
	}
	if track.Stream || !insideTempDir(track.Path) {
		return "", fmt.Errorf("only downloaded tracks can be saved")
	}
	name := track.Title
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(track.Path), filepath.Ext(track.Path))
	}
	if track.Artist != "" {
		name = track.Artist + " - " + name
	}
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	destination := filepath.Join(saveDir, name+filepath.Ext(track.Path))
	if err := fileutil.CopyFile(track.Path, destination); err != nil {
		return "", err
	}
	return destination, nil
}

func insideTempDir(path string) bool {
	relative, err := filepath.Rel(os.TempDir(), path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
