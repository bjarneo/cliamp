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
	file, err := os.Create(f.path)
	if err != nil {
		return err
	}
	defer file.Close()

	for i, s := range f.stations {
		if i > 0 {
			fmt.Fprintln(file)
		}
		fmt.Fprintln(file, "[[station]]")
		fmt.Fprintf(file, "name = %q\n", s.Name)
		fmt.Fprintf(file, "url = %q\n", s.URL)
		if s.Country != "" {
			fmt.Fprintf(file, "country = %q\n", s.Country)
		}
		if s.Bitrate > 0 {
			fmt.Fprintf(file, "bitrate = %d\n", s.Bitrate)
		}
		if s.Codec != "" {
			fmt.Fprintf(file, "codec = %q\n", s.Codec)
		}
		if s.Tags != "" {
			fmt.Fprintf(file, "tags = %q\n", s.Tags)
		}
		if s.Homepage != "" {
			fmt.Fprintf(file, "homepage = %q\n", s.Homepage)
		}
	}
	return nil
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
	var current *CatalogStation

	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[[station]]" {
			if current != nil && current.Name != "" && current.URL != "" {
				stations = append(stations, *current)
			}
			current = &CatalogStation{}
			continue
		}
		if current == nil {
			continue
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		switch key {
		case "name":
			current.Name = tomlutil.Unquote(val)
		case "url":
			current.URL = tomlutil.Unquote(val)
		case "country":
			current.Country = tomlutil.Unquote(val)
		case "codec":
			current.Codec = tomlutil.Unquote(val)
		case "tags":
			current.Tags = tomlutil.Unquote(val)
		case "homepage":
			current.Homepage = tomlutil.Unquote(val)
		case "bitrate":
			if n, err := strconv.Atoi(val); err == nil {
				current.Bitrate = n
			}
		}
	}
	if current != nil && current.Name != "" && current.URL != "" {
		stations = append(stations, *current)
	}
	return stations, nil
}
