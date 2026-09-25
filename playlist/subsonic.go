package playlist

import (
	"net/url"
	"strings"
)

// IsSubsonicStreamURL reports whether path is a Subsonic stream or download
// endpoint — Navidrome, Bandcamp's official API, any Subsonic-family server,
// whatever host it lives on. The player routes these to the buffered
// download pipeline, and IsYTDL consults the same predicate so a stream URL
// on a yt-dlp host (bandcamp.com) is never handed to yt-dlp.
func IsSubsonicStreamURL(path string) bool {
	u, err := url.Parse(path)
	if err != nil {
		return false
	}
	return isSubsonicStreamPath(u.Path)
}

func isSubsonicStreamPath(p string) bool {
	p = strings.ToLower(p)
	return strings.HasSuffix(p, "/rest/stream") ||
		strings.HasSuffix(p, "/rest/stream.view") ||
		strings.HasSuffix(p, "/rest/download") ||
		strings.HasSuffix(p, "/rest/download.view")
}
