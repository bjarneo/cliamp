package radio

import (
	"fmt"
	"slices"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// browseCountriesID is the provider-pane shortcut into the country browser.
const browseCountriesID = "browse:countries"

// locationConsentID is the provider-pane row that offers to work out the
// listener's country. Selecting it raises the question; it is not a playlist.
const locationConsentID = "loc:ask"

// countryTrackLimit caps how many stations one country or region contributes
// to a playlist. The largest country in the directory has ~8000 entries, which
// is not a playlist anyone navigates; the top few hundred by the chosen order
// is.
const countryTrackLimit = 200

// GenreLabel names the category level. The browse overlay calls these
// "genres"; for a radio directory they are places.
func (*Provider) GenreLabel() string { return "Countries" }

// BrowseEntries advertises the country and tag browsers in the radio pane.
// They are unanchored, which puts both discovery routes ahead of the flat
// station catalog.
func (*Provider) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{
		{
			ID:             browseCountriesID,
			Name:           "Browse all countries",
			Section:        sectionCountries,
			Mode:           provider.BrowseGenres,
			OpenInPlaylist: true,
		},
		{
			ID:             browseTagsID,
			Name:           "Browse genres & tags",
			Section:        sectionGenres,
			Mode:           provider.BrowseGenres,
			OpenInPlaylist: true,
		},
	}
}

// Genres lists the places stations can be browsed by: every country in the
// directory, grouped by region, preceded by the regions of the listener's own
// country when one is known.
//
// Regions are only offered for the home country. About 44% of stations carry a
// state at all, so a full country-by-region tree would be mostly empty; one
// country's worth is the part that earns its API call.
func (p *Provider) Genres() ([]provider.GenreInfo, error) {
	countries, err := p.countryIndex()
	if err != nil {
		return nil, err
	}

	// HomeCountry resolves its name against the index fetched just above, so a
	// non-empty name also means "this code is one the directory knows".
	home := p.HomeCountry()
	// Snapshot the pins once: checking each of ~250 countries against the live
	// set would take and release the provider lock once per row.
	pinned := p.pinnedIDs()

	out := make([]provider.GenreInfo, 0, len(countries))
	if home.Code != "" && countryName(countries, home.Code) != "" {
		out = append(out, p.homeRegionGenres(home, pinned)...)
	}
	for _, c := range countries {
		if c.StationCount <= 0 {
			continue
		}
		id := Place{Code: c.Code}.ID()
		out = append(out, provider.GenreInfo{
			ID:       id,
			Name:     fmt.Sprintf("%s (%d)", c.Name, c.StationCount),
			Group:    Region(c.Code),
			Favorite: pinned[id],
		})
	}
	return out, nil
}

// pinnedIDs snapshots the pinned place IDs for one pass over the country list.
func (p *Provider) pinnedIDs() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make(map[string]bool, len(p.pins.Places()))
	for _, place := range p.pins.Places() {
		ids[place.ID()] = true
	}
	return ids
}

// homeRegionGenres returns the home country's regions, grouped under its name
// so they sort and read as one block at the top of the list. A directory that
// knows no regions for the country contributes nothing.
func (p *Provider) homeRegionGenres(home Place, pinned map[string]bool) []provider.GenreInfo {
	states, err := p.stateIndex(home.Name)
	if err != nil || len(states) == 0 {
		// Regions are a convenience; losing them must not cost the country list.
		return nil
	}

	out := make([]provider.GenreInfo, 0, len(states))
	for _, s := range states {
		id := Place{Code: home.Code, State: s.Name}.ID()
		out = append(out, provider.GenreInfo{
			ID:       id,
			Name:     fmt.Sprintf("%s (%d)", s.Name, s.StationCount),
			Group:    home.Name,
			Favorite: pinned[id],
		})
	}
	return out
}

// GenreSortTypes returns the orders a place's stations can be listed in.
func (*Provider) GenreSortTypes() []provider.SortType {
	return radioSortTypes()
}

// radioSortTypes returns the station orders shared by place and tag browsing.
func radioSortTypes() []provider.SortType {
	return []provider.SortType{
		{ID: SortVotes, Label: "Most Voted"},
		{ID: SortClicks, Label: "Most Listened"},
		{ID: SortTrend, Label: "Trending"},
		{ID: SortName, Label: "By Name"},
		{ID: SortRandom, Label: "Random"},
	}
}

// GenreTracks returns a place's stations as a playlist, so that next and
// previous scan through the stations of one country or region.
func (p *Provider) GenreTracks(genreID, sortType string) ([]playlist.Track, error) {
	code, state, ok := parsePlaceID(genreID)
	if !ok {
		return nil, fmt.Errorf("radio: unknown place %q", genreID)
	}

	stations, err := Stations(StationQuery{
		CountryCode: code,
		State:       state,
		Order:       sortType,
		Limit:       countryTrackLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("radio: list stations for %s: %w", genreID, err)
	}
	return stationTracks(streamableStations(stations)), nil
}

// ToggleGenreFavorite pins or unpins a place. Pinned places show in the radio
// pane and join the rotation the catalog country filter cycles through.
func (p *Provider) ToggleGenreFavorite(genreID string) (bool, error) {
	code, state, ok := parsePlaceID(genreID)
	if !ok {
		return false, fmt.Errorf("radio: unknown place %q", genreID)
	}

	p.mu.Lock()
	place := Place{Code: code, State: state, Name: p.placeNameLocked(code, state)}
	p.mu.Unlock()

	// Toggle writes and fsyncs; it takes the pin store's own lock, not ours.
	return p.pins.Toggle(place)
}

// placeNameLocked builds the display name for a place, preferring the
// directory's country index and falling back to the built-in name table when
// it has not loaded. p.mu must be held.
func (p *Provider) placeNameLocked(code, state string) string {
	name := countryName(p.countries, code)
	if name == "" {
		name = CountryName(code)
	}
	if name == "" {
		name = code
	}
	if state == "" {
		return name
	}
	return state + ", " + name
}

// NeedsLocationConsent reports whether the listener still has to answer the
// location question. Configuration that already names a country, or records a
// refusal, counts as answered.
// Implements provider.LocationConsenter.
func (p *Provider) NeedsLocationConsent() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.locationSettled
}

