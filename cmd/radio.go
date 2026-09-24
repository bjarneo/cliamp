// radio.go implements `cliamp radio --stats`: who is listening to the cliamp
// radio channels, from radio.cliamp.stream. The animated globe variant lives
// in radio_globe.go.
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/resolve"
)

// statsTimeout bounds one round of statistics requests.
const statsTimeout = 20 * time.Second

// reportCountries caps the country table in the text report.
const reportCountries = 15

// RadioStats prints who is listening to the cliamp radio channels. With
// jsonOutput the upstream statistics document is printed as-is, indented.
func RadioStats(ctx context.Context, w io.Writer, jsonOutput bool) error {
	ctx, cancel := context.WithTimeout(ctx, statsTimeout)
	defer cancel()
	names := make(chan map[string]string, 1)
	if !jsonOutput {
		go func() { names <- channelNames() }()
	}
	stats, raw, err := radio.FetchStatistics(ctx)
	if err != nil {
		return err
	}
	if jsonOutput {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			return fmt.Errorf("radio statistics: %w", err)
		}
		buf.WriteByte('\n')
		_, err = w.Write(buf.Bytes())
		return err
	}
	_, err = io.WriteString(w, statsReport(stats.Summarize(<-names)))
	return err
}

// channelNames maps channel slugs to display names by reading the channel
// list the radio provider plays from, so statistics keyed by slug can say
// "NCS Drum & Bass" rather than "ncs-dnb". Slugs are readable on their own,
// so a failed lookup just leaves the map empty.
func channelNames() map[string]string {
	tracks, err := resolve.URL(radio.BuiltinURL)
	if err != nil {
		return nil
	}
	names := make(map[string]string, len(tracks))
	for _, t := range tracks {
		if slug := channelSlug(t.Path); slug != "" && t.Title != "" {
			names[slug] = t.Title
		}
	}
	return names
}

// channelSlug is the first path segment of a stream URL:
// https://radio.cliamp.stream/ncs-dnb/stream gives "ncs-dnb".
func channelSlug(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	slug, _, _ := strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
	return slug
}

// countryRows picks the country list to show: listeners now, or all-time
// sessions when nobody is connected, so the views are never empty.
func countryRows(s radio.Summary) (rows []radio.CountryCount, live bool) {
	if len(s.Countries) > 0 {
		return s.Countries, true
	}
	return s.AllTime, false
}

// statsReport lays the summary out as plain text for a terminal or a script.
func statsReport(s radio.Summary) string {
	var b strings.Builder
	if s.Listeners > 0 {
		b.WriteString("cliamp radio · who's listening right now\n\n")
	} else {
		b.WriteString("cliamp radio · nobody is tuned in right now\n\n")
	}
	busiest := "–"
	if len(s.Channels) > 0 {
		c := s.Channels[0]
		if c.Listeners > 0 {
			busiest = fmt.Sprintf("%s (%s listening)", c.Name, commas(c.Listeners))
		} else {
			busiest = fmt.Sprintf("%s (all-time)", c.Name)
		}
	}
	fmt.Fprintf(&b, "  %-16s %10s\n", "listening now", commas(s.Listeners))
	fmt.Fprintf(&b, "  %-16s %10s\n", "countries", commas(len(s.Countries)))
	fmt.Fprintf(&b, "  %-16s %s\n", "busiest channel", busiest)
	fmt.Fprintf(&b, "  %-16s %10s\n", "all-time high", commas(s.Peak))
	fmt.Fprintf(&b, "  %-16s %10s\n", "sessions", commas(s.Sessions))
	fmt.Fprintf(&b, "  %-16s %10s\n", "hours streamed", hours(s.Hours))

	if len(s.Channels) > 0 {
		fmt.Fprintf(&b, "\n%-18s %5s %6s %11s %10s\n", "CHANNEL", "NOW", "PEAK", "SESSIONS", "HOURS")
		for _, c := range s.Channels {
			fmt.Fprintf(&b, "%-18s %5s %6s %11s %10s\n", ansi.Truncate(c.Name, 18, "…"), commas(c.Listeners), commas(c.Peak), commas(c.Sessions), hours(c.Hours))
		}
	}
	if rows, live := countryRows(s); len(rows) > 0 {
		unit := "LISTENING"
		if !live {
			unit = "SESSIONS · ALL-TIME"
		}
		fmt.Fprintf(&b, "\n%-30s %19s\n", "COUNTRY", unit)
		for _, c := range rows[:min(len(rows), reportCountries)] {
			fmt.Fprintf(&b, "%-30s %19s\n", ansi.Truncate(c.Name, 30, "…"), commas(c.Count))
		}
	}
	return b.String()
}

// commas formats an integer with thousands separators: 783681 -> "783,681".
func commas(n int) string {
	if n < 0 {
		return "-" + commas(-n)
	}
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// hours formats listening hours: one decimal below a hundred, whole hours
// with separators above.
func hours(h float64) string {
	if h < 100 {
		return strconv.FormatFloat(h, 'f', 1, 64)
	}
	return commas(int(h + 0.5))
}
