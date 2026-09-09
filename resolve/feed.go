package resolve

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"golang.org/x/net/html/charset"
)

const (
	maxFeedBody     = 32 << 20
	maxFeedEpisodes = 300
	itunesNS        = "http://www.itunes.com/dtds/podcast-1.0.dtd"
)

// Feed fetches a publisher RSS feed directly, including extensionless URLs.
// It accepts only absolute HTTP(S) URLs without userinfo, including redirects,
// and uses the shared 30-second client with caller-controlled cancellation.
// It reads at most 32 MiB and returns the first 300 playable episodes in feed
// order. Parsing stops at that limit, leaving the remaining XML unvalidated.
// Tracks retain the original feed URL and episode GUID in podcast.feed and
// podcast.guid metadata; a missing GUID falls back to the audio URL.
// Valid publication dates are retained as YYYY-MM-DD in podcast.published.
func Feed(ctx context.Context, feedURL string) ([]playlist.Track, error) {
	u := feedHTTPURL(feedURL)
	if u == nil {
		return nil, fmt.Errorf("feed URL must be an absolute HTTP(S) URL without userinfo")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating feed request: %w", err)
	}
	// Keep the stricter redirect policy local to feeds, not M3U/PLS requests.
	client := *httpClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if feedHTTPURL(req.URL.String()) == nil {
			return fmt.Errorf("feed redirect must be an absolute HTTP(S) URL without userinfo")
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching feed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %s", resp.Status)
	}

	dec := xml.NewDecoder(io.LimitReader(resp.Body, maxFeedBody))
	dec.CharsetReader = charset.NewReaderLabel
	var tracks []playlist.Track
	var channelTitle, channelArt string
	var seenRSS, seenChannel bool
	depth := 0
	for len(tracks) < maxFeedEpisodes {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("parsing feed: %w", err)
		}
		token, err := dec.Token()
		if err == io.EOF {
			if !seenRSS || !seenChannel {
				return nil, fmt.Errorf("parsing feed: expected RSS with a channel")
			}
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing feed: %w", err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			switch depth {
			case 0:
				if seenRSS || token.Name != (xml.Name{Local: "rss"}) {
					return nil, fmt.Errorf("parsing feed: expected a single RSS root")
				}
				seenRSS = true
				depth++
			case 1:
				if token.Name != (xml.Name{Local: "channel"}) {
					err = dec.Skip()
					break
				}
				if seenChannel {
					return nil, fmt.Errorf("parsing feed: multiple RSS channels")
				}
				seenChannel = true
				depth++
			case 2:
				switch token.Name {
				case xml.Name{Local: "title"}:
					err = dec.DecodeElement(&channelTitle, &token)
				case xml.Name{Local: "image"}, xml.Name{Space: itunesNS, Local: "image"}:
					var image struct {
						Href string `xml:"href,attr"`
						URL  string `xml:"url"`
					}
					err = dec.DecodeElement(&image, &token)
					for _, raw := range []string{image.Href, image.URL} {
						if art := feedHTTPURL(raw); channelArt == "" && art != nil {
							channelArt = art.String()
						}
					}
				case xml.Name{Local: "item"}:
					var item struct {
						Title    string `xml:"title"`
						GUID     string `xml:"guid"`
						PubDate  string `xml:"pubDate"`
						Duration string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
						Episode  string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd episode"`
						Images   []struct {
							Href string `xml:"href,attr"`
						} `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd image"`
						Enclosures []struct {
							URL  string `xml:"url,attr"`
							Type string `xml:"type,attr"`
						} `xml:"enclosure"`
					}
					if err = dec.DecodeElement(&item, &token); err != nil {
						break
					}
					for _, enclosure := range item.Enclosures {
						audio := feedHTTPURL(enclosure.URL)
						if audio == nil {
							continue
						}
						mediaType := strings.TrimSpace(enclosure.Type)
						if mediaType != "" {
							var err error
							mediaType, _, err = mime.ParseMediaType(mediaType)
							if err != nil {
								continue
							}
						}
						switch {
						case mediaType == "", mediaType == "application/octet-stream", mediaType == "binary/octet-stream":
							if !player.SupportedExts[strings.ToLower(path.Ext(audio.Path))] {
								continue
							}
						case strings.HasPrefix(mediaType, "audio/"), mediaType == "application/ogg":
						default:
							continue
						}
						title := strings.TrimSpace(item.Title)
						if title == "" {
							title = "Untitled episode"
						}
						guid := strings.TrimSpace(item.GUID)
						if guid == "" {
							guid = audio.String()
						}
						number, err := strconv.Atoi(strings.TrimSpace(item.Episode))
						if err != nil || number < 0 {
							number = 0
						}
						track := playlist.Track{
							Path:         audio.String(),
							Title:        title,
							Stream:       true,
							DurationSecs: parseItunesDuration(item.Duration),
							TrackNumber:  number,
							ProviderMeta: map[string]string{"podcast.feed": feedURL, "podcast.guid": guid},
						}
						pubDate := strings.TrimSpace(item.PubDate)
						published, err := mail.ParseDate(pubDate)
						if err != nil {
							published, err = time.Parse(time.RFC3339, pubDate)
						}
						if err == nil {
							track.ProviderMeta["podcast.published"] = published.Format(time.DateOnly)
						}
						for _, image := range item.Images {
							if art := feedHTTPURL(image.Href); art != nil {
								track.AlbumArtURL = art.String()
								break
							}
						}
						tracks = append(tracks, track)
						break
					}
				default:
					err = dec.Skip()
				}
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			text := strings.TrimSpace(string(token))
			if depth == 0 && text != "" && !(!seenRSS && text == "\ufeff") {
				return nil, fmt.Errorf("parsing feed: text outside RSS root")
			}
		}
		if err != nil {
			return nil, fmt.Errorf("parsing feed: %w", err)
		}
	}
	// Channel metadata may follow items; apply it after the portion we read.
	for i := range tracks {
		tracks[i].Artist = strings.TrimSpace(channelTitle)
		tracks[i].Album = tracks[i].Artist
		if tracks[i].AlbumArtURL == "" {
			tracks[i].AlbumArtURL = channelArt
		}
	}
	return tracks, nil
}

func feedHTTPURL(raw string) *url.URL {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil
	}
	return u
}
