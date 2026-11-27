package telegram

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/meta"
)

const (
	maxArtCacheEntries = 256
	maxDiskCacheBytes  = 256 << 20
)

func (p *Provider) setArtCache(key msgKey, url string) {
	p.artMu.Lock()
	defer p.artMu.Unlock()
	if p.artCache == nil {
		p.artCache = make(map[msgKey]string)
	}
	if _, exists := p.artCache[key]; !exists && len(p.artCache) >= maxArtCacheEntries {
		clear(p.artCache)
	}
	p.artCache[key] = url
}

func (p *Provider) clearArtCacheEntry(key msgKey) {
	p.artMu.Lock()
	delete(p.artCache, key)
	p.artMu.Unlock()
}

// coverPath returns the cached cover of a message, or "" when none is on disk.
func (p *Provider) coverPath(chat string, msgID int) string {
	dir := filepath.Join(p.cacheDir, safeChat(chat))
	for _, extension := range []string{".jpg", ".png"} {
		path := filepath.Join(dir, cfgName(msgID, extension))
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return path
		}
	}
	return ""
}

// coverURL returns the cached cover as a data: URL, or "".
//
// MPRIS accepts any URL, but some clients (the Quickshell stack behind DMS)
// ignore file:// art and only render embedded data: URLs, which is the format
// Telegram Desktop publishes.
func (p *Provider) coverURL(chat string, msgID int) string {
	p.artMu.Lock()
	key := msgKey{chat: chat, id: msgID}
	cached, hit := p.artCache[key]
	p.artMu.Unlock()
	if hit {
		return cached
	}

	path := p.coverPath(chat, msgID)
	if path == "" {
		p.setArtCache(key, "")
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		p.setArtCache(key, "")
		return ""
	}
	mime := "image/jpeg"
	if strings.HasSuffix(path, ".png") {
		mime = "image/png"
	}
	url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	p.setArtCache(key, url)
	return url
}

// ArtworkFor implements provider.ArtworkProvider. It runs when the current
// track is published, so the cover is resolved on demand and never blocks the
// track list.
func (p *Provider) ArtworkFor(track playlist.Track) (string, bool) {
	chat, msgID, err := parseURI(track.Path)
	if err != nil {
		return "", false
	}
	url := p.coverURL(chat, msgID)
	if url == "" {
		return "", false
	}
	return url, true
}

// extractCoverFromFile writes the embedded front cover of a FLAC file next to
// it. It runs while the audio downloads, so reading the cover costs no extra
// request to Telegram.
func (p *Provider) extractCoverFromFile(chat string, msgID int, flacPath string) {
	if p.coverPath(chat, msgID) != "" {
		return
	}
	file, err := os.Open(flacPath)
	if err != nil {
		return
	}
	defer file.Close()
	stream, err := flac.Parse(file)
	if err != nil {
		return
	}
	for _, block := range stream.Blocks {
		picture, ok := block.Body.(*meta.Picture)
		if !ok || len(picture.Data) == 0 {
			continue
		}
		// 3 is "Cover (front)"; 0 is "Other" and is what some taggers write.
		if picture.Type != 3 && picture.Type != 0 {
			continue
		}
		extension := ".jpg"
		if strings.Contains(strings.ToLower(picture.MIME), "png") {
			extension = ".png"
		}
		dir := filepath.Join(p.cacheDir, safeChat(chat))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return
		}
		path := filepath.Join(dir, cfgName(msgID, extension))
		if err := os.WriteFile(path, picture.Data, 0o600); err != nil {
			return
		}
		p.clearArtCacheEntry(msgKey{chat: chat, id: msgID})
		pruneDiskCache(p.cacheDir, path, maxDiskCacheBytes)
		return
	}
}

type cacheFileEntry struct {
	path    string
	size    int64
	modTime time.Time
}

// pruneDiskCache removes the oldest cached files when root exceeds maxBytes.
func pruneDiskCache(root, keepPath string, maxBytes int64) {
	if root == "" || maxBytes <= 0 {
		return
	}
	var entries []cacheFileEntry
	var total int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".part") {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		size := info.Size()
		total += size
		entries = append(entries, cacheFileEntry{
			path:    path,
			size:    size,
			modTime: info.ModTime(),
		})
		return nil
	})
	if total <= maxBytes {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].modTime.Equal(entries[j].modTime) {
			return entries[i].modTime.Before(entries[j].modTime)
		}
		return entries[i].path < entries[j].path
	})
	for _, entry := range entries {
		if total <= maxBytes {
			break
		}
		if keepPath != "" && entry.path == keepPath {
			continue
		}
		if os.Remove(entry.path) == nil {
			total -= entry.size
		}
	}
}

func cfgName(msgID int, extension string) string {
	return fmt.Sprintf("%d%s", msgID, extension)
}
