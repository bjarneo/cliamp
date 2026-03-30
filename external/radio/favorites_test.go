package radio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFavoritesAddRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "radio_favorites.toml")

	f := &Favorites{byURL: make(map[string]struct{}), path: path}

	s := CatalogStation{
		Name:    "Test FM",
		URL:     "https://test.example.com/stream",
		Country: "Norway",
		Bitrate: 128,
	}

	// Add
	if err := f.Add(s); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !f.Contains(s.URL) {
		t.Fatal("expected Contains to return true after Add")
	}
	if len(f.Stations()) != 1 {
		t.Fatalf("expected 1 station, got %d", len(f.Stations()))
	}

	// Add duplicate should be no-op
	if err := f.Add(s); err != nil {
		t.Fatalf("Add duplicate: %v", err)
	}
	if len(f.Stations()) != 1 {
		t.Fatalf("expected 1 station after duplicate add, got %d", len(f.Stations()))
	}

	// Verify persistence
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty favorites file")
	}

	// Reload from disk
	stations, err := loadFavoriteStations(path)
	if err != nil {
		t.Fatalf("loadFavoriteStations: %v", err)
	}
	if len(stations) != 1 || stations[0].Name != "Test FM" {
		t.Fatalf("unexpected reloaded stations: %+v", stations)
	}

	// Remove
	if err := f.Remove(s.URL); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if f.Contains(s.URL) {
		t.Fatal("expected Contains to return false after Remove")
	}
	if len(f.Stations()) != 0 {
		t.Fatalf("expected 0 stations after remove, got %d", len(f.Stations()))
	}

	// Remove non-existent should be no-op
	if err := f.Remove("https://nonexistent.example.com"); err != nil {
		t.Fatalf("Remove non-existent: %v", err)
	}
}

func TestFavoritesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "radio_favorites.toml")

	f := &Favorites{byURL: make(map[string]struct{}), path: path}

	stations := []CatalogStation{
		{Name: "Jazz FM", URL: "https://jazz.example.com/stream", Country: "UK", Bitrate: 320, Codec: "mp3"},
		{Name: "Rock Radio", URL: "https://rock.example.com/stream", Country: "US", Bitrate: 192, Tags: "rock,metal"},
	}
	for _, s := range stations {
		if err := f.Add(s); err != nil {
			t.Fatalf("Add %s: %v", s.Name, err)
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
	if loaded[0].Country != "UK" || loaded[1].Tags != "rock,metal" {
		t.Fatalf("unexpected loaded data: %+v", loaded)
	}
}
