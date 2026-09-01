// Package radio implements a playlist.Provider for internet radio stations.
// It includes a built-in cliamp radio stream, user-defined stations from
// ~/.config/cliamp/radios.toml, favorites from radio_favorites.toml, and
// lazy-loaded catalog stations from the Radio Browser API.
package radio

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/tomlutil"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Compile-time interface checks.
var (
	_ provider.FavoriteToggler      = (*Provider)(nil)
	_ provider.CatalogLoader        = (*Provider)(nil)
	_ provider.CatalogSearcher      = (*Provider)(nil)
	_ provider.RadioStatsLoader     = (*Provider)(nil)
	_ provider.SectionedList        = (*Provider)(nil)
	_ provider.SectionTitler        = (*Provider)(nil)
	_ provider.GenreBrowser         = (*Provider)(nil)
	_ provider.GenreFavoriteToggler = (*Provider)(nil)
	_ provider.GenreLabeler         = (*Provider)(nil)
	_ provider.LocationConsenter    = (*Provider)(nil)
	_ provider.BrowseEntryProvider  = (*Provider)(nil)
	_ playlist.Refresher            = (*Provider)(nil)
)

const builtinName = "cliamp radio"
const builtinURL = "https://radio.cliamp.stream/streams.m3u"

// Section headings for each ID prefix, shown above the rows they cover in the
// radio pane. The browse shortcut shares the pinned-places heading so the two
// render as one block.
const (
	sectionCountries = "Countries"
	sectionStations  = "Stations"
	sectionFavorites = "Favorites"
	sectionCatalog   = "Catalog"
	sectionSearch    = "Search Results"
)

// Options configures the provider at construction time.
type Options struct {
	// Country records what the listener has already said about their location:
	// an ISO 3166-1 alpha-2 code to use, CountryDeclined to leave it alone, or
	// empty for "not asked yet". Nothing is detected until this is a code, so
	// a fresh install works out nobody's location on its own.
	Country string
	// SaveCountry persists the listener's answer so they are asked once rather
	// than every launch. A nil SaveCountry keeps the answer for this run only.
	SaveCountry func(code string) error
}

// CountryDeclined is the Country value recording that the listener said no to
// location detection.
const CountryDeclined = "none"

// Provider serves radio stations as single-track playlists.
// It combines local stations, pinned places, user favorites, and catalog
// stations from the Radio Browser API into a single unified list.
type Provider struct {
	mu            sync.Mutex
	stations      []station        // built-in + user-defined (radios.toml)
	favorites     *Favorites       // user favorites (radio_favorites.toml)
	pins          *Pins            // pinned countries and regions (radio_countries.toml)
	home          Place            // listener's own country; zero when unknown
	catalog       []CatalogStation // lazily loaded from Radio Browser API
	searchResults []CatalogStation // non-nil when API search is active
	countries     []Country        // cached country index, nil until first browse
	states        []State          // cached regions of the home country
	// locationSettled is false only until the listener answers the location
	// question. It gates whether to ask, not whether p.home may be used.
	locationSettled bool
	saveCountry     func(string) error
}

type station struct {
	name string
	url  string
}

// New creates a Provider with the built-in station plus any user-defined
// stations from ~/.config/cliamp/radios.toml, favorites, and pinned places.
func New(opts Options) *Provider {
	p := &Provider{
		stations: []station{
			{name: builtinName, url: builtinURL},
		},
	}

	p.saveCountry = opts.SaveCountry

	answer := strings.TrimSpace(opts.Country)
	if code := normalizeCountryCode(answer); code != "" {
		p.home = namedPlace(code)
		p.locationSettled = true
	} else if strings.EqualFold(answer, CountryDeclined) {
		p.locationSettled = true
	}

	dir, err := appdir.Dir()
	if err != nil {
		p.favorites = &Favorites{byURL: make(map[string]struct{})}
		p.pins = &Pins{}
		return p
	}
	if extra, err := loadStations(filepath.Join(dir, "radios.toml")); err == nil {
		p.stations = append(p.stations, extra...)
	}
	p.favorites = LoadFavorites()
	p.pins = LoadPins()
	return p
}

