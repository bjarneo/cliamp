package radio

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/internal/tomlutil"
)

const pinsFile = "radio_countries.toml"

// Place is a country, or a region within one, that the listener has pinned.
// State is empty for a whole country.
type Place struct {
	Code  string // ISO 3166-1 alpha-2
	Name  string // display name, e.g. "Norway" or "Oslo, Norway"
	State string
}

// ID is the stable identifier a Place is addressed by: "NO" for a country,
// "NO/Oslo" for a region within one.
func (p Place) ID() string {
	if p.State == "" {
		return p.Code
	}
	return p.Code + "/" + p.State
}

// parsePlaceID is the inverse of Place.ID. It returns ok=false for anything
// that does not start with a usable country code.
func parsePlaceID(id string) (code, state string, ok bool) {
	code, state, _ = strings.Cut(id, "/")
	code = normalizeCountryCode(code)
	if code == "" {
		return "", "", false
	}
	return code, strings.TrimSpace(state), true
}

// Pins is a persistent, ordered list of pinned places. A pin does two things:
// it puts the place in the radio pane, and it joins the rotation the catalog
// country filter cycles through.
type Pins struct {
	mu     sync.Mutex
	places []Place
	path   string
}

// LoadPins reads pinned places from ~/.config/cliamp/radio_countries.toml.
// A missing or unreadable file yields an empty set. Toggle does not replace
// an unreadable file.
func LoadPins() *Pins {
	p := &Pins{}
	dir, err := appdir.Dir()
	if err != nil {
		return p
	}
	p.path = filepath.Join(dir, pinsFile)
	places, err := loadPlaces(p.path)
	if err != nil {
		return p
	}
	p.places = places
	return p
}

// Places returns the pinned places in the order they were added.
func (p *Pins) Places() []Place {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.places)
}

// Contains reports whether the given place ID is pinned.
func (p *Pins) Contains(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.ContainsFunc(p.places, func(place Place) bool { return place.ID() == id })
}

// Toggle applies the opposite of this instance's pin state for place to the
// latest file contents, under a file lock shared with other cliamp instances.
// An intent that another instance already applied is a successful no-op on
// disk. Toggle reports whether the place is pinned after the call. A failed
// read or save leaves the pins and the file unchanged.
//
// Pins carries its own lock so that the write, which fsyncs the file and its
// directory, never runs under the provider mutex the renderer reads through.
func (p *Pins) Toggle(place Place) (pinned bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := place.ID()
	match := func(c Place) bool { return c.ID() == id }
	wasPinned := slices.ContainsFunc(p.places, match)
	if p.path == "" {
		dir, err := appdir.Dir()
		if err != nil {
			return wasPinned, fmt.Errorf("resolve radio pins directory: %w", err)
		}
		p.path = filepath.Join(dir, pinsFile)
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return wasPinned, fmt.Errorf("create radio pins directory: %w", err)
	}
	unlock, err := fileutil.LockFile(p.path + ".lock")
	if err != nil {
		return wasPinned, fmt.Errorf("lock radio pins: %w", err)
	}
	defer func() { _ = unlock() }()

	places, err := loadPlaces(p.path)
	if err != nil {
		return wasPinned, fmt.Errorf("load radio pins: %w", err)
	}
	if slices.ContainsFunc(places, match) == wasPinned {
		if wasPinned {
			places = slices.DeleteFunc(places, match)
		} else {
			places = append(places, place)
		}
		if err := p.save(places); err != nil {
			return wasPinned, fmt.Errorf("save radio pins: %w", err)
		}
	}
	p.places = places
	return !wasPinned, nil
}

// save persists places as the pin list. p.mu and the file lock must be held.
func (p *Pins) save(places []Place) error {
	var b strings.Builder
	for i, place := range places {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		fmt.Fprintln(&b, "[[country]]")
		fmt.Fprintf(&b, "code = %q\n", place.Code)
		fmt.Fprintf(&b, "name = %q\n", place.Name)
		if place.State != "" {
			fmt.Fprintf(&b, "state = %q\n", place.State)
		}
	}
	return fileutil.WriteFileAtomic(p.path, []byte(b.String()), 0o644)
}

func loadPlaces(path string) ([]Place, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var places []Place
	tomlutil.ParseSections(data, "country", func(f map[string]string) {
		code := normalizeCountryCode(f["code"])
		if code == "" {
			return
		}
		place := Place{Code: code, Name: f["name"], State: strings.TrimSpace(f["state"])}
		if place.Name == "" {
			place.Name = code
		}
		places = append(places, place)
	})
	return places, nil
}
