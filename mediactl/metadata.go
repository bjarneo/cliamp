package mediactl

import (
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/bjarneo/cliamp/internal/playback"
)

// trackPath returns a unique MPRIS track object path for a sequence number.
// Unique per-track ids let SetPosition reject seeks aimed at a track that is
// no longer current.
func trackPath(seq int64) dbus.ObjectPath {
	return dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/" + strconv.FormatInt(seq, 10))
}

func makeMetadata(t playback.Track, trackID dbus.ObjectPath) map[string]dbus.Variant {
	t.Title = dbusString(t.Title)
	t.Artist = dbusString(t.Artist)
	t.Album = dbusString(t.Album)
	t.Genre = dbusString(t.Genre)
	t.URL = dbusString(t.URL)
	t.ArtURL = dbusString(t.ArtURL)

	m := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(trackID),
	}
	if t.Title != "" {
		m["xesam:title"] = dbus.MakeVariant(t.Title)
	}
	if t.Artist != "" {
		m["xesam:artist"] = dbus.MakeVariant([]string{t.Artist})
	}
	if t.Album != "" {
		m["xesam:album"] = dbus.MakeVariant(t.Album)
	}
	if t.Genre != "" {
		m["xesam:genre"] = dbus.MakeVariant([]string{t.Genre})
	}
	if t.TrackNumber > 0 {
		m["xesam:trackNumber"] = dbus.MakeVariant(t.TrackNumber)
	}
	if t.URL != "" {
		m["xesam:url"] = dbus.MakeVariant(t.URL)
	}
	if t.ArtURL != "" {
		m["mpris:artUrl"] = dbus.MakeVariant(t.ArtURL)
	}
	if t.Duration > 0 {
		m["mpris:length"] = dbus.MakeVariant(t.Duration.Microseconds())
	}
	return m
}

func dbusString(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "")
}
