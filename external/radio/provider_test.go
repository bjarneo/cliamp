package radio

import (
	"path/filepath"
	"strings"
	"testing"
)

// newTestProvider builds a provider with the location question already
// answered, so these tests see the station rows and not the offer row.
func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	// Point HOME at a temp dir so New() doesn't touch real state.
	t.Setenv("HOME", t.TempDir())
	return New(Options{Country: CountryDeclined})
}

func TestProviderNewHasBuiltinStation(t *testing.T) {
	p := newTestProvider(t)
	if p.Name() != "Radio" {
		t.Errorf("Name() = %q, want Radio", p.Name())
	}
	infos, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(infos) == 0 {
		t.Fatal("Playlists() returned none — expected built-in cliamp radio")
	}
	if infos[0].Name != builtinName {
		t.Errorf("first playlist = %q, want %q", infos[0].Name, builtinName)
	}
	if !strings.HasPrefix(infos[0].ID, "l:") {
		t.Errorf("first playlist ID = %q, want to start with 'l:'", infos[0].ID)
	}
}

func TestProviderLoadsStationsFromTOML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create radios.toml with one extra station.
	cfgDir := filepath.Join(home, ".config", "cliamp")
	writeFile(t, filepath.Join(cfgDir, "radios.toml"), `[[station]]
name = "Extra"
url = "https://extra.example/stream"
`)

	p := New(Options{Country: CountryDeclined})
	infos, _ := p.Playlists()
	if len(infos) < 2 {
		t.Fatalf("expected builtin + extra, got %d", len(infos))
	}
	if infos[1].Name != "Extra" {
		t.Errorf("second playlist = %q, want Extra", infos[1].Name)
	}
}

func TestProviderTracksLocalStation(t *testing.T) {
	p := newTestProvider(t)
	infos, _ := p.Playlists()
	tracks, err := p.Tracks(infos[0].ID)
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	if tracks[0].Path != builtinURL {
		t.Errorf("Path = %q, want %q", tracks[0].Path, builtinURL)
	}
	if !tracks[0].Stream || !tracks[0].Realtime {
		t.Errorf("Stream/Realtime = %v/%v, want true/true", tracks[0].Stream, tracks[0].Realtime)
	}
}

func TestProviderTracksInvalidID(t *testing.T) {
	p := newTestProvider(t)
	tests := []string{
		"l:99",
		"c:0", // catalog empty
		"s:0", // search not active
		"f:5",
		"x:1",
		"notanid",
		"l:notanumber",
	}
	for _, id := range tests {
		t.Run(id, func(t *testing.T) {
			if _, err := p.Tracks(id); err == nil {
				t.Errorf("Tracks(%q) = nil err, want error", id)
			}
		})
	}
}

func TestProviderCatalogLifecycle(t *testing.T) {
	p := newTestProvider(t)

	p.AppendCatalog([]CatalogStation{
		{Name: "Radio A", URL: "http://a/", Bitrate: 192, Country: "NO"},
		{Name: "Radio B", URL: "http://b/"},
	})

	infos, _ := p.Playlists()
	// builtin (1) + catalog (2) = 3
	if len(infos) != 3 {
		t.Fatalf("len(infos) = %d, want 3 (builtin + 2 catalog)", len(infos))
	}
	if !strings.Contains(infos[1].ID, "c:0") {
		t.Errorf("catalog id[0] = %q, want c:0", infos[1].ID)
	}
	p.AppendCatalog([]CatalogStation{{Name: "Radio A duplicate", URL: "http://a/"}})
	infos, _ = p.Playlists()
	if len(infos) != 3 {
		t.Fatalf("duplicate catalog station changed len to %d", len(infos))
	}

	// Tracks for catalog entry.
	tracks, err := p.Tracks("c:0")
	if err != nil {
		t.Fatalf("Tracks(c:0): %v", err)
	}
	if tracks[0].Path != "http://a/" {
		t.Errorf("Path = %q, want http://a/", tracks[0].Path)
	}

	// ToggleFavorite on a catalog entry should add it.
	added, name, err := p.ToggleFavorite("c:0")
	if err != nil {
		t.Fatalf("ToggleFavorite: %v", err)
	}
	if !added || name != "Radio A" {
		t.Errorf("ToggleFavorite = (%v, %q), want (true, Radio A)", added, name)
	}
	// Toggle again removes.
	added, _, err = p.ToggleFavorite("c:0")
	if err != nil {
		t.Fatalf("ToggleFavorite 2: %v", err)
	}
	if added {
		t.Error("second ToggleFavorite should remove")
	}
}

func TestProviderToggleFavoriteLocalRejected(t *testing.T) {
	p := newTestProvider(t)
	_, _, err := p.ToggleFavorite("l:0")
	if err == nil {
		t.Error("ToggleFavorite on local station should error")
	}
}

