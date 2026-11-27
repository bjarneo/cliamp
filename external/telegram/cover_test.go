package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoverURLNegativeCacheAndCap(t *testing.T) {
	cacheDir := t.TempDir()
	p := &Provider{
		cacheDir: cacheDir,
		artCache: make(map[msgKey]string),
	}

	if got := p.coverURL("chat1", 1); got != "" {
		t.Fatalf("coverURL missing = %q, want empty", got)
	}
	key := msgKey{chat: "chat1", id: 1}
	p.artMu.Lock()
	cached, hit := p.artCache[key]
	p.artMu.Unlock()
	if !hit || cached != "" {
		t.Fatalf("negative lookup not cached: hit=%v val=%q", hit, cached)
	}

	// Write cover file and clear cache entry so coverURL loads the new file.
	dir := filepath.Join(cacheDir, safeChat("chat1"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1.png"), []byte("pngdata"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := p.coverURL("chat1", 1); got != "" {
		t.Fatalf("expected cached negative lookup before invalidation, got %q", got)
	}
	p.clearArtCacheEntry(key)
	if got := p.coverURL("chat1", 1); !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("coverURL after invalidation = %q, want png data URL", got)
	}

	// Fill cache beyond maxArtCacheEntries to verify bounding.
	for i := range maxArtCacheEntries + 10 {
		p.setArtCache(msgKey{chat: "cap", id: i}, "")
	}
	p.artMu.Lock()
	gotLen := len(p.artCache)
	p.artMu.Unlock()
	if gotLen > maxArtCacheEntries {
		t.Fatalf("artCache len = %d, want <= %d", gotLen, maxArtCacheEntries)
	}
}

func TestPruneDiskCache(t *testing.T) {
	tests := []struct {
		name     string
		maxBytes int64
		wantKeep []string
		wantGone []string
	}{
		{
			name:     "evicts oldest regular files while keeping keepPath and part files",
			maxBytes: 15,
			wantKeep: []string{"new.jpg", "keep.flac", "active.part"},
			wantGone: []string{"old.flac"},
		},
		{
			name:     "no eviction when under cap",
			maxBytes: 100,
			wantKeep: []string{"old.flac", "new.jpg", "keep.flac", "active.part"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			sub := filepath.Join(root, "chat")
			if err := os.MkdirAll(sub, 0o700); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			files := []struct {
				name string
				size int
				age  time.Duration
			}{
				{name: "old.flac", size: 10, age: 3 * time.Hour},
				{name: "keep.flac", size: 10, age: 2 * time.Hour},
				{name: "new.jpg", size: 5, age: 1 * time.Hour},
				{name: "active.part", size: 50, age: 4 * time.Hour},
			}
			for _, f := range files {
				path := filepath.Join(sub, f.name)
				if err := os.WriteFile(path, make([]byte, f.size), 0o600); err != nil {
					t.Fatal(err)
				}
				ts := now.Add(-f.age)
				if err := os.Chtimes(path, ts, ts); err != nil {
					t.Fatal(err)
				}
			}

			pruneDiskCache(root, filepath.Join(sub, "keep.flac"), tt.maxBytes)

			for _, name := range tt.wantKeep {
				if _, err := os.Stat(filepath.Join(sub, name)); err != nil {
					t.Errorf("expected %s to remain: %v", name, err)
				}
			}
			for _, name := range tt.wantGone {
				if _, err := os.Stat(filepath.Join(sub, name)); !os.IsNotExist(err) {
					t.Errorf("expected %s to be evicted, stat err = %v", name, err)
				}
			}
		})
	}
}
