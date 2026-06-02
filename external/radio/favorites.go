package radio

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cliamp/internal/appdir"
	"cliamp/internal/tomlutil"
)

const favoritesFile = "radio_favorites.toml"

// Favorites manages a persistent set of favorite radio stations.
type Favorites struct {
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
	return f.stations
}

// Contains returns true if the station URL is in favorites.
func (f *Favorites) Contains(url string) bool {
	_, ok := f.byURL[url]
	return ok
}

// Add adds a station to favorites and saves to disk.
func (f *Favorites) Add(s CatalogStation) error {
	if f.Contains(s.URL) {
		return nil
	}
	f.stations = append(f.stations, s)
	f.byURL[s.URL] = struct{}{}
	return f.save()
}

// Remove removes a station by URL from favorites and saves to disk.
func (f *Favorites) Remove(url string) error {
	if !f.Contains(url) {
		return nil
	}
	for i, s := range f.stations {
		if s.URL == url {
			f.stations = append(f.stations[:i], f.stations[i+1:]...)
			break
		}
	}
	delete(f.byURL, url)
	return f.save()
}

func (f *Favorites) save() error {
	if f.path == "" {
		dir, err := appdir.Dir()
		if err != nil {
			return err
		}
		f.path = filepath.Join(dir, favoritesFile)
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}

	// Build the full content in memory (writes to a Builder can't fail), then
	// write a temp file and rename so a partial/failed write can never truncate
	// or corrupt the existing favorites file.
	var b strings.Builder
	for i, s := range f.stations {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		fmt.Fprintln(&b, "[[station]]")
		fmt.Fprintf(&b, "name = %q\n", s.Name)
		fmt.Fprintf(&b, "url = %q\n", s.URL)
		if s.Country != "" {
			fmt.Fprintf(&b, "country = %q\n", s.Country)
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

	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
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
