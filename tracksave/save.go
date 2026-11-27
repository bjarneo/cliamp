// Package tracksave persists downloaded tracks in the user's music directory.
package tracksave

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

const maxSaveCollisionAttempts = 1000

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
	saveDir = filepath.Clean(saveDir)
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return "", err
	}
	if NeedsDownload(track) {
		return resolve.DownloadYTDLContext(ctx, track.Path, saveDir)
	}
	resolvedSource, srcInfo, ok := insideTempDir(track.Path)
	if track.Stream || !ok {
		return "", fmt.Errorf("only downloaded tracks can be saved")
	}

	title := cleanNamePart(track.Title)
	if title == "" {
		base := filepath.Base(resolvedSource)
		title = cleanNamePart(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	artist := cleanNamePart(track.Artist)
	name := title
	if artist != "" {
		if name != "" {
			name = artist + " - " + name
		} else {
			name = artist
		}
	}
	if name == "" || name == "." || name == ".." {
		name = "track"
	}
	ext := cleanExt(filepath.Ext(resolvedSource))

	for attempt := 0; attempt <= maxSaveCollisionAttempts; attempt++ {
		candidateName := name + ext
		if attempt > 0 {
			candidateName = fmt.Sprintf("%s (%d)%s", name, attempt, ext)
		}
		destination := filepath.Join(saveDir, candidateName)
		if filepath.Dir(destination) != saveDir {
			return "", fmt.Errorf("invalid destination filename")
		}
		err := copyFileExclusive(resolvedSource, destination)
		if err == nil {
			return destination, nil
		}
		if errors.Is(err, os.ErrExist) {
			if dstInfo, statErr := os.Stat(destination); statErr == nil && os.SameFile(srcInfo, dstInfo) {
				return "", fmt.Errorf("source and destination are the same file")
			}
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("destination file already exists")
}

// NeedsDownload reports whether SaveTo downloads track with yt-dlp, which can
// take minutes. SaveTo copies every other track that it can save.
func NeedsDownload(track playlist.Track) bool {
	return playlist.IsYouTubeURL(track.Path) || playlist.IsYTDL(track.Path)
}

func cleanNamePart(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return -1
		}
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func cleanExt(ext string) string {
	ext = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return -1
		}
		return r
	}, ext)
	if ext == "." {
		return ""
	}
	return ext
}

// copyFileExclusive copies src into a new file at dst without overwriting an
// existing file or following a symlink.
func copyFileExclusive(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	return nil
}

func insideTempDir(path string) (string, os.FileInfo, bool) {
	tempDir, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", nil, false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, false
	}
	relative, err := filepath.Rel(tempDir, resolved)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return "", nil, false
	}
	return resolved, info, true
}
