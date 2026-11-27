package ytmusic

import (
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestYTCacheRejectsDifferentOAuthAccount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := credsFile.Save(&storedCreds{RefreshToken: "account-a"}); err != nil {
		t.Fatal(err)
	}
	scopeA := storedOAuthCacheScope("client")
	cache := newYTCache(scopeA)
	cache.setPlaylists([]playlistEntry{{ID: "private", Name: "Private"}})
	cache.setTracks("private", []playlist.Track{{Title: "Secret"}})
	saveSnapshot(cache.snapshot())

	matching := loadYTCache(scopeA)
	if !matching.playlistsFresh() {
		t.Fatal("matching cache scope was not loaded")
	}

	if err := credsFile.Save(&storedCreds{RefreshToken: "account-b"}); err != nil {
		t.Fatal(err)
	}
	scopeB := storedOAuthCacheScope("client")
	loaded := loadYTCache(scopeB)
	if loaded.playlistsFresh() || len(loaded.Tracks) != 0 {
		t.Fatal("OAuth cache reused entries after the stored account changed")
	}
	if scopeA == scopeB {
		t.Fatal("OAuth cache scope did not change with the refresh token")
	}
}

func TestYTCacheRejectsLegacyUnscopedData(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	legacy := newYTCache("")
	legacy.setPlaylists([]playlistEntry{{ID: "old", Name: "Old"}})
	saveSnapshot(legacy.snapshot())

	loaded := loadYTCache(storedOAuthCacheScope("client"))
	if loaded.playlistsFresh() {
		t.Fatal("scoped cache reused legacy unscoped playlists")
	}
}

func TestOAuthCacheScopeBoundToProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := credsFile.Save(&storedCreds{RefreshToken: "account-a"}); err != nil {
		t.Fatal(err)
	}
	b := newBase(nil, "client", "secret", false)
	scopeA := b.cacheScope

	if err := credsFile.Save(&storedCreds{RefreshToken: "account-b"}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	loadedScope := b.ensureDiskCache().Scope
	b.mu.Unlock()

	if loadedScope != scopeA {
		t.Fatalf("cache scope changed from %q to %q after credentials changed", scopeA, loadedScope)
	}
	if loadedScope == storedOAuthCacheScope("client") {
		t.Fatal("provider rebound its cache to credentials written by another session")
	}
}

func TestSaveSnapshotAndClassificationConcurrentAtomic(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	cache := newYTCache("scope-1")
	cache.setPlaylists([]playlistEntry{{ID: "p1", Name: "Mix"}})
	snap := cache.snapshot()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			saveSnapshot(snap)
		}()
		go func() {
			defer wg.Done()
			saveClassification("scope-1", map[string]bool{"p1": true})
		}()
	}
	wg.Wait()

	if loaded := loadYTCache("scope-1"); !loaded.playlistsFresh() {
		t.Fatal("snapshot not readable after concurrent writes")
	}
	if got := loadClassification("scope-1"); !got["p1"] {
		t.Fatalf("classification = %v, want p1=true", got)
	}
}
