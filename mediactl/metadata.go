package mediactl

import (
	"github.com/godbus/dbus/v5"

	"cliamp/internal/playback"
)

func makeMetadata(t playback.Track) map[string]dbus.Variant {
	m := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/1")),
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
