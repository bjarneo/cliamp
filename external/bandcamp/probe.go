package bandcamp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/httpclient"
	"github.com/bjarneo/cliamp/internal/subsonicapi"
)

// Probe runs a sanitized diagnostic sweep against Bandcamp's Subsonic beta
// with the configured credentials and writes a report to w: connectivity,
// credential validity, which album sort types the beta accepts, search,
// and what the stream endpoint actually serves (redirect host, content type,
// format=raw behavior). Auth tokens, salts, and signed URLs are never
// printed, so the output is safe to share verbatim in bug reports.
func Probe(ctx context.Context, w io.Writer, cfg config.BandcampConfig) error {
	if !cfg.IsSet() {
		return fmt.Errorf("bandcamp: no credentials — add user/password to [bandcamp] in config.toml (Fan Settings -> Subsonic)")
	}
	if _, err := endpoint(cfg); err != nil {
		fmt.Fprintf(w, "config: %v — probing %s instead\n", err, DefaultURL)
	}
	c := newClient(cfg)

	fmt.Fprintf(w, "Endpoint: %s\n", redactURL(c.BaseURL()))

	if err := c.PingContext(ctx); err != nil {
		fmt.Fprintf(w, "ping: FAILED: %v\n", err)
	} else {
		fmt.Fprintln(w, "ping: ok (note: ping does not validate credentials)")
	}

	if err := c.ValidateAuthContext(ctx); err != nil {
		fmt.Fprintf(w, "auth (getPlaylists): FAILED: %v\n", err)
		return fmt.Errorf("bandcamp: credential check failed — aborting probe")
	}
	fmt.Fprintln(w, "auth (getPlaylists): ok")

	// Album sort types: report which getAlbumList2 types the beta accepts.
	allSorts := []string{
		subsonicapi.SortNewest,
		subsonicapi.SortAlphabeticalByName,
		subsonicapi.SortAlphabeticalByArtist,
		subsonicapi.SortRecent,
		subsonicapi.SortFrequent,
		subsonicapi.SortStarred,
		subsonicapi.SortByYear,
		subsonicapi.SortByGenre,
	}
	var firstAlbumID string
	for _, s := range allSorts {
		// A fresh client per sort: the unsupported-endpoint memo is keyed by
		// endpoint name, so one sort type falling through would otherwise
		// make every later sort report FAILED without a request — and this
		// report exists to say which types the beta really accepts.
		albums, err := newClient(cfg).AlbumListContext(ctx, s, 0, 1)
		switch {
		case err != nil:
			fmt.Fprintf(w, "getAlbumList2 type=%s: FAILED: %v\n", s, err)
		case len(albums) == 0:
			fmt.Fprintf(w, "getAlbumList2 type=%s: ok (empty)\n", s)
		default:
			fmt.Fprintf(w, "getAlbumList2 type=%s: ok\n", s)
			if firstAlbumID == "" {
				firstAlbumID = albums[0].ID
			}
		}
	}

	if _, err := c.SearchTracks(ctx, "a", 1); err != nil {
		fmt.Fprintf(w, "search3: FAILED: %v\n", err)
	} else {
		fmt.Fprintln(w, "search3: ok")
	}

	if firstAlbumID == "" {
		fmt.Fprintln(w, "stream: SKIPPED (no albums in collection)")
		return nil
	}
	tracks, err := c.AlbumTracksContext(ctx, firstAlbumID)
	switch {
	case err != nil:
		fmt.Fprintf(w, "getAlbum: FAILED: %v\n", err)
		return nil
	case len(tracks) == 0:
		fmt.Fprintln(w, "getAlbum: ok, but the newest album has no songs — no stream to probe")
		return nil
	}
	t := tracks[0]
	fmt.Fprintf(w, "sample track: duration=%ds (zero would misclassify as live radio)\n", t.DurationSecs)
	probeStream(ctx, w, "stream", t.Path)
	probeStream(ctx, w, "stream format=raw", t.Path+"&format=raw")
	return nil
}

// redactURL strips any userinfo and query from a URL so it can be printed.
// The configured endpoint is user-supplied and may carry proxy credentials.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparsable URL)"
	}
	u.RawQuery = ""
	return u.Redacted()
}

// probeStream GETs a stream URL over the player's streaming transport
// (shared Transport: HTTP/2-off, ICY handling — though the player's own
// User-Agent and no-timeout policy differ), reporting redirect target host,
// status, content type, and a magic-byte sniff — never a URL query (it
// carries auth, and post-redirect URLs are signed).
func probeStream(ctx context.Context, w io.Writer, label, streamURL string) {
	var redirectHost string
	client := &http.Client{
		Transport: httpclient.Streaming.Transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			redirectHost = req.URL.Host
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		// Never print the parse error: it embeds the URL, whose query
		// carries the auth token.
		fmt.Fprintf(w, "%s: FAILED: invalid stream URL\n", label)
		return
	}
	req.Header.Set("User-Agent", httpclient.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		// A *url.Error carries the full URL, whose query holds the auth
		// token (and, after the redirect, the CDN signature).
		fmt.Fprintf(w, "%s: FAILED: %v\n", label, subsonicapi.SanitizeURLError(err))
		return
	}
	defer resp.Body.Close()
	head := make([]byte, 4)
	n, _ := io.ReadFull(resp.Body, head)
	fmt.Fprintf(w, "%s: http %d, content-type %q, redirect host %q, leading bytes %q\n",
		label, resp.StatusCode, resp.Header.Get("Content-Type"), redirectHost, string(head[:n]))
}
