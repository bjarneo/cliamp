// catalog.go provides a client for the Radio Browser API (radio-browser.info),
// a free community-maintained directory of internet radio stations.
package radio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/provider"
)

const radioBrowserBase = "https://de1.api.radio-browser.info/json"
const cliampRadioStatsURL = "https://radio.cliamp.stream/statistics"

// CatalogStation represents a station from the Radio Browser API.
type CatalogStation struct {
	Name        string `json:"name"`
	URL         string `json:"url_resolved"`
	Country     string `json:"country"`
	CountryCode string `json:"countrycode"`
	State       string `json:"state"`
	Tags        string `json:"tags"`
	Codec       string `json:"codec"`
	Bitrate     int    `json:"bitrate"`
	Votes       int    `json:"votes"`
	Homepage    string `json:"homepage"`
}

// Country is one entry of the directory's country index.
type Country struct {
	Name         string `json:"name"`
	Code         string `json:"iso_3166_1"`
	StationCount int    `json:"stationcount"`
}

// State is a region within a country. Only about 44% of stations carry one, so
// a country's state list covers a fraction of its stations.
type State struct {
	Name         string `json:"name"`
	Country      string `json:"country"`
	StationCount int    `json:"stationcount"`
}

var catalogClient = &http.Client{Timeout: 10 * time.Second}
var statsClient = &http.Client{Timeout: 10 * time.Second}

// RadioStats returns listener statistics for cliamp radio's built-in stations.
func (*Provider) RadioStats() (provider.RadioStats, error) {
	return FetchStats()
}

// FetchStats fetches listener statistics for cliamp radio's built-in stations.
func FetchStats() (provider.RadioStats, error) {
	var stats provider.RadioStats
	if err := getJSON(statsClient, cliampRadioStatsURL, &stats); err != nil {
		return provider.RadioStats{}, fmt.Errorf("cliamp radio stats: %w", err)
	}
	return stats, nil
}

// StationQuery narrows a station listing. The zero value lists everything.
//
// Location is expressed as a country code and optional state rather than a
// radius: only about 12% of directory entries carry coordinates at all, and
// the API ignores every distance ordering, so a "stations near me" search
// would silently hide most of the catalog and return it unsorted. The country
// code, by contrast, is present on ~99% of entries.
type StationQuery struct {
	Name        string // free-text station name
	CountryCode string // ISO 3166-1 alpha-2
	State       string // region within CountryCode
	Order       string // one of SortVotes, SortClicks, … ; defaults to SortVotes
	Offset      int
	Limit       int
}

// Station listing orders accepted by the directory's search endpoint.
const (
	SortVotes  = "votes"
	SortClicks = "clickcount"
	SortTrend  = "clicktrend"
	SortName   = "name"
	SortRandom = "random"
)

// values renders the query as request parameters. Name, countrycode, and state
// are matched exactly where the API allows it, so that filtering on "Georgia"
// cannot also pull in "South Georgia".
func (q StationQuery) values() url.Values {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	order := q.Order
	if order == "" {
		order = SortVotes
	}
	v := url.Values{}
	v.Set("limit", strconv.Itoa(limit))
	v.Set("offset", strconv.Itoa(max(0, q.Offset)))
	v.Set("order", order)
	v.Set("hidebroken", "true")
	// Ascending reads naturally for names and means nothing for random;
	// every other order wants the biggest numbers first.
	if order != SortName && order != SortRandom {
		v.Set("reverse", "true")
	}
	if name := strings.TrimSpace(q.Name); name != "" {
		v.Set("name", name)
	}
	if code := normalizeCountryCode(q.CountryCode); code != "" {
		v.Set("countrycode", code)
	}
	if state := strings.TrimSpace(q.State); state != "" {
		v.Set("state", state)
		v.Set("stateExact", "true")
	}
	return v
}

// Stations runs a station query against the directory. The /stations/search
// endpoint subsumes the directory's /topvote and /byname shortcuts: with no
// name and no country it returns the same top-voted feed, in the same order.
func Stations(q StationQuery) ([]CatalogStation, error) {
	var stations []CatalogStation
	if err := fetchJSON(radioBrowserBase+"/stations/search?"+q.values().Encode(), &stations); err != nil {
		return nil, err
	}
	return stations, nil
}

// FetchCountries returns the directory's country index, most stations first.
// Entries without a usable ISO code are dropped, and the directory's handful
// of lowercase duplicates ("de" beside "DE") are folded into one another.
func FetchCountries() ([]Country, error) {
	var raw []Country
	if err := fetchJSON(radioBrowserBase+"/countries", &raw); err != nil {
		return nil, err
	}

	byCode := make(map[string]*Country, len(raw))
	merged := make([]Country, 0, len(raw))
	for _, c := range raw {
		code := normalizeCountryCode(c.Code)
		if code == "" {
			continue
		}
		if existing, ok := byCode[code]; ok {
			existing.StationCount += c.StationCount
			continue
		}
		merged = append(merged, Country{
			Name:         displayCountryName(c.Name),
			Code:         code,
			StationCount: c.StationCount,
		})
		byCode[code] = &merged[len(merged)-1]
	}
	slices.SortFunc(merged, func(a, b Country) int {
		if a.StationCount != b.StationCount {
			return b.StationCount - a.StationCount
		}
		return strings.Compare(a.Name, b.Name)
	})
	return merged, nil
}

// FetchStates returns the regions the directory knows for a country, named by
// its full country name (not its code), most stations first. The directory's
// "- None -" bucket and its inconsistent casing are cleaned up here.
func FetchStates(country string) ([]State, error) {
	country = strings.TrimSpace(country)
	if country == "" {
		return nil, nil
	}
	var raw []State
	if err := fetchJSON(radioBrowserBase+"/states/"+url.PathEscape(country)+"/", &raw); err != nil {
		return nil, err
	}

	// State names are submitter-entered, so the same region turns up as both
	// "oslo" and "Oslo". The API matches them case-insensitively, so folding
	// the two into one row loses nothing and spares the listener a duplicate.
	states := make([]State, 0, len(raw))
	byName := make(map[string]int, len(raw))
	for _, s := range raw {
		name := strings.TrimSpace(s.Name)
		if name == "" || name == "- None -" || s.StationCount <= 0 {
			continue
		}
		key := strings.ToLower(name)
		if i, seen := byName[key]; seen {
			states[i].StationCount += s.StationCount
			continue
		}
		byName[key] = len(states)
		states = append(states, State{
			Name:         displayStateName(name),
			Country:      country,
			StationCount: s.StationCount,
		})
	}
	slices.SortFunc(states, func(a, b State) int {
		if a.StationCount != b.StationCount {
			return b.StationCount - a.StationCount
		}
		return strings.Compare(a.Name, b.Name)
	})
	return states, nil
}

// fetchJSON reads a JSON document from the Radio Browser API.
func fetchJSON(u string, out any) error {
	if err := getJSON(catalogClient, u, out); err != nil {
		return fmt.Errorf("radio-browser: %w", err)
	}
	return nil
}

// getJSON performs one GET and decodes the response body. Callers wrap the
// error with the name of the service they were talking to.
func getJSON(client *http.Client, u string, out any) error {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cliamp/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
