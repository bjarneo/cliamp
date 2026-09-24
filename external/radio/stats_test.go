package radio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const statsFixture = `{
  "total_sessions": 783681, "total_listen_hours": 102930.3, "peak_listeners": 280,
  "stations": {
    "edm": {
      "total_sessions": 105215, "total_listen_hours": 10164.6, "peak_listeners": 43, "active_listeners": 7,
      "active_listener_countries": [
        {"country": "United States", "country_code": "US", "sessions": 3, "listen_hours": 3},
        {"country": "Kenya", "country_code": "KE", "sessions": 1, "listen_hours": 0.2},
        {"country": "Germany", "country_code": "DE", "sessions": 3, "listen_hours": 1}
      ],
      "top_countries": [
        {"country": "United States", "country_code": "US", "sessions": 30000, "listen_hours": 3000},
        {"country": "Germany", "country_code": "DE", "sessions": 9000, "listen_hours": 900}
      ],
      "top_cities": [{"city": "Berlin", "country_code": "DE", "sessions": 400, "listen_hours": 40}],
      "daily": [
        {"date": "2026-09-08", "sessions": 190, "listen_hours": 11.8},
        {"date": "2026-09-07", "sessions": 114, "listen_hours": 6}
      ]
    },
    "amiga": {
      "total_sessions": 132, "total_listen_hours": 6, "peak_listeners": 4, "active_listeners": 1,
      "active_listener_countries": [
        {"country": "Denmark", "country_code": "DK", "sessions": 1, "listen_hours": 0.1}
      ],
      "top_countries": [
        {"country": "United States", "country_code": "US", "sessions": 29, "listen_hours": 1.5},
        {"country": "Unknown", "country_code": "", "sessions": 5, "listen_hours": 0}
      ],
      "daily": [
        {"date": "2026-09-08", "sessions": 107, "listen_hours": 5.4},
        {"date": "2026-08-01", "sessions": 1, "listen_hours": 0.1}
      ]
    },
    "lofi": {
      "total_sessions": 500000, "total_listen_hours": 70000, "peak_listeners": 120, "active_listeners": 0,
      "active_listener_countries": [], "top_countries": [], "daily": []
    }
  }
}`

func TestFetchStatistics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "cliamp/") {
			t.Errorf("User-Agent = %q", ua)
		}
		w.Write([]byte(statsFixture))
	}))
	defer srv.Close()

	stats, raw, err := fetchStatistics(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != statsFixture {
		t.Error("raw body was not passed through verbatim")
	}
	if stats.PeakListeners != 280 || len(stats.Stations) != 3 {
		t.Errorf("decoded peak=%d stations=%d", stats.PeakListeners, len(stats.Stations))
	}
	if got := stats.Stations["edm"].TopCities[0].City; got != "Berlin" {
		t.Errorf("top city = %q", got)
	}
}

func TestFetchStatisticsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	if _, _, err := fetchStatistics(context.Background(), srv.Client(), srv.URL); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v, want HTTP 502", err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv2.Close()
	if _, _, err := fetchStatistics(context.Background(), srv2.Client(), srv2.URL); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v, want decode error", err)
	}
}

func TestSummarize(t *testing.T) {
	var stats Statistics
	if err := json.Unmarshal([]byte(statsFixture), &stats); err != nil {
		t.Fatal(err)
	}
	sum := stats.Summarize(map[string]string{"edm": "EDM", "ncs-dnb": "NCS Drum & Bass"})

	if sum.Listeners != 8 {
		t.Errorf("Listeners = %d, want 8", sum.Listeners)
	}
	if sum.Peak != 280 || sum.Sessions != 783681 || sum.Hours != 102930.3 {
		t.Errorf("totals = %d %d %v", sum.Peak, sum.Sessions, sum.Hours)
	}

	wantCountries := []CountryCount{
		{"DE", "Germany", 3}, {"US", "United States", 3}, {"DK", "Denmark", 1}, {"KE", "Kenya", 1},
	}
	if len(sum.Countries) != len(wantCountries) {
		t.Fatalf("countries = %+v", sum.Countries)
	}
	for i, want := range wantCountries {
		if sum.Countries[i] != want {
			t.Errorf("countries[%d] = %+v, want %+v", i, sum.Countries[i], want)
		}
	}
	// All-time sessions are summed across channels; the unknown country is dropped.
	wantAllTime := []CountryCount{{"US", "United States", 30029}, {"DE", "Germany", 9000}}
	if len(sum.AllTime) != len(wantAllTime) {
		t.Fatalf("all-time = %+v", sum.AllTime)
	}
	for i, want := range wantAllTime {
		if sum.AllTime[i] != want {
			t.Errorf("all-time[%d] = %+v, want %+v", i, sum.AllTime[i], want)
		}
	}

	if len(sum.Channels) != 3 {
		t.Fatalf("channels = %+v", sum.Channels)
	}
	if c := sum.Channels[0]; c.Slug != "edm" || c.Name != "EDM" || c.Listeners != 7 || c.Peak != 43 {
		t.Errorf("busiest = %+v", c)
	}
	if c := sum.Channels[1]; c.Slug != "amiga" || c.Name != "amiga" {
		t.Errorf("second channel = %+v, want amiga falling back to its slug", c)
	}
	if c := sum.Channels[2]; c.Slug != "lofi" {
		t.Errorf("idle channel should sort last, got %+v", c)
	}

	wantDaily := []DailyPoint{{"2026-08-01", 0.1}, {"2026-09-07", 6}, {"2026-09-08", 17.2}}
	if len(sum.Daily) != len(wantDaily) {
		t.Fatalf("daily = %+v", sum.Daily)
	}
	for i, want := range wantDaily {
		got := sum.Daily[i]
		if got.Date != want.Date || got.Hours-want.Hours > 1e-9 || want.Hours-got.Hours > 1e-9 {
			t.Errorf("daily[%d] = %+v, want %+v", i, got, want)
		}
	}
}

func TestSummarizeNobodyListening(t *testing.T) {
	var stats Statistics
	if err := json.Unmarshal([]byte(statsFixture), &stats); err != nil {
		t.Fatal(err)
	}
	for slug, st := range stats.Stations {
		st.ActiveListeners = 0
		st.ActiveListenerCountries = nil
		stats.Stations[slug] = st
	}
	sum := stats.Summarize(nil)
	if sum.Listeners != 0 || len(sum.Countries) != 0 {
		t.Errorf("Listeners=%d Countries=%+v, want idle", sum.Listeners, sum.Countries)
	}
	if len(sum.AllTime) != 2 || sum.AllTime[0].Code != "US" {
		t.Errorf("all-time = %+v", sum.AllTime)
	}
	if sum.Channels[0].Slug != "lofi" {
		t.Errorf("with nobody listening the busiest channel is by sessions, got %+v", sum.Channels[0])
	}
}

func TestSummarizeDailyWindow(t *testing.T) {
	st := StationStats{}
	for d := 1; d <= 40; d++ {
		st.Daily = append(st.Daily, DailyStats{Date: fmt.Sprintf("2026-07-%02d", d), ListenHours: float64(d)})
	}
	sum := Statistics{Stations: map[string]StationStats{"x": st}}.Summarize(nil)
	if len(sum.Daily) != DailyWindow {
		t.Fatalf("kept %d days, want %d", len(sum.Daily), DailyWindow)
	}
	if sum.Daily[0].Date != "2026-07-10" || sum.Daily[DailyWindow-1].Date != "2026-07-40" {
		t.Errorf("window = %s .. %s", sum.Daily[0].Date, sum.Daily[DailyWindow-1].Date)
	}
}