// namedPlace pairs a country code with the best name available without a
// network call. The directory's own name replaces it once its index loads.
func namedPlace(code string) Place {
	name := CountryName(code)
	if name == "" {
		name = code
	}
	return Place{Code: code, Name: name}
}

func (p *Provider) Name() string { return "Radio" }

// Playlists returns a unified list: pinned places, local stations, favorites
// (★ prefixed), then catalog stations (with metadata). IDs are prefixed with
// "p:", "l:", "f:", or "c:".
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var out []playlist.PlaylistInfo

	// When search is active, show only search results.
	if p.searchResults != nil {
		for i, s := range p.searchResults {
			out = append(out, p.catalogEntry("s", i, s))
		}
		return out, nil
	}

	// The location offer sits at the top of the Countries section, directly
	// under the browse shortcut, and only until the listener has answered.
	if !p.locationSettled {
		out = append(out, playlist.PlaylistInfo{
			ID:   locationConsentID,
			Name: "Use my location",
		})
	}

	// Places: the listener's own country first, then whatever they pinned.
	for i, place := range p.placesLocked() {
		name := place.Name
		if i == 0 && place.ID() == p.homeLocked().ID() {
			name += " (near you)"
		} else {
			name = "★ " + name
		}
		out = append(out, playlist.PlaylistInfo{
			ID:   fmt.Sprintf("p:%d", i),
			Name: name,
		})
	}

	// Local stations.
	for i, s := range p.stations {
		out = append(out, playlist.PlaylistInfo{
			ID:   fmt.Sprintf("l:%d", i),
			Name: s.name,
		})
	}

	// Favorites.
	for i, s := range p.favorites.Stations() {
		out = append(out, playlist.PlaylistInfo{
			ID:   fmt.Sprintf("f:%d", i),
			Name: "★ " + formatCatalogName(s),
		})
	}

	// Catalog stations.
	for i, s := range p.catalog {
		out = append(out, p.catalogEntry("c", i, s))
	}

	return out, nil
}

// catalogEntry builds a PlaylistInfo for a CatalogStation, marking favorites with ★.
func (p *Provider) catalogEntry(prefix string, idx int, s CatalogStation) playlist.PlaylistInfo {
	name := formatCatalogName(s)
	if p.favorites.Contains(s.URL) {
		name = "★ " + name
	}
	return playlist.PlaylistInfo{
		ID:   fmt.Sprintf("%s:%d", prefix, idx),
		Name: name,
	}
}

