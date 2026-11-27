// Package radiometa pulls now-playing metadata for radio stations that do not
// carry inline ICY StreamTitle metadata and instead publish the current track
// (or show) via a separate JSON API. It currently covers NTS and FIP.
//
// Resolver matches a stream URL to a fetch function; the player polls it and
// feeds titles through the same path as ICY metadata, so no display code needs
// to know where the title came from.
package radiometa

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

var client = httpclient.NewAPI(8 * time.Second)

// Resolver reports how to fetch now-playing metadata for streamURL, or ok=false
// when the URL is not a recognized broadcaster. It satisfies
// player.StreamMetadataResolver.
func Resolver(streamURL string) (fetch func(ctx context.Context) (string, error), interval time.Duration, ok bool) {
	switch {
	case matchNTS(streamURL):
		channel := ntsChannel(streamURL)
		return func(ctx context.Context) (string, error) {
			return ntsNowPlaying(ctx, channel)
		}, 30 * time.Second, true
	case matchFIP(streamURL):
		station := fipStationID(streamURL)
		return func(ctx context.Context) (string, error) {
			return fipNowPlaying(ctx, station, time.Now().Unix())
		}, 15 * time.Second, true
	}
	return nil, 0, false
}

// parseStreamURL parses an HTTP or HTTPS stream URL and returns its lowercased
// hostname and non-empty lowercased path segments.
func parseStreamURL(raw string) (host string, segments []string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Opaque != "" {
		return "", nil, false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", nil, false
	}
	host = strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" || strings.HasPrefix(host, ".") || strings.Contains(host, "..") {
		return "", nil, false
	}
	for seg := range strings.SplitSeq(strings.ToLower(u.Path), "/") {
		if seg != "" {
			segments = append(segments, seg)
		}
	}
	return host, segments, true
}

// hasHostSuffix reports whether host equals domain or is a subdomain of domain.
func hasHostSuffix(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// hasPathSegment reports whether segments contains any target value.
func hasPathSegment(segments []string, targets ...string) bool {
	for _, seg := range segments {
		if slices.Contains(targets, seg) {
			return true
		}
	}
	return false
}

// maxMetaBody limits a now-playing response. The documents are a few KB.
const maxMetaBody = 1 << 20

// getJSON performs one GET under ctx and decodes the response body into v. A
// body over maxMetaBody returns httpclient.ErrTooLarge.
func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", httpclient.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return httpclient.ReadJSON(resp.Body, maxMetaBody, v)
}

// --- NTS -------------------------------------------------------------------

// matchNTS reports whether rawURL is an NTS Radio stream. The directory entry
// points at ntslive.net, which redirects to radiomast.io.
func matchNTS(rawURL string) bool {
	host, segments, ok := parseStreamURL(rawURL)
	if !ok {
		return false
	}
	switch {
	case hasHostSuffix(host, "ntslive.net"):
		return hasPathSegment(segments, "stream", "stream1", "stream2", "nts1", "nts2")
	case hasHostSuffix(host, "radiomast.io"):
		return hasPathSegment(segments, "nts1", "nts2")
	default:
		return false
	}
}

// ntsChannel returns the NTS channel encoded in the stream URL path.
func ntsChannel(rawURL string) string {
	_, segments, ok := parseStreamURL(rawURL)
	if ok && hasPathSegment(segments, "stream2", "nts2") {
		return "2"
	}
	return "1"
}

type ntsLive struct {
	Results []struct {
		Channel string `json:"channel_name"`
		Now     struct {
			BroadcastTitle string `json:"broadcast_title"`
			Embeds         struct {
				Details struct {
					Name string `json:"name"`
				} `json:"details"`
			} `json:"embeds"`
		} `json:"now"`
	} `json:"results"`
}

func ntsNowPlaying(ctx context.Context, channel string) (string, error) {
	var live ntsLive
	if err := getJSON(ctx, "https://www.nts.live/api/v2/live", &live); err != nil {
		return "", err
	}
	return parseNTS(&live, channel), nil
}

// parseNTS returns the current show title for the given channel. NTS is live DJ
// radio with no per-track tagging, so the show/broadcast name is the best
// available now-playing string. Prefers the nicely-cased details name.
func parseNTS(live *ntsLive, channel string) string {
	for _, r := range live.Results {
		if r.Channel != channel {
			continue
		}
		if name := strings.TrimSpace(r.Now.Embeds.Details.Name); name != "" {
			return name
		}
		return strings.TrimSpace(r.Now.BroadcastTitle)
	}
	return ""
}

