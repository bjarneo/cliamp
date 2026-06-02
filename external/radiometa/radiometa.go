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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 8 * time.Second}

const userAgent = "cliamp/1.0 (https://github.com/bjarneo/cliamp)"

// Resolver reports how to fetch now-playing metadata for streamURL, or ok=false
// when the URL is not a recognized broadcaster. It satisfies
// player.StreamMetadataResolver.
func Resolver(streamURL string) (fetch func(ctx context.Context) (string, error), interval time.Duration, ok bool) {
	u := strings.ToLower(streamURL)
	switch {
	case matchNTS(u):
		channel := ntsChannel(u)
		return func(ctx context.Context) (string, error) {
			return ntsNowPlaying(ctx, channel)
		}, 30 * time.Second, true
	case matchFIP(u):
		station := fipStationID(u)
		return func(ctx context.Context) (string, error) {
			return fipNowPlaying(ctx, station, time.Now().Unix())
		}, 15 * time.Second, true
	}
	return nil, 0, false
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// --- NTS -------------------------------------------------------------------

// matchNTS reports whether url is an NTS Radio stream. The directory entry
// points at ntslive.net (which 302-redirects to radiomast), so match either.
func matchNTS(url string) bool {
	return strings.Contains(url, "ntslive.net") ||
		strings.Contains(url, "radiomast.io/nts") ||
		strings.Contains(url, "/nts1") || strings.Contains(url, "/nts2")
}

// ntsChannel returns the NTS channel ("1" or "2") encoded in the stream URL.
// The channel-2 stream is "stream2" (ntslive) or "nts2" (radiomast).
func ntsChannel(url string) string {
	if strings.Contains(url, "stream2") || strings.Contains(url, "nts2") {
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

// fipStations maps a URL slug fragment to the Radio France livemeta station ID.
// Order matters: specific genres are checked before the plain FIP default.
var fipStations = []struct {
	slug string
	id   int
}{
	{"fipjazz", 65},
	{"fipgroove", 66},
	{"fiprock", 64},
	{"fipreggae", 71},
	{"fipelectro", 74},
	{"fipmetal", 77},
	{"fipnouveau", 70}, // fipnouveautes
	{"fipworld", 69},
	{"fipmonde", 69},
}

// matchFIP reports whether url is a Radio France FIP stream.
func matchFIP(url string) bool {
	return strings.Contains(url, "radiofrance.fr") && strings.Contains(url, "fip")
}

// fipStationID returns the livemeta station ID for a FIP stream URL, defaulting
// to 7 (the main FIP channel) when no sub-channel slug matches.
func fipStationID(url string) int {
	for _, s := range fipStations {
		if strings.Contains(url, s.slug) {
			return s.id
		}
	}
	return 7
}

type fipLive struct {
	Steps map[string]struct {
		Title   string `json:"title"`
		Authors string `json:"authors"`
		Start   int64  `json:"start"`
		End     int64  `json:"end"`
	} `json:"steps"`
}

func fipNowPlaying(ctx context.Context, station int, now int64) (string, error) {
	var live fipLive
	url := fmt.Sprintf("https://api.radiofrance.fr/livemeta/pull/%d", station)
	if err := getJSON(ctx, url, &live); err != nil {
		return "", err
	}
	return parseFIP(&live, now), nil
}

// parseFIP returns "Artist - Title" for the step playing at now. It picks the
// step whose [start,end) window contains now; if none does (clock skew, gaps),
// it falls back to the most recent step that has already started.
func parseFIP(live *fipLive, now int64) string {
	var bestStart int64 = -1
	var artist, title string
	for _, s := range live.Steps {
		if s.Start <= now && now < s.End {
			return formatTrack(s.Authors, s.Title)
		}
		if s.Start <= now && s.Start > bestStart {
			bestStart = s.Start
			artist, title = s.Authors, s.Title
		}
	}
	return formatTrack(artist, title)
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