// Tracks returns a playlist for the given ID: a single stream for a station,
// or a country's or region's stations for a place.
func (p *Provider) Tracks(id string) ([]playlist.Track, error) {
	// The location offer is a question, not a playlist. The UI intercepts it
	// before reaching here; refuse it by name so a caller that does not (an
	// IPC client, say) gets a useful error rather than "invalid station ID".
	if id == locationConsentID {
		return nil, errors.New("radio: the location row is a prompt, not a playlist")
	}

	prefix, idx, err := parseStationID(id)
	if err != nil {
		return nil, err
	}

	// A place is not a station: it expands to that country's or region's
	// stations, so next and previous scan through them. The directory call
	// runs off the provider lock, which the UI needs to render the pane.
	if prefix == "p" {
		place, ok := p.placeAt(idx)
		if !ok {
			return nil, errors.New("invalid place index")
		}
		return p.GenreTracks(place.ID(), SortVotes)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var url, title string
	switch prefix {
	case "l":
		if idx < 0 || idx >= len(p.stations) {
			return nil, errors.New("invalid local station index")
		}
		url, title = p.stations[idx].url, p.stations[idx].name
	case "f":
		favs := p.favorites.Stations()
		if idx < 0 || idx >= len(favs) {
			return nil, errors.New("invalid favorite index")
		}
		url, title = favs[idx].URL, favs[idx].Name
	case "c":
		if idx < 0 || idx >= len(p.catalog) {
			return nil, errors.New("invalid catalog station index")
		}
		url, title = p.catalog[idx].URL, p.catalog[idx].Name
	case "s":
		if p.searchResults == nil || idx < 0 || idx >= len(p.searchResults) {
			return nil, errors.New("invalid search result index")
		}
		url, title = p.searchResults[idx].URL, p.searchResults[idx].Name
	default:
		return nil, errors.New("unknown station type")
	}

	return []playlist.Track{{
		Path: url, Title: title, Stream: true, Realtime: true,
	}}, nil
}

// placeAt returns the place at index idx of the pane's Countries section.
func (p *Provider) placeAt(idx int) (Place, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	places := p.placesLocked()
	if idx < 0 || idx >= len(places) {
		return Place{}, false
	}
	return places[idx], true
}

// AppendCatalog adds catalog stations fetched from the Radio Browser API.
func (p *Provider) AppendCatalog(stations []CatalogStation) {
	stations = streamableStations(stations)
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]struct{}, len(p.catalog)+len(stations))
	for _, station := range p.catalog {
		seen[station.URL] = struct{}{}
	}
	for _, station := range stations {
		if _, exists := seen[station.URL]; exists {
			continue
		}
		seen[station.URL] = struct{}{}
		p.catalog = append(p.catalog, station)
	}
}

// ToggleFavorite toggles the favorite status of a catalog or favorite entry.
// Returns (true, name) if added, (false, name) if removed.
func (p *Provider) ToggleFavorite(id string) (added bool, name string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	prefix, idx, err := parseStationID(id)
	if err != nil {
		return false, "", err
	}

	var s CatalogStation
	switch prefix {
	case "c":
		if idx < 0 || idx >= len(p.catalog) {
			return false, "", errors.New("invalid catalog index")
		}
		s = p.catalog[idx]
	case "s":
		if p.searchResults == nil || idx < 0 || idx >= len(p.searchResults) {
			return false, "", errors.New("invalid search result index")
		}
		s = p.searchResults[idx]
	case "f":
		favs := p.favorites.Stations()
		if idx < 0 || idx >= len(favs) {
			return false, "", errors.New("invalid favorite index")
		}
		s = favs[idx]
	default:
		return false, "", errors.New("cannot favorite local stations")
	}

	if p.favorites.Contains(s.URL) {
		return false, s.Name, p.favorites.Remove(s.URL)
	}
	return true, s.Name, p.favorites.Add(s)
}

// SetSearchResults activates search mode with the given results.
// Playlists() will return search results instead of catalog stations.
func (p *Provider) SetSearchResults(stations []CatalogStation) {
	stations = streamableStations(stations)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.searchResults = stations
}

// streamableStations drops directory entries whose URL is not http or https.
//
// Radio Browser is a public directory: anyone can submit a station with any
// url_resolved value, and that string becomes a playlist.Track.Path that
// playback dispatches on. An "ssh://" path reaches exec.Command("ssh", ...)
// against the submitter's host, and a bare filesystem path is opened as a
// local file. A station is by definition a network stream, so nothing
// legitimate is lost by requiring one here.
//
// Local stations from radios.toml are not filtered: that file is the user's
// own, and it carries the trust of anything else they type.
func streamableStations(stations []CatalogStation) []CatalogStation {
	filtered := make([]CatalogStation, 0, len(stations))
	for _, station := range stations {
		u, err := url.Parse(strings.TrimSpace(station.URL))
		if err != nil {
			continue
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			if u.Host != "" {
				filtered = append(filtered, station)
			}
		}
	}
	return filtered
}

