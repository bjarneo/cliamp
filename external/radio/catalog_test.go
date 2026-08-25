package radio

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestSearchStationsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/stations/byname/") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if !strings.Contains(r.URL.Path, "jazz") {
			t.Errorf("path should contain query 'jazz': %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"Jazz FM","url_resolved":"https://jazz.example/stream","country":"UK","bitrate":128}]`))
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	stations, err := SearchStations("jazz", 10)
	if err != nil {
		t.Fatalf("SearchStations: %v", err)
	}
	if len(stations) != 1 {
		t.Fatalf("got %d stations, want 1", len(stations))
	}
	if stations[0].Name != "Jazz FM" {
		t.Errorf("Name = %q, want Jazz FM", stations[0].Name)
	}
	if stations[0].URL != "https://jazz.example/stream" {
		t.Errorf("URL = %q", stations[0].URL)
	}
	if stations[0].Bitrate != 128 {
		t.Errorf("Bitrate = %d, want 128", stations[0].Bitrate)
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

func TestSearchStationsDefaultLimit(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	if _, err := SearchStations("x", 0); err != nil {
		t.Fatalf("SearchStations: %v", err)
	}
	if !strings.Contains(gotURL, "limit=50") {
		t.Errorf("default limit should be 50, got URL %q", gotURL)
	}
}

func TestSearchStationsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	_, err := SearchStations("jazz", 10)
	if err == nil {
		t.Error("SearchStations should return error on 500")
	}
}

func TestTopStationsOffset(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"Top1","url_resolved":"http://t1/","bitrate":64}]`))
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	stations, err := TopStationsOffset(50, 25)
	if err != nil {
		t.Fatalf("TopStationsOffset: %v", err)
	}
	if len(stations) != 1 || stations[0].Name != "Top1" {
		t.Fatalf("stations = %+v", stations)
	}
	if !strings.Contains(gotURL, "topvote/25") {
		t.Errorf("URL %q should use limit 25", gotURL)
	}
	if !strings.Contains(gotURL, "offset=50") {
		t.Errorf("URL %q should contain offset=50", gotURL)
	}
}

func TestTopStationsOffsetClampsNegatives(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	if _, err := TopStationsOffset(-10, 0); err != nil {
		t.Fatalf("TopStationsOffset: %v", err)
	}
	if !strings.Contains(gotURL, "offset=0") {
		t.Errorf("URL %q should clamp negative offset to 0", gotURL)
	}
	if !strings.Contains(gotURL, "topvote/50") {
		t.Errorf("URL %q should use default limit 50", gotURL)
	}
}

func TestFetchStationsInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid`))
	}))
	defer srv.Close()
	installCatalogClient(t, srv.URL)

	_, err := SearchStations("x", 10)
	if err == nil {
		t.Error("SearchStations should error on invalid JSON")
	}
}
