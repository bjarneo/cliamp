package mediactl

import (
	"reflect"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"cliamp/internal/playback"
)

func TestMakeMetadataMapsPlaybackTrackToMPRISFields(t *testing.T) {
	track := playback.Track{
		Title:       "Song",
		Artist:      "Artist",
		Album:       "Album",
		Genre:       "Ambient",
		TrackNumber: 7,
		URL:         "file:///tmp/song.mp3",
		Duration:    3*time.Minute + 15*time.Second,
	}

	got := makeMetadata(track)

	want := map[string]dbus.Variant{
		"mpris:trackid":     dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/1")),
		"xesam:title":       dbus.MakeVariant("Song"),
		"xesam:artist":      dbus.MakeVariant([]string{"Artist"}),
		"xesam:album":       dbus.MakeVariant("Album"),
		"xesam:genre":       dbus.MakeVariant([]string{"Ambient"}),
		"xesam:trackNumber": dbus.MakeVariant(7),
		"xesam:url":         dbus.MakeVariant("file:///tmp/song.mp3"),
		"mpris:length":      dbus.MakeVariant(track.Duration.Microseconds()),
	}

	if len(got) != len(want) {
		t.Fatalf("metadata field count = %d, want %d", len(got), len(want))
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			t.Fatalf("metadata missing key %q", key)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Fatalf("metadata[%q] = %#v, want %#v", key, gotValue, wantValue)
		}
	}
}

func TestMakeMetadataOmitsEmptyOptionalFields(t *testing.T) {
	got := makeMetadata(playback.Track{})

	if len(got) != 1 {
		t.Fatalf("metadata field count = %d, want 1", len(got))
	}
	if !reflect.DeepEqual(got["mpris:trackid"], dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/1"))) {
		t.Fatalf("track id = %#v, want default track id", got["mpris:trackid"])
	}
	for _, key := range []string{
		"xesam:title",
		"xesam:artist",
		"xesam:album",
		"xesam:genre",
		"xesam:trackNumber",
		"xesam:url",
		"mpris:length",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("metadata unexpectedly included %q", key)
		}
	}
}
