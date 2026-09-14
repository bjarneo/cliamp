package radio

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/internal/tomlutil"
)

const favoritesFile = "radio_favorites.toml"

// Favorites manages a persistent set of favorite radio stations.
type Favorites struct {
	mu       sync.RWMutex
	revision uint64
	stations []CatalogStation
	byURL    map[string]struct{}
	path     string
}

// LoadFavorites reads favorites from ~/.config/cliamp/radio_favorites.toml.
func LoadFavorites() *Favorites {
	f := &Favorites{byURL: make(map[string]struct{})}
	dir, err := appdir.Dir()
	if err != nil {
		return f
	}
	f.path = filepath.Join(dir, favoritesFile)
	stations, err := loadFavoriteStations(f.path)
	if err != nil {
		return f
	}
	f.stations = stations
	for _, s := range stations {
		f.byURL[s.URL] = struct{}{}
	}
	return f
}

// Stations returns all favorite stations.
func (f *Favorites) Stations() []CatalogStation {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return slices.Clone(f.stations)
}

// Contains returns true if the station URL is in favorites.
func (f *Favorites) Contains(url string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.byURL[url]
	return ok
}

// Count returns the number of saved stations.
func (f *Favorites) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.stations)
}

// Revision changes when a successful toggle publishes a new local snapshot.
func (f *Favorites) Revision() uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.revision
}

// Toggle applies the opposite of this instance's displayed membership to the
// latest file contents. An intent already applied by another instance is a
// successful no-op on disk, not a second toggle. The result is the new favorite
// state. Failed persistence leaves the local snapshot unchanged.
func (f *Favorites) Toggle(s CatalogStation) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, wasFavorite := f.byURL[s.URL]
	favorite := !wasFavorite
	if f.path == "" {
		dir, err := appdir.Dir()
		if err != nil {
			return false, fmt.Errorf("resolve radio favorites directory: %w", err)
		}
		f.path = filepath.Join(dir, favoritesFile)
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return false, fmt.Errorf("create radio favorites directory: %w", err)
	}
	unlock, err := fileutil.LockFile(f.path + ".lock")
	if err != nil {
		return false, fmt.Errorf("lock radio favorites: %w", err)
	}
	defer func() { _ = unlock() }()

	stations, err := loadFavoriteStations(f.path)
	if err != nil {
		return false, fmt.Errorf("load radio favorites: %w", err)
	}
	exists := slices.ContainsFunc(stations, func(station CatalogStation) bool {
		return station.URL == s.URL
	})
	if exists != favorite {
		if favorite {
			stations = append(stations, s)
		} else {
			stations = slices.DeleteFunc(stations, func(station CatalogStation) bool {
				return station.URL == s.URL
			})
		}
		if err := f.save(stations); err != nil {
			return false, fmt.Errorf("save radio favorites: %w", err)
		}
	}
	f.stations = stations
	f.byURL = make(map[string]struct{}, len(stations))
	for _, station := range stations {
		f.byURL[station.URL] = struct{}{}
	}
	f.revision++
	return favorite, nil
}

func (f *Favorites) save(stations []CatalogStation) error {
	// Build the full content in memory (writes to a Builder can't fail), then
	// hand it to WriteFileAtomic so a partial or failed write can never
	// truncate or corrupt the existing favorites file.
	var b strings.Builder
	for i, s := range stations {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		fmt.Fprintln(&b, "[[station]]")
		fmt.Fprintf(&b, "name = %q\n", s.Name)
		fmt.Fprintf(&b, "url = %q\n", s.URL)
		if s.Country != "" {
			fmt.Fprintf(&b, "country = %q\n", s.Country)
		}
		if s.State != "" {
			fmt.Fprintf(&b, "state = %q\n", s.State)
		}
		if s.Bitrate > 0 {
			fmt.Fprintf(&b, "bitrate = %d\n", s.Bitrate)
		}
		if s.Codec != "" {
			fmt.Fprintf(&b, "codec = %q\n", s.Codec)
		}
		if s.Tags != "" {
			fmt.Fprintf(&b, "tags = %q\n", s.Tags)
		}
		if s.Homepage != "" {
			fmt.Fprintf(&b, "homepage = %q\n", s.Homepage)
		}
	}

	return fileutil.WriteFileAtomic(f.path, []byte(b.String()), 0o644)
}

// loadFavoriteStations parses the favorites TOML file.
func loadFavoriteStations(path string) ([]CatalogStation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var stations []CatalogStation
	tomlutil.ParseSections(data, "station", func(f map[string]string) {
		s := CatalogStation{
			Name:     f["name"],
			URL:      f["url"],
			Country:  f["country"],
			State:    f["state"],
			Codec:    f["codec"],
			Tags:     f["tags"],
			Homepage: f["homepage"],
		}
		if n, err := strconv.Atoi(f["bitrate"]); err == nil {
			s.Bitrate = n
		}
		if s.Name != "" && s.URL != "" {
			stations = append(stations, s)
		}
	})
	return stations, nil
}
