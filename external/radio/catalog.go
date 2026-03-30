// catalog.go provides a client for the Radio Browser API (radio-browser.info),
// a free community-maintained directory of internet radio stations.
package radio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const radioBrowserBase = "https://de1.api.radio-browser.info/json"

// CatalogStation represents a station from the Radio Browser API.
type CatalogStation struct {
	Name     string `json:"name"`
	URL      string `json:"url_resolved"`
	Country  string `json:"country"`
	Tags     string `json:"tags"`
	Codec    string `json:"codec"`
	Bitrate  int    `json:"bitrate"`
	Votes    int    `json:"votes"`
	Homepage string `json:"homepage"`
}

var catalogClient = &http.Client{Timeout: 10 * time.Second}

// SearchStations searches the Radio Browser API by station name.
func SearchStations(query string, limit int) ([]CatalogStation, error) {
	if limit <= 0 {
		limit = 50
	}
	u := fmt.Sprintf("%s/stations/byname/%s?limit=%d&order=votes&reverse=true&hidebroken=true",
		radioBrowserBase, url.PathEscape(query), limit)
	return fetchStations(u)
}

// TopStationsOffset returns a page of the most-voted stations starting at offset.
func TopStationsOffset(offset, limit int) ([]CatalogStation, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	u := fmt.Sprintf("%s/stations/topvote/%d?offset=%d&hidebroken=true",
		radioBrowserBase, limit, offset)
	return fetchStations(u)
}

func fetchStations(u string) ([]CatalogStation, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cliamp/1.0")

	resp, err := catalogClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radio-browser: HTTP %d", resp.StatusCode)
	}

	var stations []CatalogStation
	if err := json.NewDecoder(resp.Body).Decode(&stations); err != nil {
		return nil, err
	}
	return stations, nil
}
