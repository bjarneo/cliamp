package mediactl

import (
	"strconv"

	"github.com/godbus/dbus/v5"

	"cliamp/internal/playback"
)

// trackPath returns a unique MPRIS track object path for a sequence number.
// Unique per-track ids let SetPosition reject seeks aimed at a track that is
// no longer current.
func trackPath(seq int64) dbus.ObjectPath {
	return dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/" + strconv.FormatInt(seq, 10))
}

func makeMetadata(t playback.Track, trackID dbus.ObjectPath) map[string]dbus.Variant {
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
	if t.Duration > 0 {
		m["mpris:length"] = dbus.MakeVariant(t.Duration.Microseconds())
	}
	return m
}
