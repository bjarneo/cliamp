package radio

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFavoritesToggle(t *testing.T) {
	f := &Favorites{path: filepath.Join(t.TempDir(), favoritesFile)}
	station := CatalogStation{Name: "Test FM", URL: "https://test.example/stream"}
	for i, wantAdded := range []bool{true, false, true, false} {
		added, err := f.Toggle(station)
		if err != nil || added != wantAdded {
			t.Fatalf("toggle %d = %v, %v; want %v", i, added, err, wantAdded)
		}
		if f.Contains(station.URL) != wantAdded || f.Revision() != uint64(i+1) {
			t.Fatal("toggle did not update membership and revision")
		}
		loaded, err := loadFavoriteStations(f.path)
		if err != nil || !slices.Equal(loaded, f.Stations()) {
			t.Fatalf("disk and snapshot differ: %+v, %v", loaded, err)
		}
	}
}

// Toggle reports which lookup failed when no config directory can be resolved,
// since the UI surfaces the raw error text.
func TestFavoritesToggleConfigDirError(t *testing.T) {
	for _, key := range []string{"CLIAMP_CONFIG_DIR", "XDG_CONFIG_HOME", "HOME", "USERPROFILE", "APPDATA"} {
		t.Setenv(key, "")
	}
	f := &Favorites{}
	_, err := f.Toggle(CatalogStation{Name: "Test FM", URL: "https://test.example/stream"})
	if err == nil || !strings.HasPrefix(err.Error(), "resolve radio favorites directory: ") {
		t.Fatalf("Toggle error = %v; want radio favorites directory context", err)
	}
	if f.path != "" || f.Revision() != 0 {
		t.Fatal("failed directory resolution changed the store")
	}
}

func TestFavoritesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "radio_favorites.toml")

	f := &Favorites{byURL: make(map[string]struct{}), path: path}

	stations := []CatalogStation{
		{Name: "Jazz FM", URL: "https://jazz.example.com/stream", Country: "UK", State: "Région \"North\"", Bitrate: 320, Codec: "mp3", Homepage: "https://jazz.example.com"},
		{Name: "Rock Radio", URL: "https://rock.example.com/stream", Country: "US", Bitrate: 192, Tags: "rock,metal"},
	}
	for _, s := range stations {
		if added, err := f.Toggle(s); err != nil || !added {
			t.Fatalf("Toggle %s = %v, %v", s.Name, added, err)
		}
	}

	// Reload
	loaded, err := loadFavoriteStations(path)
	if err != nil {
		t.Fatalf("loadFavoriteStations: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 stations, got %d", len(loaded))
	}
	if !slices.Equal(loaded, stations) {
		t.Fatalf("unexpected loaded data: %+v", loaded)
	}
}

func TestFavoritesToggleSharedWithProvider(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	favorites := LoadFavorites()
	p := New(Options{Favorites: favorites, Country: CountryDeclined})
	station := CatalogStation{Name: "Jazz FM", URL: "https://jazz.example/stream"}
	p.AppendCatalog([]CatalogStation{station})

	// The Catalog adapter and playback share one store, keyed by URL.
	if added, _, err := p.ToggleFavorite("c:0"); err != nil || !added {
		t.Fatalf("catalog toggle = %v, %v", added, err)
	}
	station.Name = "Different directory label"
	if added, err := favorites.Toggle(station); err != nil || added {
		t.Fatalf("track toggle = %v, %v; want removal by URL", added, err)
	}
	if favorites.Count() != 0 || LoadFavorites().Count() != 0 {
		t.Fatal("removal did not reach the shared store and disk")
	}
	if added, err := favorites.Toggle(station); err != nil || !added {
		t.Fatalf("track toggle = %v, %v", added, err)
	}
	lists, err := p.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, list := range lists {
		if list.ID == "f:"+station.URL {
			found = strings.Contains(list.Name, station.Name)
		}
	}
	if !found || !LoadFavorites().Contains(station.URL) {
		t.Fatal("playback favorite missing from provider list or reloaded store")
	}

	// Callers cannot mutate the live store through a returned slice.
	snapshot := favorites.Stations()
	snapshot[0].Name = "Corrupted"
	if favorites.Stations()[0].Name != station.Name {
		t.Fatal("Stations returned mutable store state")
	}
}

func TestFavoritesPersistenceFailurePreservesState(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprintf("remove=%v", remove), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), favoritesFile)
			f := &Favorites{path: path}
			station := CatalogStation{Name: "Jazz", URL: "https://jazz.example/stream"}
			if remove {
				if added, err := f.Toggle(station); err != nil || !added {
					t.Fatalf("toggle = %v, %v", added, err)
				}
			}
			// A directory cannot be read as a favorites file, even as root.
			f.path = t.TempDir()
			revision := f.Revision()
			if _, err := f.Toggle(station); err == nil {
				t.Fatal("expected persistence failure")
			}
			if f.Revision() != revision {
				t.Fatal("failed persistence advanced revision")
			}
			if f.Contains(station.URL) != remove || (f.Count() == 1) != remove {
				t.Fatal("failed persistence changed memory")
			}
		})
	}
}

func TestFavoritesConcurrentAccess(t *testing.T) {
	f := &Favorites{path: filepath.Join(t.TempDir(), favoritesFile)}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			station := CatalogStation{Name: fmt.Sprintf("Station %d", i), URL: fmt.Sprintf("https://radio.example/%d", i)}
			for range 4 {
				if _, err := f.Toggle(station); err != nil {
					t.Error(err)
				}
				f.Contains(station.URL)
				f.Stations()
				f.Count()
			}
		})
	}
	wg.Wait()
	if f.Count() != 0 {
		t.Fatalf("unbalanced toggles: %v", f.Stations())
	}
}