// --- FIP (Radio France) ----------------------------------------------------

// fipStations maps a FIP path token to the Radio France livemeta station ID.
var fipStations = map[string]int{
	"fip":           7,
	"fipjazz":       65,
	"fipgroove":     66,
	"fiprock":       64,
	"fipreggae":     71,
	"fipelectro":    74,
	"fipmetal":      77,
	"fipnouveau":    70,
	"fipnouveautes": 70,
	"fipworld":      69,
	"fipmonde":      69,
}

// fipSegmentStem returns the station token before the first hyphen, underscore, or dot.
func fipSegmentStem(seg string) string {
	if i := strings.IndexAny(seg, "-_."); i >= 0 {
		return seg[:i]
	}
	return seg
}

// fipStationForPath returns the Radio France livemeta station ID matched by a
// path segment. Specific sub-channels take precedence over the main channel.
func fipStationForPath(segments []string) (int, bool) {
	matchedMain := false
	for _, seg := range segments {
		id, ok := fipStations[fipSegmentStem(seg)]
		if !ok {
			continue
		}
		if id != 7 {
			return id, true
		}
		matchedMain = true
	}
	if matchedMain {
		return 7, true
	}
	return 0, false
}

// matchFIP reports whether rawURL is a Radio France FIP stream.
func matchFIP(rawURL string) bool {
	host, segments, ok := parseStreamURL(rawURL)
	if !ok || !hasHostSuffix(host, "radiofrance.fr") {
		return false
	}
	_, ok = fipStationForPath(segments)
	return ok
}

// fipStationID returns the livemeta station ID for a FIP stream URL.
// It defaults to 7 when no sub-channel segment matches.
func fipStationID(rawURL string) int {
	_, segments, ok := parseStreamURL(rawURL)
	if !ok {
		return 7
	}
	if id, ok := fipStationForPath(segments); ok {
		return id
	}
	return 7
}

type fipStep struct {
	Title   string `json:"title"`
	Authors string `json:"authors"`
	Start   int64  `json:"start"`
	End     int64  `json:"end"`
}

type fipLive struct {
	Steps map[string]fipStep `json:"steps"`
}

func fipNowPlaying(ctx context.Context, station int, now int64) (string, error) {
	var live fipLive
	url := fmt.Sprintf("https://api.radiofrance.fr/livemeta/pull/%d", station)
	if err := getJSON(ctx, url, &live); err != nil {
		return "", err
	}
	return parseFIP(&live, now), nil
}

// betterFIPStep reports whether step a wins over step b.
// A later Start wins. An earlier End breaks Start ties, and the map key breaks End ties.
func betterFIPStep(ka string, a fipStep, kb string, b fipStep) bool {
	if a.Start != b.Start {
		return a.Start > b.Start
	}
	if a.End != b.End {
		return a.End < b.End
	}
	return ka < kb
}

// parseFIP returns the formatted track for the step playing at now.
// It inspects every step whose window contains now and selects the step with
// the latest Start. When no window contains now, it falls back to the most
// recent started step.
func parseFIP(live *fipLive, now int64) string {
	var (
		hasActive   bool
		activeKey   string
		active      fipStep
		hasFallback bool
		fallbackKey string
		fallback    fipStep
	)
	for k, s := range live.Steps {
		if s.Start <= now && now < s.End {
			if !hasActive || betterFIPStep(k, s, activeKey, active) {
				hasActive, activeKey, active = true, k, s
			}
			continue
		}
		if s.Start <= now {
			if !hasFallback || betterFIPStep(k, s, fallbackKey, fallback) {
				hasFallback, fallbackKey, fallback = true, k, s
			}
		}
	}
	if hasActive {
		return formatTrack(active.Authors, active.Title)
	}
	if hasFallback {
		return formatTrack(fallback.Authors, fallback.Title)
	}
	return ""
}

// formatTrack joins artist and title as "Artist - Title", matching the ICY
// StreamTitle convention the UI splits on. Falls back gracefully when either
// field is empty.
func formatTrack(artist, title string) string {
	artist = strings.TrimSpace(artist)
	title = strings.TrimSpace(title)
	switch {
	case artist != "" && title != "":
		return artist + " - " + title
	case title != "":
		return title
	default:
		return artist
	}
}
