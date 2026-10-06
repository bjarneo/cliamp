package radio

import (
	"strings"
	"testing"
)

// Until the listener answers, the Countries section leads with the offer and
// no country of theirs appears anywhere.
func TestPlaylistsOffersLocationBeforeItIsAnswered(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TZ", "Europe/Oslo")
	p := New(Options{})

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if lists[0].ID != locationConsentID || lists[0].Name != "Use my location" {
		t.Fatalf("first row = %+v, want the location offer", lists[0])
	}
	for _, list := range lists {
		if strings.HasPrefix(list.ID, "p:") {
			t.Errorf("a country row appeared before the question was answered: %+v", list)
		}
	}
	if p.LocationConsentID() != locationConsentID {
		t.Errorf("LocationConsentID() = %q, want the offer row", p.LocationConsentID())
	}
}

// Once answered, the offer is gone whichever way it was answered.
func TestPlaylistsDropsTheOfferOnceAnswered(t *testing.T) {
	for _, country := range []string{"NO", CountryDeclined} {
		t.Run(country, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			p := New(Options{Country: country})

			lists, err := p.Playlists()
			if err != nil {
				t.Fatalf("Playlists: %v", err)
			}
			for _, list := range lists {
				if list.ID == locationConsentID {
					t.Fatalf("the offer is still shown after answering %q", country)
				}
			}
			if p.LocationConsentID() != "" {
				t.Errorf("LocationConsentID() = %q, want empty", p.LocationConsentID())
			}
		})
	}
}

// The offer row is a question; asking for its tracks is a caller error.
func TestTracksRejectsTheLocationRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := New(Options{})
	if _, err := p.Tracks(locationConsentID); err == nil {
		t.Error("Tracks should refuse the location row")
	}
}

func TestPlaylistsListsPlacesBeforeStations(t *testing.T) {
	d := &directory{}
	d.serve(t)

	p := newPlaceProvider(t, "NO")
	if _, err := p.pins.Toggle(Place{Code: "DE", Name: "Germany"}); err != nil {
		t.Fatalf("pin Germany: %v", err)
	}

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(lists) < 3 {
		t.Fatalf("lists = %+v, want two places and the built-in station", lists)
	}
	// The home country leads and says why it is there; pins follow, starred.
	if lists[0].ID != "p:NO" || lists[0].Name != "Norway (near you)" {
		t.Errorf("first row = %+v, want the home country", lists[0])
	}
	if lists[1].ID != "p:DE" || lists[1].Name != "★ Germany" {
		t.Errorf("second row = %+v, want the pinned country", lists[1])
	}
	if lists[2].ID != "l:0" || lists[2].Name != builtinName {
		t.Errorf("third row = %+v, want the built-in station", lists[2])
	}
}

func TestPlaylistsWithoutAHomeCountryOrPinsIsUnchanged(t *testing.T) {
	p := newPlaceProvider(t, "")
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(lists) != 1 || lists[0].ID != "l:0" {
		t.Fatalf("lists = %+v, want only the built-in station", lists)
	}
}

func TestTracksExpandsAPlaceIntoItsStations(t *testing.T) {
	d := &directory{stations: []CatalogStation{
		{Name: "NRK P1", URL: "https://nrk.example/p1"},
		{Name: "NRK P3", URL: "https://nrk.example/p3"},
	}}
	d.serve(t)

	p := newPlaceProvider(t, "NO")
	tracks, err := p.Tracks("p:NO")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("tracks = %+v, want both of the country's stations", tracks)
	}
	if d.lastQuery.Get("countrycode") != "NO" {
		t.Errorf("query = %v, want it narrowed to NO", d.lastQuery)
	}

	if _, err := p.Tracks("p:XX"); err == nil {
		t.Error("Tracks should reject an unknown place")
	}
}

// A place row keeps its ID when another pin goes away, as favorite rows do. A
// selection or a track request for the row then still names the same place.
func TestPlaceIDsSurviveAPinChange(t *testing.T) {
	d := &directory{}
	d.serve(t)

	p := newPlaceProvider(t, "NO")
	germany := Place{Code: "DE", Name: "Germany"}
	oslo := Place{Code: "NO", Name: "Oslo, Norway", State: "Oslo"}
	for _, place := range []Place{germany, oslo} {
		if _, err := p.pins.Toggle(place); err != nil {
			t.Fatalf("pin %s: %v", place.ID(), err)
		}
	}
	rowIDs := func() map[string]string {
		t.Helper()
		lists, err := p.Playlists()
		if err != nil {
			t.Fatalf("Playlists: %v", err)
		}
		ids := make(map[string]string)
		for _, list := range lists {
			if strings.HasPrefix(list.ID, "p:") {
				ids[list.Name] = list.ID
			}
		}
		return ids
	}
	before := rowIDs()

	if _, err := p.pins.Toggle(germany); err != nil {
		t.Fatalf("unpin Germany: %v", err)
	}
	after := rowIDs()
	for _, name := range []string{"Norway (near you)", "★ Oslo, Norway"} {
		if before[name] == "" || after[name] != before[name] {
			t.Errorf("row %q: ID %q before the unpin, %q after, want the same ID", name, before[name], after[name])
		}
	}

	if _, err := p.Tracks(before["★ Oslo, Norway"]); err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if d.lastQuery.Get("countrycode") != "NO" || d.lastQuery.Get("state") != "Oslo" {
		t.Errorf("query = %v, want it narrowed to Oslo, NO", d.lastQuery)
	}
}

func TestSectionTitle(t *testing.T) {
	p := newPlaceProvider(t, "")
	for prefix, want := range map[string]string{
		"p":      "Countries",
		"browse": "Countries",
		"loc":    "Countries",
		"l":      "Stations",
		"f":      "Favorites",
		"c":      "Catalog",
		"s":      "Search Results",
		"":       "",
		"zzz":    "",
	} {
		if got := p.SectionTitle(prefix); got != want {
			t.Errorf("SectionTitle(%q) = %q, want %q", prefix, got, want)
		}
	}
}

func TestRefreshDropsCachedDirectoryData(t *testing.T) {
	d := &directory{countries: []Country{{Name: "Norway", Code: "NO", StationCount: 247}}}
	d.serve(t)

	p := newPlaceProvider(t, "")
	if _, err := p.Genres(); err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if len(p.countries) != 1 {
		t.Fatalf("countries = %+v, want the index cached", p.countries)
	}
	p.AppendCatalog([]CatalogStation{{Name: "Stale", URL: "https://stale.example/stream"}})

	p.Refresh()
	if p.countries != nil || p.catalog != nil {
		t.Errorf("after Refresh: countries = %+v, catalog = %+v, want both dropped", p.countries, p.catalog)
	}
}

func TestIsFavoritableIDExcludesPlaces(t *testing.T) {
	p := newPlaceProvider(t, "")
	for id, want := range map[string]bool{
		"c:0": true, "f:https://radio.example/live": true, "s:0": true,
		"p:NO": false, "l:0": false, "browse:countries": false,
	} {
		if got := p.IsFavoritableID(id); got != want {
			t.Errorf("IsFavoritableID(%q) = %v, want %v", id, got, want)
		}
	}
}
