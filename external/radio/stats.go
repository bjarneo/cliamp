package radio

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// StatsURL serves live and all-time listener statistics for the cliamp radio
// channels: the document cliamp.stream renders its "who's listening" section
// from. Countries come from the server's own view of its listeners; cliamp
// sends nothing about where it is running.
const StatsURL = "https://radio.cliamp.stream/statistics"

// maxStatsBody bounds the statistics document; it is tens of kilobytes.
const maxStatsBody = 8 << 20

// Statistics is the document served by StatsURL.
type Statistics struct {
	TotalSessions    int                     `json:"total_sessions"`
	TotalListenHours float64                 `json:"total_listen_hours"`
	PeakListeners    int                     `json:"peak_listeners"`
	Stations         map[string]StationStats `json:"stations"`
}

// StationStats is one channel's share of Statistics, keyed upstream by the
// channel slug ("edm", "ncs-dnb").
type StationStats struct {
	TotalSessions           int            `json:"total_sessions"`
	TotalListenHours        float64        `json:"total_listen_hours"`
	PeakListeners           int            `json:"peak_listeners"`
	ActiveListeners         int            `json:"active_listeners"`
	ActiveListenerCountries []CountryStats `json:"active_listener_countries"`
	TopCountries            []CountryStats `json:"top_countries"`
	TopCities               []CityStats    `json:"top_cities"`
	Daily                   []DailyStats   `json:"daily"`
}

// CountryStats counts sessions from one country. In
// ActiveListenerCountries the sessions are the listeners connected now.
type CountryStats struct {
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Sessions    int     `json:"sessions"`
	ListenHours float64 `json:"listen_hours"`
}

// CityStats counts all-time sessions from one city.
type CityStats struct {
	City        string  `json:"city"`
	CountryCode string  `json:"country_code"`
	Sessions    int     `json:"sessions"`
	ListenHours float64 `json:"listen_hours"`
}

// DailyStats is one day's activity on one channel.
type DailyStats struct {
	Date        string  `json:"date"` // YYYY-MM-DD
	Sessions    int     `json:"sessions"`
	ListenHours float64 `json:"listen_hours"`
}

// FetchStatistics downloads the statistics document. The raw body comes back
// alongside the decoded form so callers can pass it through verbatim.
func FetchStatistics(ctx context.Context) (Statistics, []byte, error) {
	stats, raw, err := fetchStatistics(ctx, catalogClient, StatsURL)
	if err != nil {
		return Statistics{}, nil, fmt.Errorf("radio statistics: %w", err)
	}
	return stats, raw, nil
}

func fetchStatistics(ctx context.Context, client *http.Client, u string) (Statistics, []byte, error) {
	resp, err := get(ctx, client, u)
	if err != nil {
		return Statistics{}, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxStatsBody))
	if err != nil {
		return Statistics{}, nil, err
	}
	var stats Statistics
	if err := json.Unmarshal(raw, &stats); err != nil {
		return Statistics{}, nil, fmt.Errorf("decode: %w", err)
	}
	return stats, raw, nil
}

// Summary is Statistics boiled down to what the statistics views show.
type Summary struct {
	Listeners int              // listeners connected right now, all channels
	Countries []CountryCount   // listeners now per country, heaviest first; empty when nobody is connected
	AllTime   []CountryCount   // sessions per country over all time, heaviest first
	Channels  []ChannelSummary // busiest first
	Peak      int              // all-time high of concurrent listeners
	Sessions  int              // all-time sessions
	Hours     float64          // all-time listening hours
	Daily     []DailyPoint     // listening hours per day, oldest first, at most DailyWindow days
}

// CountryCount is one country's listeners now, or sessions all-time.
type CountryCount struct {
	Code  string
	Name  string
	Count int
}

// ChannelSummary is one channel's headline numbers. Channels sort by current
// listeners, then all-time sessions, so the first one is always the busiest.
type ChannelSummary struct {
	Slug      string
	Name      string
	Listeners int
	Peak      int
	Sessions  int
	Hours     float64
}

// DailyPoint is the listening hours across all channels on one day.
type DailyPoint struct {
	Date  string // YYYY-MM-DD
	Hours float64
}

// DailyWindow is how many trailing days Summary.Daily keeps.
const DailyWindow = 31

// Summarize aggregates the per-channel statistics. names maps channel slugs
// to display names; a missing entry falls back to the slug.
func (s Statistics) Summarize(names map[string]string) Summary {
	sum := Summary{Peak: s.PeakListeners, Sessions: s.TotalSessions, Hours: s.TotalListenHours}
	live := make(map[string]*CountryCount)
	allTime := make(map[string]*CountryCount)
	daily := make(map[string]float64)
	for slug, st := range s.Stations {
		sum.Listeners += st.ActiveListeners
		sum.Channels = append(sum.Channels, ChannelSummary{
			Slug: slug, Name: cmp.Or(names[slug], slug), Listeners: st.ActiveListeners,
			Peak: st.PeakListeners, Sessions: st.TotalSessions, Hours: st.TotalListenHours,
		})
		addCountries(live, st.ActiveListenerCountries)
		addCountries(allTime, st.TopCountries)
		for _, d := range st.Daily {
			daily[d.Date] += d.ListenHours
		}
	}
	slices.SortFunc(sum.Channels, func(a, b ChannelSummary) int {
		return cmp.Or(cmp.Compare(b.Listeners, a.Listeners), cmp.Compare(b.Sessions, a.Sessions), strings.Compare(a.Name, b.Name))
	})
	sum.Countries = sortedCountries(live)
	sum.AllTime = sortedCountries(allTime)
	dates := slices.Sorted(maps.Keys(daily))
	if len(dates) > DailyWindow {
		dates = dates[len(dates)-DailyWindow:]
	}
	for _, date := range dates {
		sum.Daily = append(sum.Daily, DailyPoint{Date: date, Hours: daily[date]})
	}
	return sum
}

func addCountries(into map[string]*CountryCount, rows []CountryStats) {
	for _, c := range rows {
		code := normalizeCountryCode(c.CountryCode)
		if code == "" {
			continue
		}
		cc := into[code]
		if cc == nil {
			cc = &CountryCount{Code: code, Name: c.Country}
			into[code] = cc
		}
		cc.Count += c.Sessions
	}
}

func sortedCountries(m map[string]*CountryCount) []CountryCount {
	out := make([]CountryCount, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b CountryCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.Name, b.Name))
	})
	return out
}
