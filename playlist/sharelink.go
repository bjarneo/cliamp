package playlist

import "strings"

// ShareLink derives a shareable https URL for a track path.
//
//   - Spotify URIs (spotify:<type>:<id>) map to the corresponding
//     open.spotify.com page, e.g. spotify:track:<id> becomes
//     https://open.spotify.com/track/<id>.
//   - Plain http(s) URLs are returned unchanged, since they are already
//     links (radio streams, direct files, video pages).
//
// Anything else (local files, yt-dlp search expressions and other
// pseudo-protocols, provider URIs without a public page) has no shareable
// form and reports false.
func ShareLink(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if rest, ok := strings.CutPrefix(path, "spotify:"); ok {
		typ, id, ok := strings.Cut(rest, ":")
		if !ok || typ == "" || id == "" || strings.Contains(id, ":") {
			return "", false
		}
		switch typ {
		case "track", "episode", "album", "playlist", "artist", "show":
			return "https://open.spotify.com/" + typ + "/" + id, true
		default:
			return "", false
		}
	}
	if IsYTSearch(path) {
		return "", false
	}
	if IsURL(path) {
		return path, true
	}
	return "", false
}