func TestProviderToggleFavoriteInvalidIdx(t *testing.T) {
	p := newTestProvider(t)
	_, _, err := p.ToggleFavorite("c:99")
	if err == nil {
		t.Error("ToggleFavorite on out-of-range catalog idx should error")
	}
}

func TestProviderSearchLifecycle(t *testing.T) {
	p := newTestProvider(t)

	if p.IsSearching() {
		t.Error("fresh provider should not be searching")
	}

	p.SetSearchResults([]CatalogStation{
		{Name: "Hit", URL: "http://hit/"},
	})
	if !p.IsSearching() {
		t.Error("SetSearchResults should put provider into searching mode")
	}

	infos, _ := p.Playlists()
	if len(infos) != 1 || !strings.HasPrefix(infos[0].ID, "s:") {
		t.Fatalf("Playlists during search = %+v, want single s:0 entry", infos)
	}

	// Tracks for a search result.
	tracks, err := p.Tracks("s:0")
	if err != nil {
		t.Fatalf("Tracks(s:0): %v", err)
	}
	if tracks[0].Path != "http://hit/" {
		t.Errorf("Path = %q, want http://hit/", tracks[0].Path)
	}

	p.ClearSearch()
	if p.IsSearching() {
		t.Error("ClearSearch should leave searching = false")
	}
}

func TestIsCatalogOrFavID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"c:0", true},
		{"f:3", true},
		{"s:1", true},
		{"l:0", false},
		{"123", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsCatalogOrFavID(tt.id); got != tt.want {
			t.Errorf("IsCatalogOrFavID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestProviderIDPrefix(t *testing.T) {
	p := newTestProvider(t)
	tests := []struct {
		id   string
		want string
	}{
		{"c:0", "c"},
		{"l:5", "l"},
		{"f:3", "f"},
		{"s:0", "s"},
		{"noprefix", ""},
	}
	for _, tt := range tests {
		if got := p.IDPrefix(tt.id); got != tt.want {
			t.Errorf("IDPrefix(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestProviderIsFavoritableID(t *testing.T) {
	p := newTestProvider(t)
	if !p.IsFavoritableID("c:0") {
		t.Error("c:0 should be favoritable")
	}
	if p.IsFavoritableID("l:0") {
		t.Error("l:0 should not be favoritable")
	}
}

func TestParseStationIDLegacyNumeric(t *testing.T) {
	prefix, idx, err := parseStationID("5")
	if err != nil {
		t.Fatalf("parseStationID(5): %v", err)
	}
	if prefix != "l" || idx != 5 {
		t.Errorf("parseStationID(5) = (%q, %d), want (l, 5)", prefix, idx)
	}
}

func TestParseStationIDError(t *testing.T) {
	_, _, err := parseStationID("c:notnumber")
	if err == nil {
		t.Error("parseStationID('c:notnumber') should error")
	}
}

func TestFormatCatalogName(t *testing.T) {
	tests := []struct {
		in   CatalogStation
		want string
	}{
		{CatalogStation{Name: "Jazz"}, "Jazz"},
		{CatalogStation{Name: "Jazz", Bitrate: 128}, "Jazz [128k]"},
		{CatalogStation{Name: "Jazz", Country: "UK"}, "Jazz · UK"},
		{CatalogStation{Name: "Jazz", Bitrate: 320, Country: "US"}, "Jazz [320k] · US"},
	}
	for _, tt := range tests {
		if got := formatCatalogName(tt.in); got != tt.want {
			t.Errorf("formatCatalogName(%+v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLoadStationsIgnoresMissingFile(t *testing.T) {
	stations, err := loadStations(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Errorf("loadStations on missing file should not error, got %v", err)
	}
	if stations != nil {
		t.Errorf("missing file should return nil stations, got %+v", stations)
	}
}

func TestLoadStationsParsesMultiple(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "radios.toml")
	writeFile(t, path, `# leading comment
[[station]]
name = "A"
url = "http://a/"

[[station]]
name = "B"
url = "http://b/"
# trailing comment
`)
	stations, err := loadStations(path)
	if err != nil {
		t.Fatalf("loadStations: %v", err)
	}
	if len(stations) != 2 {
		t.Fatalf("len = %d, want 2", len(stations))
	}
	if stations[0].name != "A" || stations[1].name != "B" {
		t.Errorf("names = %+v", stations)
	}
}

func TestLoadStationsSkipsIncompleteEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "radios.toml")
	writeFile(t, path, `[[station]]
name = "no-url"

[[station]]
name = "complete"
url = "http://c/"
`)
	stations, err := loadStations(path)
	if err != nil {
		t.Fatalf("loadStations: %v", err)
	}
	if len(stations) != 1 || stations[0].name != "complete" {
		t.Errorf("expected only 'complete', got %+v", stations)
	}
}
