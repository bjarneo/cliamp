// Package tracksave persists downloaded tracks in the user's music directory.
package tracksave

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

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

// SaveTo downloads or copies a track to the configured directory. A cancel of
// ctx stops a yt-dlp download.
func SaveTo(ctx context.Context, track playlist.Track, directory string) (string, error) {
	saveDir, err := Directory(directory)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return "", err
	}
	if NeedsDownload(track) {
		return resolve.DownloadYTDLContext(ctx, track.Path, saveDir)
	}
	if track.Stream || !insideTempDir(track.Path) {
		return "", fmt.Errorf("only downloaded tracks can be saved")
	}
	destination := filepath.Join(saveDir, fileName(track)+filepath.Ext(track.Path))
	if err := fileutil.CopyFile(track.Path, destination); err != nil {
		return "", err
	}
	return destination, nil
}

// Opener opens the file of a streamed track, such as the original file on a
// media server, and names its extension, such as ".flac".
type Opener func(ctx context.Context, track playlist.Track) (body io.ReadCloser, ext string, err error)

// SaveFrom saves the file that open returns for track in the configured
// directory, as "Artist - Title.ext". When that file exists, it reads nothing
// and reports existed. The download goes to a hidden temporary file first,
// so a failed one leaves no partial track behind.
func SaveFrom(ctx context.Context, track playlist.Track, directory string, open Opener) (path string, existed bool, err error) {
	saveDir, err := Directory(directory)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return "", false, err
	}
	body, ext, err := open(ctx, track)
	if err != nil {
		return "", false, err
	}
	defer body.Close()
	destination := filepath.Join(saveDir, fileName(track)+ext)
	if _, err := os.Stat(destination); err == nil {
		return destination, true, nil
	}
	tmp, err := os.CreateTemp(saveDir, ".cliamp-save-*"+ext)
	if err != nil {
		return "", false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, body); err != nil {
		tmp.Close()
		return "", false, fmt.Errorf("download: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmp.Name(), destination); err != nil {
		return "", false, err
	}
	return destination, false, nil
}

// fileName names a saved track "Artist - Title", with the characters that
// file systems reject replaced.
func fileName(track playlist.Track) string {
	name := track.Title
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(track.Path), filepath.Ext(track.Path))
	}
	if track.Artist != "" {
		name = track.Artist + " - " + name
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
}

// NeedsDownload reports whether SaveTo downloads track with yt-dlp, which can
// take minutes. SaveTo copies every other track that it can save.
func NeedsDownload(track playlist.Track) bool {
	return playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path)
}

func insideTempDir(path string) bool {
	relative, err := filepath.Rel(os.TempDir(), path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
