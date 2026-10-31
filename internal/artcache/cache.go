// Package artcache resolves remote cover images into the bounded cliamp album
// art cache. The returned source is always safe to hand to media controls and
// terminal renderers: authenticated callers store bytes, never credentialed
// URLs.
package artcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/httpclient"
)

const (
	cacheDirName  = "album-art"
	maxBodyBytes  = 32 << 20
	maxCacheBytes = 100 << 20
)

var downloadClient = httpclient.NewAPI(20 * time.Second)

// Resolve returns local and data sources unchanged and downloads an HTTP(S)
// image into the content-addressed cache.
func Resolve(ctx context.Context, source string) (string, error) {
	source = strings.TrimSpace(source)
	switch {
	case source == "":
		return "", fmt.Errorf("artwork: empty source")
	case strings.HasPrefix(source, "file://"), strings.HasPrefix(source, "data:"):
		return source, nil
	}
	u, err := url.Parse(source)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("artwork: unsupported source")
	}
	if u.User != nil {
		return "", fmt.Errorf("artwork: URL credentials are not allowed")
	}
	if cached, ok := Lookup(source); ok {
		return cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", fmt.Errorf("artwork: request: %w", err)
	}
	req.Header.Set("Accept", "image/*")
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("artwork: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("artwork: download: HTTP %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return "", fmt.Errorf("artwork: read: %w", err)
	}
	if len(raw) > maxBodyBytes {
		return "", fmt.Errorf("artwork: image exceeds %d bytes", maxBodyBytes)
	}
	return StoreForKey(source, raw)
}

// Store writes image bytes to the content-addressed cache and returns a file
// URL. Only formats the terminal renderer can decode are accepted.
func Store(raw []byte) (string, error) {
	sum := sha256.Sum256(raw)
	return store(hex.EncodeToString(sum[:]), raw)
}

// StoreForKey writes image bytes under a stable, non-sensitive cache key.
// It is used for authenticated artwork whose source URL must not be exposed.
func StoreForKey(key string, raw []byte) (string, error) {
	if key == "" {
		return "", fmt.Errorf("artwork: empty cache key")
	}
	sum := sha256.Sum256([]byte(key))
	return store("source-"+hex.EncodeToString(sum[:]), raw)
}

func store(name string, raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("artwork: empty image")
	}
	if len(raw) > maxBodyBytes {
		return "", fmt.Errorf("artwork: image exceeds %d bytes", maxBodyBytes)
	}
	ext, err := imageExtension(raw)
	if err != nil {
		return "", err
	}
	dataDir, err := appdir.DataDir()
	if err != nil {
		return "", fmt.Errorf("artwork: data directory: %w", err)
	}
	dir := filepath.Join(dataDir, cacheDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("artwork: create cache: %w", err)
	}
	path := filepath.Join(dir, name+"."+ext)
	if _, err := os.Stat(path); err == nil {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		cleanup(dir, path)
		return FileURL(path), nil
	}
	tmp, err := os.CreateTemp(dir, ".artwork-*")
	if err != nil {
		return "", fmt.Errorf("artwork: create cache file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", fmt.Errorf("artwork: chmod cache file: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return "", fmt.Errorf("artwork: write cache file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("artwork: close cache file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if _, statErr := os.Stat(path); statErr != nil {
			return "", fmt.Errorf("artwork: commit cache file: %w", err)
		}
	}
	cleanup(dir, path)
	return FileURL(path), nil
}

// Lookup returns artwork previously stored for key. It checks only the image
// extensions StoreForKey can create and touches a hit for oldest-first cleanup.
func Lookup(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	dataDir, err := appdir.DataDir()
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(key))
	base := filepath.Join(dataDir, cacheDirName, "source-"+hex.EncodeToString(sum[:]))
	for _, ext := range []string{"jpg", "png", "gif"} {
		path := base + "." + ext
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			now := time.Now()
			_ = os.Chtimes(path, now, now)
			cleanup(filepath.Dir(path), path)
			return FileURL(path), true
		}
	}
	return "", false
}

func imageExtension(raw []byte) (string, error) {
	switch http.DetectContentType(raw) {
	case "image/jpeg":
		return "jpg", nil
	case "image/png":
		return "png", nil
	case "image/gif":
		return "gif", nil
	default:
		return "", fmt.Errorf("artwork: unsupported image format")
	}
}

// FileURL converts path to a file URL suitable for playlist metadata and
// media-control APIs.
func FileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

type cacheFile struct {
	path     string
	size     int64
	modified time.Time
}

func cleanup(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var files []cacheFile
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".artwork-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		files = append(files, cacheFile{path: path, size: info.Size(), modified: info.ModTime()})
		total += info.Size()
	}
	if total <= maxCacheBytes {
		return
	}
	slices.SortFunc(files, func(a, b cacheFile) int { return a.modified.Compare(b.modified) })
	for _, file := range files {
		if total <= maxCacheBytes {
			break
		}
		if file.path == keep {
			continue
		}
		if err := os.Remove(file.path); err == nil {
			total -= file.size
		}
	}
}