// ClearSearch deactivates search mode, restoring the catalog view.
func (p *Provider) ClearSearch() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.searchResults = nil
}

// IsSearching returns true if API search results are active.
func (p *Provider) IsSearching() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.searchResults != nil
}

// LoadCatalogPage fetches the next page of catalog entries from the Radio
// Browser API, the directory's own top-voted feed, and appends them to the
// provider's catalog.
// Implements provider.CatalogLoader.
func (p *Provider) LoadCatalogPage(offset, limit int) (int, error) {
	stations, err := Stations(StationQuery{Order: SortVotes, Offset: offset, Limit: limit})
	if err != nil {
		return 0, err
	}
	p.AppendCatalog(stations)
	return len(stations), nil
}

// SearchCatalog performs a server-side station search via the Radio Browser
// API. Results are reflected in subsequent Playlists() calls.
// Implements provider.CatalogSearcher.
func (p *Provider) SearchCatalog(query string) (int, error) {
	stations, err := Stations(StationQuery{Name: query, Order: SortVotes, Limit: searchLimit})
	if err != nil {
		return 0, err
	}
	p.SetSearchResults(stations)
	return len(stations), nil
}

// searchLimit caps how many results one catalog search returns.
const searchLimit = 200

// SectionTitle names the pane section for an ID prefix. The provider owns this
// wording because only it knows what its prefixes mean.
// Implements provider.SectionTitler.
func (*Provider) SectionTitle(prefix string) string {
	switch prefix {
	case "p", "browse", "loc":
		return sectionCountries
	case "l":
		return sectionStations
	case "f":
		return sectionFavorites
	case "c":
		return sectionCatalog
	case "s":
		return sectionSearch
	default:
		return ""
	}
}

// IsFavoritableID reports whether the given ID can be favorited.
// Implements provider.SectionedList.
func (p *Provider) IsFavoritableID(id string) bool {
	return IsCatalogOrFavID(id)
}

// IsCatalogOrFavID returns true if the ID belongs to a catalog, search, or favorite entry.
func IsCatalogOrFavID(id string) bool {
	return strings.HasPrefix(id, "c:") || strings.HasPrefix(id, "f:") || strings.HasPrefix(id, "s:")
}

// IDPrefix returns the type prefix of a provider list ID ("p", "l", "f", "c",
// "s", "browse", or "").
// Also implements provider.SectionedList when called as a method.
func (p *Provider) IDPrefix(id string) string {
	return idPrefix(id)
}

func idPrefix(id string) string {
	prefix, _, ok := strings.Cut(id, ":")
	if !ok {
		return ""
	}
	return prefix
}

// parseStationID splits a prefixed ID like "c:42" into its prefix and index.
// Legacy numeric IDs (no colon) are treated as "l:" local station indices.
func parseStationID(id string) (prefix string, idx int, err error) {
	raw := id
	prefix, idxStr, ok := strings.Cut(id, ":")
	if !ok {
		prefix = "l"
		idxStr = raw
	}
	idx, err = strconv.Atoi(idxStr)
	if err != nil {
		return "", 0, errors.New("invalid station ID")
	}
	return prefix, idx, nil
}

// formatCatalogName builds a display name from a CatalogStation. The country
// is trimmed the same way the country browser trims it, so one station does
// not read as being in "The United States Of America" while the country it was
// picked from reads as "United States Of America".
func formatCatalogName(s CatalogStation) string {
	name := s.Name
	if s.Bitrate > 0 {
		name += fmt.Sprintf(" [%dk]", s.Bitrate)
	}
	if country := displayCountryName(s.Country); country != "" {
		name += " · " + country
	}
	return name
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
	tomlutil.ParseSections(data, "station", func(f map[string]string) {
		s := station{name: f["name"], url: f["url"]}
		if s.name != "" && s.url != "" {
			stations = append(stations, s)
		}
	})
	return stations, nil
}
