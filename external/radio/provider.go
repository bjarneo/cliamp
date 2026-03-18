// Package radio implements a playlist.Provider for internet radio stations.
// It includes a built-in cliamp radio stream and reads user-defined stations
// from ~/.config/cliamp/radios.toml.
package radio

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"cliamp/internal/appdir"
	"cliamp/internal/tomlutil"
	"cliamp/playlist"
)

const builtinLofiName = "cliamp radio"
const builtinLofiURL = "https://radio.cliamp.stream/lofi/stream.pls"
const builtinSynthwaveName = "cliamp synthwave"
const builtinSynthwaveURL = "https://radio.cliamp.stream/synthwave/stream.pls"

// Provider serves radio stations as single-track playlists.
type Provider struct {
	stations []station
}

type station struct {
	name string
	url  string
}

// New creates a Provider with the built-in station plus any user-defined
// stations from ~/.config/cliamp/radios.toml.
func New() *Provider {
	p := &Provider{
		stations: []station{
			{name: builtinLofiName, url: builtinLofiURL},
			{name: builtinSynthwaveName, url: builtinSynthwaveURL},
		},
	}

	dir, err := appdir.Dir()
	if err != nil {
		return p
	}
	if extra, err := loadStations(filepath.Join(dir, "radios.toml")); err == nil {
		p.stations = append(p.stations, extra...)
	}
	return p
}

func (p *Provider) Name() string { return "Radio" }

func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	var out []playlist.PlaylistInfo
	for i, s := range p.stations {
		out = append(out, playlist.PlaylistInfo{
			ID:         strconv.Itoa(i),
			Name:       s.name,
			TrackCount: 1,
		})
	}
	return out, nil
}

func (p *Provider) Tracks(id string) ([]playlist.Track, error) {
	idx, err := strconv.Atoi(id)
	if err != nil || idx < 0 || idx >= len(p.stations) {
		return nil, errors.New("invalid station ID")
	}
	s := p.stations[idx]
	return []playlist.Track{{
		Path:     s.url,
		Title:    s.name,
		Stream:   true,
		Realtime: true,
	}}, nil
}

// loadStations parses a TOML file with [[station]] sections.
func loadStations(path string) ([]station, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var stations []station
	var current *station

	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[[station]]" {
			if current != nil && current.name != "" && current.url != "" {
				stations = append(stations, *current)
			}
			current = &station{}
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
		val = tomlutil.Unquote(val)

		switch key {
		case "name":
			current.name = val
		case "url":
			current.url = val
		}
	}
	if current != nil && current.name != "" && current.url != "" {
		stations = append(stations, *current)
	}
	return stations, nil
}
