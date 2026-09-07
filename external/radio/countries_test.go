package radio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// directory is a stand-in Radio Browser server that records the query it was
// asked, so tests can assert on how a request was narrowed.
type directory struct {
	countries []Country
	states    []State
	tags      []Tag
	stations  []CatalogStation
	lastPath  string
	lastQuery url.Values
}

func (d *directory) serve(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.lastPath, d.lastQuery = r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")

		var body any
		switch {
		case strings.HasSuffix(r.URL.Path, "/countries"):
			body = d.countries
		case strings.HasSuffix(r.URL.Path, "/tags"):
			body = d.tags
		case strings.Contains(r.URL.Path, "/states/"):
			body = d.states
		default:
			body = d.stations
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	installCatalogClient(t, srv.URL)
}

// newPlaceProvider builds a provider whose location question is already
// answered: with a country when home is set, declined when it is empty. Tests
// that want the unanswered state say so explicitly.
func newPlaceProvider(t *testing.T, home string) *Provider {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if home == "" {
		home = CountryDeclined
	}
	return New(Options{Country: home})
}

func TestFetchCountriesMergesDuplicatesAndSortsByStationCount(t *testing.T) {
	d := &directory{countries: []Country{
		{Name: "Norway", Code: "NO", StationCount: 247},
		{Name: "The United States Of America", Code: "US", StationCount: 7974},
		{Name: "The United States Of America", Code: "us", StationCount: 1},
		{Name: "", Code: "XX", StationCount: 1},
		{Name: "Nowhere", Code: "", StationCount: 5},
	}}
	d.serve(t)

	countries, err := FetchCountries()
	if err != nil {
		t.Fatalf("FetchCountries: %v", err)
	}
	if len(countries) != 2 {
		t.Fatalf("countries = %+v, want US and NO only", countries)
	}
	// The lowercase duplicate folds into the uppercase entry, counts added.
	if countries[0].Code != "US" || countries[0].StationCount != 7975 {
		t.Errorf("first country = %+v, want US with 7975 stations", countries[0])
	}
	if countries[0].Name != "United States Of America" {
		t.Errorf("name = %q, want the article trimmed", countries[0].Name)
	}
	if countries[1].Code != "NO" {
		t.Errorf("second country = %+v, want NO", countries[1])
	}
}

func TestFetchStatesDropsThePlaceholderBucket(t *testing.T) {
	d := &directory{states: []State{
		{Name: "- None -", Country: "Norway", StationCount: 1},
		{Name: "Innlandet", Country: "Norway", StationCount: 16},
		{Name: "oslo", Country: "Norway", StationCount: 11},
		{Name: "Oslo", Country: "Norway", StationCount: 4},
		{Name: "Møre og Romsdal", Country: "Norway", StationCount: 3},
		{Name: "  ", Country: "Norway", StationCount: 3},
		{Name: "Empty", Country: "Norway", StationCount: 0},
	}}
	d.serve(t)

	states, err := FetchStates("Norway")
	if err != nil {
		t.Fatalf("FetchStates: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("states = %+v, want Innlandet, Oslo and Møre og Romsdal", states)
	}
	// Sorted by station count: Innlandet 16, then Oslo 11+4.
	if states[0].Name != "Innlandet" {
		t.Errorf("states = %+v, want most stations first", states)
	}
	// "oslo" and "Oslo" are one region; the API matches them case-insensitively.
	if states[1].Name != "Oslo" || states[1].StationCount != 15 {
		t.Errorf("second state = %+v, want Oslo with both spellings counted", states[1])
	}
	// A local spelling that already carries capitals is left alone.
	if states[2].Name != "Møre og Romsdal" {
		t.Errorf("third state = %+v, want its original spelling", states[2])
	}
	if !strings.Contains(d.lastPath, "/states/Norway/") {
		t.Errorf("path = %q, want the country name escaped into it", d.lastPath)
	}
}

func TestFetchStatesWithoutACountryMakesNoRequest(t *testing.T) {
	d := &directory{}
	d.serve(t)
	states, err := FetchStates("  ")
	if err != nil || states != nil {
		t.Fatalf("FetchStates(\"\") = (%+v, %v), want (nil, nil)", states, err)
	}
	if d.lastPath != "" {
		t.Errorf("requested %q, want no request at all", d.lastPath)
	}
}

func TestStationQueryValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		query  StationQuery
		want   map[string]string
		absent []string
	}{
		{
			name:   "defaults",
			query:  StationQuery{},
			want:   map[string]string{"limit": "50", "offset": "0", "order": SortVotes, "reverse": "true", "hidebroken": "true"},
			absent: []string{"name", "tag", "tagExact", "countrycode", "state"},
		},
		{
			name:  "country and region are matched exactly",
			query: StationQuery{CountryCode: "no", State: "Oslo", Limit: 25, Offset: 50},
			want: map[string]string{
				"countrycode": "NO", "state": "Oslo", "stateExact": "true",
				"limit": "25", "offset": "50",
			},
		},
		{
			name:  "tag is matched exactly",
			query: StationQuery{Tag: "smooth jazz"},
			want:  map[string]string{"tag": "smooth jazz", "tagExact": "true"},
		},
		{
			name:   "names sort ascending",
			query:  StationQuery{Order: SortName},
			want:   map[string]string{"order": SortName},
			absent: []string{"reverse"},
		},
		{
			name:   "random needs no direction",
			query:  StationQuery{Order: SortRandom},
			absent: []string{"reverse"},
		},
		{
			name:   "an unusable country code is dropped, not sent",
			query:  StationQuery{CountryCode: "XX"},
			absent: []string{"countrycode"},
		},
		{
			name:  "a negative offset is clamped",
			query: StationQuery{Offset: -10},
			want:  map[string]string{"offset": "0"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.query.values()
			for key, want := range tc.want {
				if got := v.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			for _, key := range tc.absent {
				if v.Has(key) {
					t.Errorf("%s = %q, want it absent", key, v.Get(key))
				}
			}
		})
	}
}

func TestGenresListsCountriesGroupedByRegion(t *testing.T) {
	d := &directory{countries: []Country{
		{Name: "Germany", Code: "DE", StationCount: 6233},
		{Name: "Norway", Code: "NO", StationCount: 247},
		{Name: "Japan", Code: "JP", StationCount: 300},
		{Name: "Ghost", Code: "GH", StationCount: 0},
	}}
	d.serve(t)

	p := newPlaceProvider(t, "")
	genres, err := p.Genres()
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if len(genres) != 3 {
		t.Fatalf("genres = %+v, want the three countries with stations", genres)
	}
	if genres[0].ID != "DE" || genres[0].Name != "Germany (6233)" {
		t.Errorf("first genre = %+v, want Germany with its station count", genres[0])
	}
	// Sorted by station count: Germany, Japan, Norway.
	if genres[1].ID != "JP" || genres[1].Group != "Asia" {
		t.Errorf("second genre = %+v, want Japan in Asia", genres[1])
	}
	if genres[2].ID != "NO" || genres[2].Group != "Europe" {
		t.Errorf("third genre = %+v, want Norway in Europe", genres[2])
	}
	for _, g := range genres {
		if g.Favorite {
			t.Errorf("%s is marked pinned with an empty pin store", g.ID)
		}
	}
}

func TestGenresPutsTheHomeCountrysRegionsFirst(t *testing.T) {
	d := &directory{
		countries: []Country{
			{Name: "Germany", Code: "DE", StationCount: 6233},
			{Name: "Norway", Code: "NO", StationCount: 247},
		},
		states: []State{{Name: "Oslo", Country: "Norway", StationCount: 11}},
	}
	d.serve(t)

	p := newPlaceProvider(t, "NO")
	genres, err := p.Genres()
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if len(genres) != 3 {
		t.Fatalf("genres = %+v, want Oslo plus the two countries", genres)
	}
	// The listener's own regions lead, grouped under their country's name so
	// they read as one block.
	if genres[0].ID != "NO/Oslo" || genres[0].Group != "Norway" {
		t.Errorf("first genre = %+v, want Oslo grouped under Norway", genres[0])
	}
	if genres[0].Name != "Oslo (11)" {
		t.Errorf("name = %q, want the station count", genres[0].Name)
	}
	if genres[1].ID != "DE" {
		t.Errorf("countries should follow the home regions, got %+v", genres[1])
	}
}

func TestGenresSurvivesAStateLookupFailure(t *testing.T) {
	// Regions are a convenience; losing them must not cost the country list.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/states/") {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"Norway","iso_3166_1":"NO","stationcount":247}]`))
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	p := newPlaceProvider(t, "NO")
	genres, err := p.Genres()
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	if len(genres) != 1 || genres[0].ID != "NO" {
		t.Fatalf("genres = %+v, want just the country", genres)
	}
}

func TestGenreTracksQueriesOnePlace(t *testing.T) {
	d := &directory{stations: []CatalogStation{
		{Name: "NRK P3", URL: "https://nrk.example/p3", Country: "Norway", Bitrate: 192},
		{Name: "Hostile", URL: "ssh://attacker.example/payload"},
	}}
	d.serve(t)

	p := newPlaceProvider(t, "")
	tracks, err := p.GenreTracks("NO/Oslo", SortClicks)
	if err != nil {
		t.Fatalf("GenreTracks: %v", err)
	}
	if d.lastQuery.Get("countrycode") != "NO" || d.lastQuery.Get("state") != "Oslo" {
		t.Errorf("query = %v, want it narrowed to NO/Oslo", d.lastQuery)
	}
	if d.lastQuery.Get("order") != SortClicks {
		t.Errorf("order = %q, want %q", d.lastQuery.Get("order"), SortClicks)
	}
	// The non-HTTP entry is a command-execution risk, not a station.
	if len(tracks) != 1 {
		t.Fatalf("tracks = %+v, want only the streamable station", tracks)
	}
	if !tracks[0].Stream || !tracks[0].Realtime {
		t.Errorf("track = %+v, want a realtime stream", tracks[0])
	}
	if tracks[0].Title != "NRK P3 [192k] · Norway" {
		t.Errorf("title = %q, want the station's bitrate and country", tracks[0].Title)
	}
}

func TestGenreTracksRejectsAnUnknownPlace(t *testing.T) {
	p := newPlaceProvider(t, "")
	if _, err := p.GenreTracks("browse:countries", SortVotes); err == nil {
		t.Error("GenreTracks should reject an ID that is not a place")
	}
}

func TestToggleGenreFavoriteNamesThePlaceFromTheIndex(t *testing.T) {
	d := &directory{countries: []Country{{Name: "Norway", Code: "NO", StationCount: 247}}}
	d.serve(t)

	p := newPlaceProvider(t, "")
	if _, err := p.Genres(); err != nil { // warms the country index
		t.Fatalf("Genres: %v", err)
	}

	pinned, err := p.ToggleGenreFavorite("NO/Oslo")
	if err != nil || !pinned {
		t.Fatalf("ToggleGenreFavorite = (%v, %v), want pinned", pinned, err)
	}
	places := p.pins.Places()
	if len(places) != 1 || places[0].Name != "Oslo, Norway" {
		t.Fatalf("pins = %+v, want the region named with its country", places)
	}

	if pinned, err := p.ToggleGenreFavorite("NO/Oslo"); err != nil || pinned {
		t.Fatalf("second toggle = (%v, %v), want unpinned", pinned, err)
	}
}

func TestGenreSortTypesAreAcceptedByTheQueryBuilder(t *testing.T) {
	p := newPlaceProvider(t, "")
	valid := []string{SortVotes, SortClicks, SortTrend, SortName, SortRandom}
	for _, sort := range p.GenreSortTypes() {
		if !slices.Contains(valid, sort.ID) {
			t.Errorf("sort %+v is not an order the directory accepts", sort)
		}
		if sort.Label == "" {
			t.Errorf("sort %+v has no label", sort)
		}
	}
}
