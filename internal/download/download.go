// Package download streams audio into complete, uniquely named local files.
package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Filename makes a bounded, portable filename component from untrusted metadata.
func Filename(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	// Limit bytes without splitting UTF-8; leave room for the random suffix.
	var b strings.Builder
	for _, r := range name {
		if b.Len()+len(string(r)) > 160 {
			break
		}
		b.WriteRune(r)
	}
	name = strings.Trim(b.String(), " .")
	if name == "" {
		name = "track"
	}
	// A prefix also avoids Windows reserved device names (CON, NUL, etc.).
	return "track-" + name
}

// Save never forwards credentials, buffers the full body, or exposes the URL in
// errors. A random suffix avoids overwriting existing downloads. The temporary
// file and final file share a directory so publication uses a single rename.
func Save(ctx context.Context, client *http.Client, streamURL, directory, name, ext string) (string, error) {
	switch ext {
	case ".mp3", ".aac", ".flac", ".m4a", ".ogg":
	default:
		return "", fmt.Errorf("unsupported audio format")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return "", fmt.Errorf("invalid audio request")
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("audio request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("audio HTTP status %d", resp.StatusCode)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("create download directory: %w", err)
	}
	f, err := os.CreateTemp(directory, Filename(name)+"-*"+ext+".part")
	if err != nil {
		return "", fmt.Errorf("create download: %w", err)
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("audio transfer failed")
	}
	if n == 0 || (resp.ContentLength >= 0 && n != resp.ContentLength) {
		return "", fmt.Errorf("incomplete audio transfer")
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sync download: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close download: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	destination := strings.TrimSuffix(temporary, ".part")
	// A previous completed file can share a random suffix after its .part was removed.
	if _, err := os.Lstat(destination); err == nil {
		return "", fmt.Errorf("download destination already exists")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("check download destination: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return "", fmt.Errorf("finish download: %w", err)
	}
	return filepath.Clean(destination), nil
}
