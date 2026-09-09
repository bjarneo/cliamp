package podcast

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/appmeta"
)

type show struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	FeedURL      string `json:"feed_url"`
	Author       string `json:"author"`
	Artwork      string `json:"artwork"`
	Genre        string `json:"genre"`
	EpisodeCount int    `json:"episode_count"`
}

type category struct {
	ID   string
	Name string
}

var categories = []category{
	{"1489", "News"},
	{"1318", "Technology"},
	{"1488", "True Crime"},
	{"1303", "Comedy"},
	{"1324", "Society & Culture"},
	{"1487", "History"},
	{"1533", "Science"},
	{"1512", "Health & Fitness"},
	{"1321", "Business"},
	{"1310", "Music"},
	{"1545", "Sports"},
	{"1301", "Arts"},
	{"1304", "Education"},
	{"1483", "Fiction"},
	{"1309", "TV & Film"},
	{"1314", "Religion & Spirituality"},
	{"1305", "Kids & Family"},
	{"1502", "Leisure"},
	{"1511", "Government"},
}

type client struct {
	http         *http.Client
	directoryURL string
	chartsURL    string
}

func newClient() *client {
	return &client{
		http:         &http.Client{Timeout: 30 * time.Second},
		directoryURL: "https://itunes.apple.com",
		chartsURL:    "https://rss.marketingtools.apple.com/api/v2",
	}
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

func (c *client) search(ctx context.Context, term, genreID string) ([]show, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, nil
	}
	params := url.Values{
		"term":   {term},
		"media":  {"podcast"},
		"entity": {"podcast"},
		"limit":  {"100"},
	}
	if genreID = strings.TrimSpace(genreID); genreID != "" {
		params.Set("genreId", genreID)
	}
	shows, err := c.fetchShows(ctx, "search", params)
	if err != nil {
		return nil, err
	}
	return uniqueShows(shows), nil
}

func (c *client) top(ctx context.Context, country string) ([]show, error) {
	var chart struct {
		Feed struct {
			Results []*struct {
				ID string `json:"id"`
			} `json:"results"`
		} `json:"feed"`
	}
	endpoint := strings.TrimRight(c.chartsURL, "/") + "/" + strings.ToLower(country) + "/podcasts/top/100/podcasts.json"
	if err := c.getJSON(ctx, endpoint, &chart); err != nil {
		return nil, fmt.Errorf("podcast charts: %w", err)
	}
	if chart.Feed.Results == nil {
		return nil, fmt.Errorf("podcast charts: missing results array")
	}
	var ids []string
	for _, entry := range chart.Feed.Results {
		if entry == nil {
			return nil, fmt.Errorf("podcast charts: null result")
		}
		if id := strings.TrimSpace(entry.ID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	shows, err := c.fetchShows(ctx, "lookup", url.Values{
		"id":     {strings.Join(ids, ",")},
		"entity": {"podcast"},
	})
	if err != nil {
		return nil, err
	}
	found := make(map[string]show, len(shows))
	for _, s := range shows {
		found[s.ID] = s
	}
	var ranked []show
	for _, id := range ids {
		if s, ok := found[id]; ok {
			ranked = append(ranked, s)
		}
	}
	// Deduplicate after ranking: lookup order must not pick a lower-ranked show.
	return uniqueShows(ranked), nil
}

func (c *client) fetchShows(ctx context.Context, endpoint string, params url.Values) ([]show, error) {
	var response struct {
		Results []*struct {
			CollectionID     json.Number `json:"collectionId"`
			CollectionName   string      `json:"collectionName"`
			ArtistName       string      `json:"artistName"`
			FeedURL          string      `json:"feedUrl"`
			ArtworkURL600    string      `json:"artworkUrl600"`
			ArtworkURL100    string      `json:"artworkUrl100"`
			PrimaryGenreName string      `json:"primaryGenreName"`
			TrackCount       int         `json:"trackCount"`
		} `json:"results"`
	}
	rawURL := strings.TrimRight(c.directoryURL, "/") + "/" + endpoint + "?" + params.Encode()
	if err := c.getJSON(ctx, rawURL, &response); err != nil {
		return nil, fmt.Errorf("podcast %s: %w", endpoint, err)
	}
	if response.Results == nil {
		return nil, fmt.Errorf("podcast %s: missing results array", endpoint)
	}
	var shows []show
	for _, entry := range response.Results {
		if entry == nil {
			return nil, fmt.Errorf("podcast %s: null result", endpoint)
		}
		feedURL := strings.TrimSpace(entry.FeedURL)
		if !validHTTPURL(feedURL) {
			continue
		}
		title := strings.TrimSpace(entry.CollectionName)
		if title == "" {
			title = "Untitled show"
		}
		artwork := strings.TrimSpace(entry.ArtworkURL600)
		if !validHTTPURL(artwork) {
			artwork = strings.TrimSpace(entry.ArtworkURL100)
		}
		if !validHTTPURL(artwork) {
			artwork = ""
		}
		shows = append(shows, show{
			ID:           entry.CollectionID.String(),
			Title:        title,
			FeedURL:      feedURL,
			Author:       strings.TrimSpace(entry.ArtistName),
			Artwork:      artwork,
			Genre:        strings.TrimSpace(entry.PrimaryGenreName),
			EpisodeCount: entry.TrackCount,
		})
	}
	return shows, nil
}

func uniqueShows(shows []show) []show {
	seen := make(map[string]bool, len(shows))
	var unique []show
	for _, s := range shows {
		if !seen[s.FeedURL] {
			seen[s.FeedURL] = true
			unique = append(unique, s)
		}
	}
	return unique
}

func (c *client) getJSON(ctx context.Context, rawURL string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", appmeta.ClientName()+"/"+appmeta.Version())
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP status %s", resp.Status)
	}
	const maxBody = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxBody {
		return fmt.Errorf("response exceeds %d bytes", maxBody)
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
