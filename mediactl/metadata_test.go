package mediactl

import (
	"reflect"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/bjarneo/cliamp/internal/playback"
)

func TestMakeMetadataMapsPlaybackTrackToMPRISFields(t *testing.T) {
	track := playback.Track{
		Title:       "Song",
		Artist:      "Artist",
		Album:       "Album",
		Genre:       "Ambient",
		TrackNumber: 7,
		URL:         "file:///tmp/song.mp3",
		ArtURL:      "file:///tmp/cover.jpg",
		Duration:    3*time.Minute + 15*time.Second,
	}

	got := makeMetadata(track, trackPath(1))

	want := map[string]dbus.Variant{
		"mpris:trackid":     dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/1")),
		"xesam:title":       dbus.MakeVariant("Song"),
		"xesam:artist":      dbus.MakeVariant([]string{"Artist"}),
		"xesam:album":       dbus.MakeVariant("Album"),
		"xesam:genre":       dbus.MakeVariant([]string{"Ambient"}),
		"xesam:trackNumber": dbus.MakeVariant(7),
		"xesam:url":         dbus.MakeVariant("file:///tmp/song.mp3"),
		"mpris:artUrl":      dbus.MakeVariant("file:///tmp/cover.jpg"),
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
	got := makeMetadata(playback.Track{}, trackPath(1))

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
		"mpris:artUrl",
		"mpris:length",
	} {
		if _, ok := got[key]; ok {
			t.Fatalf("metadata unexpectedly included %q", key)
		}
	}
}

func TestMakeMetadataCoercesInvalidDBusStrings(t *testing.T) {
	track := playback.Track{
		Title:  "Suite N\xb01",
		Artist: "Cello\xffArtist",
		Album:  "Album\x00Name",
		Genre:  "Classical\xfe",
		URL:    "file:///tmp/Cello\xff.mp3",
	}

	got := makeMetadata(track, trackPath(1))

	want := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/1")),
		"xesam:title":   dbus.MakeVariant("Suite N\uFFFD1"),
		"xesam:artist":  dbus.MakeVariant([]string{"Cello\uFFFDArtist"}),
		"xesam:album":   dbus.MakeVariant("AlbumName"),
		"xesam:genre":   dbus.MakeVariant([]string{"Classical\uFFFD"}),
		"xesam:url":     dbus.MakeVariant("file:///tmp/Cello\uFFFD.mp3"),
	}

	for key, wantValue := range want {
		if !reflect.DeepEqual(got[key], wantValue) {
			t.Fatalf("metadata[%q] = %#v, want %#v", key, got[key], wantValue)
		}
	}
}

func TestMakeMetadataCoercesInvalidArtURL(t *testing.T) {
	tests := []struct {
		name   string
		artURL string
		want   string
	}{
		{
			name:   "invalid UTF-8 in artURL",
			artURL: "file:///tmp/cover\xff.jpg",
			want:   "file:///tmp/cover\uFFFD.jpg",
		},
		{
			name:   "null byte in artURL",
			artURL: "file:///tmp/cover\x00.jpg",
			want:   "file:///tmp/cover.jpg",
		},
		{
			name:   "valid artURL unchanged",
			artURL: "file:///tmp/cover.jpg",
			want:   "file:///tmp/cover.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			track := playback.Track{ArtURL: tt.artURL}
			got := makeMetadata(track, trackPath(1))

			gotArtURL := got["mpris:artUrl"].Value().(string)
			if gotArtURL != tt.want {
				t.Fatalf("artURL = %q, want %q", gotArtURL, tt.want)
			}
		})
	}
}
