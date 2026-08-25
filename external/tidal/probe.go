package tidal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// probeQualities is every tier the playbackinfo endpoint accepts, in
// ascending order.
var probeQualities = []string{qualityLow, qualityHigh, qualityLossless, qualityHiRes}

// Probe requests playback info for one track at every quality tier and writes
// a sanitized diagnostic report to w: delivered audioQuality, manifest type,
// codec, bit depth, sample rate, and the CDN host/extension. Tokens and
// signed URLs are never printed, so the output is safe to share verbatim.
//
// It authenticates from stored credentials, falling back to an interactive
// device-flow sign-in with the URL printed to w. clientID/clientSecret follow
// the same fallback rules as New.
func Probe(ctx context.Context, w io.Writer, query, clientID, clientSecret string) error {
	if clientID == "" {
		clientID, clientSecret = fallbackClientID, fallbackClientSecret
	} else if clientSecret == "" {
		clientSecret = clientID
	}

	c, err := newClientSilent(ctx)
	if err != nil {
		fmt.Fprintln(w, "No stored credentials; starting device-flow sign-in.")
		SetAuthURLObserver(func(u string) {
			fmt.Fprintf(w, "Open %s and approve this device.\n", u)
		})
		defer SetAuthURLObserver(nil)
		c, err = newClientInteractive(ctx, clientID, clientSecret)
		if err != nil {
			return err
		}
	}

	tracks, err := c.searchTracks(ctx, query, 1)
	if err != nil {
		return fmt.Errorf("tidal: probe search: %w", err)
	}
	if len(tracks) == 0 {
		return fmt.Errorf("tidal: probe: no results for %q", query)
	}
	t := tracks[0]

	fmt.Fprintf(w, "Track: %s — %s (id %s, streamReady=%v allowStreaming=%v)\n",
		trackArtist(t, t.Album), t.Title, t.ID.String(), t.StreamReady, t.AllowStreaming)
	clientKind := "embedded fallback"
	if c.clientID != fallbackClientID {
		clientKind = "user-configured"
	}
	fmt.Fprintf(w, "Client: %s credentials (client_id ends %q)\n\n", clientKind, tail(c.clientID, 4))

	for _, q := range probeQualities {
		fmt.Fprintf(w, "requested=%s\n", q)
		pi, err := c.playbackInfo(ctx, t.ID.String(), q)
		if err != nil {
			fmt.Fprintf(w, "  error: %v\n\n", sanitizeError(err))
			continue
		}
		fmt.Fprintf(w, "  audioQuality=%s manifestMimeType=%s", pi.AudioQuality, pi.ManifestMimeType)
		if pi.BitDepth > 0 || pi.SampleRate > 0 {
			fmt.Fprintf(w, " bitDepth=%d sampleRate=%d", pi.BitDepth, pi.SampleRate)
		}
		fmt.Fprintln(w)
		describeManifest(w, pi)
		fmt.Fprintln(w)
	}
	return nil
}

// describeManifest writes sanitized details of the decoded manifest.
func describeManifest(w io.Writer, pi apiPlaybackInfo) {
	raw, err := base64.StdEncoding.DecodeString(pi.Manifest)
	if err != nil {
		fmt.Fprintf(w, "  manifest: base64 decode failed: %v\n", err)
		return
	}
	switch {
	case strings.Contains(pi.ManifestMimeType, "vnd.tidal.bts"):
		var m btsManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			fmt.Fprintf(w, "  bts: parse failed: %v\n", err)
			return
		}
		fmt.Fprintf(w, "  bts: mimeType=%s codecs=%s encryptionType=%s urls=%d",
			m.MimeType, m.Codecs, m.EncryptionType, len(m.URLs))
		if len(m.URLs) > 0 {
			fmt.Fprintf(w, " url[0]=%s", sanitizeURL(m.URLs[0]))
		}
		fmt.Fprintln(w)
	case strings.Contains(pi.ManifestMimeType, "dash+xml"):
		fmt.Fprintf(w, "  dash: codecs=%s audioSamplingRate=%s segments~=%d\n",
			mpdAttr(raw, "codecs"), mpdAttr(raw, "audioSamplingRate"), mpdSegmentCount(raw))
	default:
		fmt.Fprintf(w, "  manifest: unrecognized type, %d bytes\n", len(raw))
	}
}

// mpdAttr extracts the first value of an XML attribute from a raw MPD without
// a full DASH parser; good enough for diagnostics.
func mpdAttr(raw []byte, attr string) string {
	re := regexp.MustCompile(attr + `="([^"]*)"`)
	if m := re.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return "?"
}

// mpdSegmentCount estimates the segment count from a SegmentTimeline, when
// present.
func mpdSegmentCount(raw []byte) int {
	return len(regexp.MustCompile(`<S\s`).FindAll(raw, -1))
}

// sanitizeURL reduces a signed CDN URL to host + file extension, dropping the
// path and every query parameter (which carry the signature).
func sanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparseable)"
	}
	return fmt.Sprintf("host=%s ext=%s", u.Host, path.Ext(u.Path))
}

// sanitizeError strips anything after a '?' so signed URLs inside wrapped
// HTTP errors cannot leak into shared output.
func sanitizeError(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i] + "?…"
	}
	return s
}

// tail returns the last n characters of s.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
