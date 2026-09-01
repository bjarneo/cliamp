package radio

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type hostRewriter struct {
	target *url.URL
	rt     http.RoundTripper
}

func (h hostRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = h.target.Scheme
	clone.URL.Host = h.target.Host
	clone.Host = h.target.Host
	return h.rt.RoundTrip(clone)
}

func installCatalogClient(t *testing.T, serverURL string) {
	t.Helper()
	old := catalogClient
	catalogClient = testHTTPClient(t, serverURL)
	t.Cleanup(func() { catalogClient = old })
}

func installStatsClient(t *testing.T, serverURL string) {
	t.Helper()
	old := statsClient
	statsClient = testHTTPClient(t, serverURL)
	t.Cleanup(func() { statsClient = old })
}

func testHTTPClient(t *testing.T, serverURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: hostRewriter{target: u, rt: http.DefaultTransport},
	}
}

func TestStationsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/stations/search" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "jazz" {
			t.Errorf("name = %q, want jazz", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"Jazz FM","url_resolved":"https://jazz.example/stream","country":"UK","countrycode":"GB","bitrate":128}]`))
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	stations, err := Stations(StationQuery{Name: "jazz", Limit: 10})
	if err != nil {
		t.Fatalf("Stations: %v", err)
	}
	if len(stations) != 1 {
		t.Fatalf("got %d stations, want 1", len(stations))
	}
	got := stations[0]
	if got.Name != "Jazz FM" || got.URL != "https://jazz.example/stream" {
		t.Errorf("station = %+v", got)
	}
	if got.Bitrate != 128 || got.CountryCode != "GB" {
		t.Errorf("station = %+v, want bitrate 128 and country code GB", got)
	}
}

func TestFetchStats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/statistics" {
			t.Errorf("path = %q, want /statistics", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"total_sessions":42,
			"total_listen_hours":12.5,
			"peak_listeners":7,
			"stations":{"lofi":{"total_sessions":40,"active_listeners":3}}
		}`))
	}))
	defer srv.Close()
	installStatsClient(t, srv.URL)

	stats, err := FetchStats()
	if err != nil {
		t.Fatalf("FetchStats: %v", err)
	}
	if stats.TotalSessions != 42 || stats.TotalListenHours != 12.5 || stats.PeakListeners != 7 {
		t.Fatalf("stats = %+v, want aggregate values", stats)
	}
	if station := stats.Stations["lofi"]; station.TotalSessions != 40 || station.ActiveListeners != 3 {
		t.Fatalf("lofi stats = %+v, want decoded station values", station)
	}
}

func TestStationsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	if _, err := Stations(StationQuery{Name: "jazz"}); err == nil {
		t.Error("Stations should return an error on 500")
	}
}

func TestStationsInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid`))
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	if _, err := Stations(StationQuery{Name: "x"}); err == nil {
		t.Error("Stations should error on invalid JSON")
	}
}
