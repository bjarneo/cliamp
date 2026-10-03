package playlist

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// tomlMetaPrefix starts the key of each ProviderMeta entry in a TOML track
// section, e.g. provider_meta.navidrome.id.
const tomlMetaPrefix = "provider_meta."

// legacyRestrictedMetaKey is the ProviderMeta key that marked an exclusive
// Mixcloud show before Track.Restricted. Files that older versions wrote
// still hold it, and TrackFromTOML reads it as Restricted.
const legacyRestrictedMetaKey = "mixcloud.exclusive"

// WriteTrackTOML writes the persisted fields of t as `key = value` lines. The
// caller writes the section header and its own keys, such as a timestamp.
// Empty optional fields are left out, and ProviderMeta keys come in sorted
// order, so one track always gives the same bytes. A ProviderMeta key that
// validMetaKey rejects is left out, because the key is written raw.
// TrackFromTOML reads the lines back.
func WriteTrackTOML(w io.Writer, t Track) {
	fmt.Fprintf(w, "path = %q\n", t.Path)
	fmt.Fprintf(w, "title = %q\n", t.Title)
	if t.Artist != "" {
		fmt.Fprintf(w, "artist = %q\n", t.Artist)
	}
	if t.Album != "" {
		fmt.Fprintf(w, "album = %q\n", t.Album)
	}
	if t.Genre != "" {
		fmt.Fprintf(w, "genre = %q\n", t.Genre)
	}
	if t.Year != 0 {
		fmt.Fprintf(w, "year = %d\n", t.Year)
	}
	if t.TrackNumber != 0 {
		fmt.Fprintf(w, "track_number = %d\n", t.TrackNumber)
	}
	if t.DurationSecs != 0 {
		fmt.Fprintf(w, "duration_secs = %d\n", t.DurationSecs)
	}
	// An HTTP path is always a stream. A provider URI, such as a qobuz://
	// track, keeps the flag in the file.
	if t.Stream && !IsURL(t.Path) {
		fmt.Fprintln(w, "stream = true")
	}
	if t.Feed {
		fmt.Fprintln(w, "feed = true")
	}
	if t.Realtime {
		fmt.Fprintln(w, "realtime = true")
	}
	if t.Restricted {
		fmt.Fprintln(w, "restricted = true")
	}
	if t.AlbumArtURL != "" {
		fmt.Fprintf(w, "album_art_url = %q\n", t.AlbumArtURL)
	}
	for _, k := range slices.Sorted(maps.Keys(t.ProviderMeta)) {
		if !validMetaKey(k) {
			continue
		}
		fmt.Fprintf(w, "%s%s = %q\n", tomlMetaPrefix, k, t.ProviderMeta[k])
	}
}

// validMetaKey reports whether k is not empty and holds only the bytes
// A-Z, a-z, 0-9, '.', '_' and '-'. Other bytes, such as a newline, '=' or
// '[', can end the key line or start a new section.
func validMetaKey(k string) bool {
	if k == "" {
		return false
	}
	for i := range len(k) {
		c := k[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// TrackFromTOML builds a Track from the unquoted fields of one TOML section,
// as tomlutil.ParseSections passes them. It ignores keys it does not know, so
// a store can keep its own keys in the same section. Stream is set for an
// HTTP path and for the stream key. ProviderMeta stays nil when the section
// has no provider_meta keys. The legacy provider_meta.mixcloud.exclusive key
// sets Restricted and does not reach ProviderMeta.
func TrackFromTOML(f map[string]string) Track {
	t := Track{
		Path:        f["path"],
		Title:       f["title"],
		Artist:      f["artist"],
		Album:       f["album"],
		Genre:       f["genre"],
		Feed:        f["feed"] == "true",
		Realtime:    f["realtime"] == "true",
		Restricted:  f["restricted"] == "true",
		AlbumArtURL: f["album_art_url"],
	}
	t.Stream = IsURL(t.Path) || f["stream"] == "true"
	if n, err := strconv.Atoi(f["year"]); err == nil {
		t.Year = n
	}
	if n, err := strconv.Atoi(f["track_number"]); err == nil {
		t.TrackNumber = n
	}
	if n, err := strconv.Atoi(f["duration_secs"]); err == nil {
		t.DurationSecs = n
	}
	for k, v := range f {
		if metaKey, ok := strings.CutPrefix(k, tomlMetaPrefix); ok {
			if metaKey == legacyRestrictedMetaKey {
				t.Restricted = t.Restricted || v == "true"
				continue
			}
			if t.ProviderMeta == nil {
				t.ProviderMeta = make(map[string]string)
			}
			t.ProviderMeta[metaKey] = v
		}
	}
	return t
}
