package local

import (
	"maps"
	"os"
	"sync"

	"github.com/bjarneo/cliamp/playlist"
)

// maxTagCacheEntries bounds the number of cached file tag entries per Provider.
const maxTagCacheEntries = 10_000

type tagCacheKey struct {
	path            string
	modTimeUnixNano int64
	size            int64
}

type tagCacheEntry struct {
	key   tagCacheKey
	track playlist.Track
}

// tagCache caches parsed audio metadata keyed by path, modification time, and
// file size. The zero value is ready for use.
type tagCache struct {
	mu         sync.RWMutex
	entries    map[string]tagCacheEntry
	maxEntries int
}

func (c *tagCache) limit() int {
	if c.maxEntries > 0 {
		return c.maxEntries
	}
	return maxTagCacheEntries
}

func cloneCachedTrack(t playlist.Track) playlist.Track {
	t.ProviderMeta = maps.Clone(t.ProviderMeta)
	return t
}

// read returns track metadata for files in order. It serves unchanged files
// from the cache and calls loader only for cache misses.
func (c *tagCache) read(files []string, loader func([]string) []playlist.Track) []playlist.Track {
	if len(files) == 0 {
		return nil
	}
	tracks := make([]playlist.Track, len(files))
	keys := make([]tagCacheKey, len(files))
	hasKey := make([]bool, len(files))
	for i, path := range files {
		if info, err := os.Stat(path); err == nil {
			keys[i] = tagCacheKey{
				path:            path,
				modTimeUnixNano: info.ModTime().UnixNano(),
				size:            info.Size(),
			}
			hasKey[i] = true
		}
	}

	var missIdx []int
	var missPaths []string
	c.mu.RLock()
	for i, path := range files {
		if hasKey[i] {
			if entry, ok := c.entries[path]; ok && entry.key == keys[i] {
				tracks[i] = cloneCachedTrack(entry.track)
				continue
			}
		}
		missIdx = append(missIdx, i)
		missPaths = append(missPaths, path)
	}
	c.mu.RUnlock()

	if len(missPaths) == 0 {
		return tracks
	}

	loaded := loader(missPaths)
	limit := c.limit()
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]tagCacheEntry, min(len(missPaths), limit))
	}
	for j, idx := range missIdx {
		if j >= len(loaded) {
			break
		}
		track := loaded[j]
		tracks[idx] = cloneCachedTrack(track)
		if !hasKey[idx] {
			continue
		}
		path := keys[idx].path
		if _, exists := c.entries[path]; !exists && len(c.entries) >= limit {
			clear(c.entries)
		}
		c.entries[path] = tagCacheEntry{
			key:   keys[idx],
			track: cloneCachedTrack(track),
		}
	}
	c.mu.Unlock()
	return tracks
}
