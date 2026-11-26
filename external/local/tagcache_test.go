package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestTagCacheSkipsUnchangedAndInvalidatesOnChange(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(t *testing.T, path string, origMod time.Time)
		wantCount int
	}{
		{
			name:      "unchanged file hits cache",
			mutate:    func(*testing.T, string, time.Time) {},
			wantCount: 1,
		},
		{
			name: "mtime change invalidates cache",
			mutate: func(t *testing.T, path string, origMod time.Time) {
				t.Helper()
				nextMod := origMod.Add(2 * time.Second)
				if err := os.Chtimes(path, nextMod, nextMod); err != nil {
					t.Fatal(err)
				}
			},
			wantCount: 2,
		},
		{
			name: "size change with preserved mtime invalidates cache",
			mutate: func(t *testing.T, path string, origMod time.Time) {
				t.Helper()
				if err := os.WriteFile(path, []byte("extra bytes"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, origMod, origMod); err != nil {
					t.Fatal(err)
				}
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t)
			musicDir := t.TempDir()
			fileA := filepath.Join(musicDir, "song-alpha.mp3")
			fileB := filepath.Join(musicDir, "song-beta.mp3")
			writeAudioFile(t, fileA)
			writeAudioFile(t, fileB)
			if err := p.CreateDirPlaylist("mix", []string{musicDir}); err != nil {
				t.Fatal(err)
			}

			var mu sync.Mutex
			counts := make(map[string]int)
			origReadTags := readTags
			readTags = func(files []string) []playlist.Track {
				mu.Lock()
				for _, f := range files {
					counts[f]++
				}
				mu.Unlock()
				return origReadTags(files)
			}
			t.Cleanup(func() { readTags = origReadTags })

			infoA, err := os.Stat(fileA)
			if err != nil {
				t.Fatal(err)
			}

			// First Tracks call populates the cache for both files.
			tracks, err := p.Tracks("mix")
			if err != nil || len(tracks) != 2 {
				t.Fatalf("first Tracks() = %d, %v; want 2, nil", len(tracks), err)
			}

			// SearchTracks reuses cached tags without parsing either file again.
			found, err := p.SearchTracks(context.Background(), "song", 0)
			if err != nil || len(found) != 2 {
				t.Fatalf("SearchTracks() = %d, %v; want 2, nil", len(found), err)
			}
			if counts[fileA] != 1 || counts[fileB] != 1 {
				t.Fatalf("parse counts after warm SearchTracks = (%d, %d), want (1, 1)", counts[fileA], counts[fileB])
			}

			tt.mutate(t, fileA, infoA.ModTime())

			// Second Tracks call must re-parse only fileA when it changed.
			tracks, err = p.Tracks("mix")
			if err != nil || len(tracks) != 2 {
				t.Fatalf("second Tracks() = %d, %v; want 2, nil", len(tracks), err)
			}
			if counts[fileA] != tt.wantCount {
				t.Errorf("fileA parse count = %d, want %d", counts[fileA], tt.wantCount)
			}
			if counts[fileB] != 1 {
				t.Errorf("fileB parse count = %d, want 1", counts[fileB])
			}
		})
	}
}

func TestTagCacheBoundAndClonesProviderMeta(t *testing.T) {
	dir := t.TempDir()
	files := make([]string, 6)
	for i := range files {
		files[i] = filepath.Join(dir, fmt.Sprintf("track-%d.mp3", i))
		writeAudioFile(t, files[i])
	}

	cache := &tagCache{maxEntries: 3}
	loader := func(paths []string) []playlist.Track {
		out := make([]playlist.Track, len(paths))
		for i, p := range paths {
			out[i] = playlist.Track{
				Path:         p,
				Title:        filepath.Base(p),
				ProviderMeta: map[string]string{"k": "v"},
			}
		}
		return out
	}

	got := cache.read(files, loader)
	if len(got) != len(files) {
		t.Fatalf("read() len = %d, want %d", len(got), len(files))
	}
	cache.mu.RLock()
	cachedLen := len(cache.entries)
	cache.mu.RUnlock()
	if cachedLen > 3 {
		t.Fatalf("cache size = %d, want <= 3", cachedLen)
	}

	// Changes to a returned ProviderMeta map must not corrupt cached entries.
	first := cache.read(files[5:6], loader)
	first[0].ProviderMeta["k"] = "mutated"
	second := cache.read(files[5:6], func([]string) []playlist.Track {
		t.Fatal("unexpected cache miss on unchanged file")
		return nil
	})
	if second[0].ProviderMeta["k"] != "v" {
		t.Fatalf("cached ProviderMeta was mutated to %q, want %q", second[0].ProviderMeta["k"], "v")
	}
}

func TestTagCacheConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	files := make([]string, 4)
	for i := range files {
		files[i] = filepath.Join(dir, fmt.Sprintf("track-%d.mp3", i))
		writeAudioFile(t, files[i])
	}

	var cache tagCache
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				tracks := cache.read(files, readTags)
				if len(tracks) != len(files) {
					t.Errorf("read() len = %d, want %d", len(tracks), len(files))
				}
			}
		}()
	}
	wg.Wait()
}