// LocationConsentID is the provider-list row that opens the location question.
// It is empty once the listener has answered, so the row disappears rather
// than sitting there re-asking.
// Implements provider.LocationConsenter.
func (p *Provider) LocationConsentID() string {
	if !p.NeedsLocationConsent() {
		return ""
	}
	return locationConsentID
}

// LocationPrompt returns the question to put to the listener.
// Implements provider.LocationConsenter.
func (*Provider) LocationPrompt() string {
	return "Use your country to suggest nearby radio? cliamp would read it from your system timezone. Nothing is sent to a location service."
}

// SetLocationConsent records the listener's answer and persists it, so the
// question is asked once rather than every launch. Detection happens here and
// nowhere else: until this is called with true, the provider has not worked
// out where anybody is.
// Implements provider.LocationConsenter.
func (p *Provider) SetLocationConsent(allowed bool) (string, error) {
	code := ""
	if allowed {
		code = DetectHomeCountry()
	}

	p.mu.Lock()
	p.locationSettled = true
	if code != "" {
		p.home = namedPlace(code)
	} else {
		p.home = Place{}
	}
	home, save := p.home, p.saveCountry
	p.mu.Unlock()

	if save == nil {
		return home.Name, nil
	}
	// Record a refusal, and an allowed-but-undetectable answer, as a refusal:
	// both mean "do not use a location", and neither should re-ask next launch.
	stored := code
	if stored == "" {
		stored = CountryDeclined
	}
	if err := save(stored); err != nil {
		return home.Name, fmt.Errorf("radio: save location choice: %w", err)
	}
	return home.Name, nil
}

// HomeCountry returns the listener's own country, or the zero Place when it
// could not be determined.
func (p *Provider) HomeCountry() Place {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.homeLocked()
}

// homeLocked upgrades the home country's display name to the directory's own
// once its index has loaded; until then the built-in table's name stands.
// p.mu must be held.
func (p *Provider) homeLocked() Place {
	if p.home.Code == "" {
		return Place{}
	}
	if name := countryName(p.countries, p.home.Code); name != "" {
		p.home.Name = name
	}
	return p.home
}

// countryIndex returns the directory's country list, fetching it once per
// process. Refresh drops the cache.
func (p *Provider) countryIndex() ([]Country, error) {
	p.mu.Lock()
	cached := p.countries
	p.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	countries, err := FetchCountries()
	if err != nil {
		return nil, fmt.Errorf("radio: list countries: %w", err)
	}

	p.mu.Lock()
	p.countries = countries
	p.mu.Unlock()
	return countries, nil
}

// stateIndex returns the home country's regions, fetching them once per
// process. Refresh drops the cache.
func (p *Provider) stateIndex(country string) ([]State, error) {
	p.mu.Lock()
	cached := p.states
	p.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	states, err := FetchStates(country)
	if err != nil {
		return nil, err
	}
	if states == nil {
		// Distinguish "fetched, none exist" from "not fetched yet" so a country
		// with no regions is not re-requested on every browse.
		states = []State{}
	}

	p.mu.Lock()
	p.states = states
	p.mu.Unlock()
	return states, nil
}

func countryName(countries []Country, code string) string {
	i := slices.IndexFunc(countries, func(c Country) bool { return c.Code == code })
	if i < 0 {
		return ""
	}
	return countries[i].Name
}

// stationTracks converts directory entries into playable stream tracks.
func stationTracks(stations []CatalogStation) []playlist.Track {
	tracks := make([]playlist.Track, 0, len(stations))
	for _, s := range stations {
		tracks = append(tracks, playlist.Track{
			Path:     s.URL,
			Title:    formatCatalogName(s),
			Stream:   true,
			Realtime: true,
		})
	}
	return tracks
}

// placesLocked returns the places shown in the pane's Countries section: the
// listener's own country first, then pinned places in the order they were
// pinned. p.mu must be held.
func (p *Provider) placesLocked() []Place {
	var places []Place
	if home := p.homeLocked(); home.Code != "" {
		places = append(places, home)
	}
	for _, pin := range p.pins.Places() {
		if !slices.ContainsFunc(places, func(c Place) bool { return c.ID() == pin.ID() }) {
			places = append(places, pin)
		}
	}
	return places
}

// Refresh drops the cached country and tag indexes and the loaded catalog page
// so the next browse and the next catalog page come from the directory again.
func (p *Provider) Refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.countries = nil
	p.states = nil
	p.tags = nil
	p.tagGeneration++
	p.catalog = nil
}
