package resolve

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"cliamp/playlist"
)

var (
	xiaoyuzhouSchemaScriptRe = regexp.MustCompile(`(?s)<script[^>]+name="schema:podcast-show"[^>]*>(.*?)</script>`)
	isoDurationRe            = regexp.MustCompile(`^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?$`)
)

func resolveXiaoyuzhouEpisode(pageURL string) ([]playlist.Track, error) {
	resp, err := httpClient.Get(pageURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading page: %w", err)
	}

	track, err := parseXiaoyuzhouEpisodeHTML(pageURL, string(body))
	if err != nil {
		return nil, err
	}
	return []playlist.Track{track}, nil
}

func parseXiaoyuzhouEpisodeHTML(pageURL, doc string) (playlist.Track, error) {
	audioURL := extractMetaContent(doc, "property", "og:audio")
	title := extractMetaContent(doc, "property", "og:title")

	var schema struct {
		Name            string `json:"name"`
		TimeRequired    string `json:"timeRequired"`
		AssociatedMedia struct {
			ContentURL string `json:"contentUrl"`
		} `json:"associatedMedia"`
		PartOfSeries struct {
			Name string `json:"name"`
		} `json:"partOfSeries"`
	}
	if raw := extractXiaoyuzhouSchemaJSON(doc); raw != "" {
		_ = json.Unmarshal([]byte(raw), &schema)
	}

	if audioURL == "" {
		audioURL = schema.AssociatedMedia.ContentURL
	}
	if audioURL == "" {
		return playlist.Track{}, fmt.Errorf("audio URL not found")
	}
	if title == "" {
		title = schema.Name
	}
	if title == "" {
		title = pageURL
	}

	return playlist.Track{
		Path:         audioURL,
		Title:        title,
		Artist:       schema.PartOfSeries.Name,
		Stream:       true,
		DurationSecs: parseISODurationSeconds(schema.TimeRequired),
	}, nil
}

func extractXiaoyuzhouSchemaJSON(doc string) string {
	m := xiaoyuzhouSchemaScriptRe.FindStringSubmatch(doc)
	if len(m) < 2 {
		return ""
	}
	return html.UnescapeString(strings.TrimSpace(m[1]))
}

func extractMetaContent(doc, attr, value string) string {
	qv := regexp.QuoteMeta(value)
	// Match both <meta property="og:x" content="..."> and <meta content="..." property="og:x">
	patterns := []string{
		fmt.Sprintf(`<meta[^>]+%s="%s"[^>]+content="([^"]+)"`, attr, qv),
		fmt.Sprintf(`<meta[^>]+content="([^"]+)"[^>]+%s="%s"`, attr, qv),
	}
	for _, p := range patterns {
		if m := regexp.MustCompile(p).FindStringSubmatch(doc); len(m) >= 2 {
			return html.UnescapeString(m[1])
		}
	}
	return ""
}

func parseISODurationSeconds(raw string) int {
	m := isoDurationRe.FindStringSubmatch(strings.TrimSpace(raw))
	if len(m) == 0 {
		return 0
	}
	var total int
	if m[1] != "" {
		if hours, err := strconv.Atoi(m[1]); err == nil {
			total += hours * 3600
		}
	}
	if m[2] != "" {
		if minutes, err := strconv.Atoi(m[2]); err == nil {
			total += minutes * 60
		}
	}
	if m[3] != "" {
		if seconds, err := strconv.Atoi(m[3]); err == nil {
			total += seconds
		}
	}
	return total
}