// Both stores start with the same empty snapshot. Each mutation must use the
// latest disk contents while preserving the intent of the local snapshot.
func TestFavoritesReloadBeforeToggle(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	a, b := LoadFavorites(), LoadFavorites()
	first := CatalogStation{Name: "First", URL: "https://radio.example/first"}
	second := CatalogStation{Name: "Second", URL: "https://radio.example/second"}
	if added, err := a.Toggle(first); err != nil || !added {
		t.Fatalf("first toggle = %v, %v", added, err)
	}
	if added, err := b.Toggle(second); err != nil || !added {
		t.Fatalf("second toggle = %v, %v", added, err)
	}
	if !slices.Equal(b.Stations(), []CatalogStation{first, second}) {
		t.Fatalf("second writer lost an earlier favorite: %+v", b.Stations())
	}
	// a has never seen second. Its add intent must not undo b's addition.
	if added, err := a.Toggle(second); err != nil || !added {
		t.Fatalf("stale store toggle = %v, %v; want favorite", added, err)
	}
	if !slices.Equal(a.Stations(), []CatalogStation{first, second}) || !a.Contains(second.URL) {
		t.Fatalf("snapshot was not rebuilt from disk: %+v", a.Stations())
	}
	if !slices.Equal(LoadFavorites().Stations(), a.Stations()) || a.Revision() != 2 {
		t.Fatal("successful mutation did not publish the latest snapshot")
	}
}

func TestFavoritesTogglePreservesSnapshotIntent(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprintf("remove=%v", remove), func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			local := LoadFavorites()
			station := CatalogStation{Name: "Station", URL: "https://radio.example/station"}
			if remove {
				if added, err := local.Toggle(station); err != nil || !added {
					t.Fatalf("initial toggle = %v, %v", added, err)
				}
			}
			remote := LoadFavorites()
			remoteStation := station
			remoteStation.Name = "Updated station name"
			if added, err := remote.Toggle(remoteStation); err != nil || added == remove {
				t.Fatalf("remote toggle = %v, %v", added, err)
			}
			unrelated := CatalogStation{Name: "Unrelated", URL: "https://radio.example/unrelated"}
			if added, err := remote.Toggle(unrelated); err != nil || !added {
				t.Fatalf("unrelated toggle = %v, %v", added, err)
			}
			before, err := os.ReadFile(local.path)
			if err != nil {
				t.Fatal(err)
			}
			rev := local.Revision()
			added, err := local.Toggle(station)
			if err != nil || added == remove || local.Contains(station.URL) == remove {
				t.Fatalf("local toggle reversed the visible intent: %v, %v", added, err)
			}
			after, err := os.ReadFile(local.path)
			if err != nil || string(before) != string(after) {
				t.Fatalf("already-applied intent changed disk contents: %v", err)
			}
			if !local.Contains(unrelated.URL) || local.Revision() != rev+1 {
				t.Fatal("successful no-op did not refresh the local snapshot")
			}
			if !slices.Equal(local.Stations(), LoadFavorites().Stations()) {
				t.Fatal("local snapshot disagrees with persisted state")
			}
		})
	}
}

func TestFavoritesLockFailurePreservesState(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	f := LoadFavorites()
	station := CatalogStation{Name: "Station", URL: "https://radio.example/stream"}
	if _, err := f.Toggle(station); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.path + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	revision := f.Revision()
	if _, err := f.Toggle(station); err == nil {
		t.Fatal("expected lock failure")
	}
	after, err := os.ReadFile(f.path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("failed toggle changed disk: %v", err)
	}
	if !f.Contains(station.URL) || f.Revision() != revision {
		t.Fatal("failed toggle changed the snapshot")
	}
}

// Separate processes have independent mutexes and snapshots. Exercise the
// advisory file lock with concurrent writers, not just goroutines sharing f.
func TestFavoritesConcurrentProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.LockFile assumes a single writer on Windows")
	}
	if child := os.Getenv("CLIAMP_TEST_RADIO_FAVORITES_CHILD"); child != "" {
		// TestMain clears normal config overrides. Pass the isolated path
		// explicitly rather than falling back to the real user directory.
		path := os.Getenv("CLIAMP_TEST_RADIO_FAVORITES_PATH")
		if path == "" {
			t.Fatal("missing isolated favorites path")
		}
		f := &Favorites{path: path}
		for i := range 4 {
			station := CatalogStation{
				Name: fmt.Sprintf("Station %s/%d", child, i),
				URL:  fmt.Sprintf("https://radio.example/%s/%d", child, i),
			}
			if added, err := f.Toggle(station); err != nil || !added {
				t.Fatalf("child toggle = %v, %v", added, err)
			}
		}
		return
	}
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFavoritesConcurrentProcesses$")
			cmd.Env = append(os.Environ(),
				fmt.Sprintf("CLIAMP_TEST_RADIO_FAVORITES_CHILD=%d", i),
				"CLIAMP_TEST_RADIO_FAVORITES_PATH="+filepath.Join(os.Getenv("CLIAMP_CONFIG_DIR"), favoritesFile),
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("child %d: %v\n%s", i, err, out)
			}
		})
	}
	wg.Wait()
	f := LoadFavorites()
	for i := range 4 {
		for j := range 4 {
			if !f.Contains(fmt.Sprintf("https://radio.example/%d/%d", i, j)) {
				t.Errorf("lost favorite from child %d, station %d", i, j)
			}
		}
	}
}
