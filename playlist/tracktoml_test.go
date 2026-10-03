package playlist

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/tomlutil"
)

// fullTOMLTrack sets every exported Track field, so the tests show which
// fields the codec keeps and which it drops.
func fullTOMLTrack() Track {
	return Track{
		Path:           "https://music.example.com/rest/stream?id=42",
		Title:          `Say "Hi"`,
		Artist:         "Artist",
		Album:          "Album",
		Genre:          "Rock",
		Year:           1979,
		TrackNumber:    3,
		Stream:         true,
		Realtime:       true,
		Feed:           true,
		DurationSecs:   208,
		Bookmark:       true,
		Restricted:     true,
		Unplayable:     true,
		DirSourced:     true,
		EmbeddedLyrics: "[00:01.00]Line",
		AlbumArtURL:    "file:///tmp/cover.jpg",
		ProviderMeta: map[string]string{
			"radio.name":   "Station",
			"navidrome.id": "42",
			"podcast.feed": "https://feed.example.com/rss",
		},
	}
}

// A new Track field must be added to fullTOMLTrack, and the author must then
// decide if the codec persists it.
func TestFullTOMLTrackSetsEveryField(t *testing.T) {
	v := reflect.ValueOf(fullTOMLTrack())
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.IsExported() && v.Field(i).IsZero() {
			t.Errorf("fullTOMLTrack does not set %s", f.Name)
		}
	}
}

func roundTripTOML(t *testing.T, in Track) Track {
	t.Helper()
	var b strings.Builder
	b.WriteString("[[entry]]\n")
	WriteTrackTOML(&b, in)
	var got []Track
	tomlutil.ParseSections([]byte(b.String()), "entry", func(f map[string]string) {
		got = append(got, TrackFromTOML(f))
	})
	if len(got) != 1 {
		t.Fatalf("parsed %d sections, want 1:\n%s", len(got), b.String())
	}
	return got[0]
}

func TestTrackTOMLRoundTrip(t *testing.T) {
	full := fullTOMLTrack()
	tests := []struct {
		name string
		in   Track
		want Track
	}{
		{
			name: "every field",
			in:   full,
			want: Track{
				Path:         full.Path,
				Title:        full.Title,
				Artist:       full.Artist,
				Album:        full.Album,
				Genre:        full.Genre,
				Year:         full.Year,
				TrackNumber:  full.TrackNumber,
				Stream:       true,
				Realtime:     true,
				Feed:         true,
				DurationSecs: full.DurationSecs,
				Restricted:   true,
				AlbumArtURL:  full.AlbumArtURL,
				ProviderMeta: full.ProviderMeta,
			},
		},
		{
			name: "path only",
			in:   Track{Path: "/music/a.mp3"},
			want: Track{Path: "/music/a.mp3"},
		},
		{
			name: "stream follows the path",
			in:   Track{Path: "http://radio.example.com/live", Title: "Live"},
			want: Track{Path: "http://radio.example.com/live", Title: "Live", Stream: true},
		},
		{
			// Providers resolve these URIs over the network at play time.
			name: "provider URI keeps its stream flag",
			in:   Track{Path: "qobuz://track/42", Title: "Q", Stream: true},
			want: Track{Path: "qobuz://track/42", Title: "Q", Stream: true},
		},
		{
			name: "provider URI without the flag stays off",
			in:   Track{Path: "spotify:track:abc", Title: "S"},
			want: Track{Path: "spotify:track:abc", Title: "S"},
		},
		{
			name: "a stream flag round-trips at any other path",
			in:   Track{Path: "/music/a.mp3", Stream: true},
			want: Track{Path: "/music/a.mp3", Stream: true},
		},
		{
			name: "empty meta map loads as nil",
			in:   Track{Path: "/a.mp3", ProviderMeta: map[string]string{}},
			want: Track{Path: "/a.mp3"},
		},
		{
			name: "escapes survive",
			in: Track{
				Path:         `/music/a "b" \ c.mp3`,
				Title:        "line one\nline two",
				Artist:       "Björk",
				ProviderMeta: map[string]string{"x.id": `a = "b"`, "x.empty": ""},
			},
			want: Track{
				Path:         `/music/a "b" \ c.mp3`,
				Title:        "line one\nline two",
				Artist:       "Björk",
				ProviderMeta: map[string]string{"x.id": `a = "b"`, "x.empty": ""},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := roundTripTOML(t, tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("round trip:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestWriteTrackTOMLGolden(t *testing.T) {
	const want = `path = "https://music.example.com/rest/stream?id=42"
title = "Say \"Hi\""
artist = "Artist"
album = "Album"
genre = "Rock"
year = 1979
track_number = 3
duration_secs = 208
feed = true
realtime = true
restricted = true
album_art_url = "file:///tmp/cover.jpg"
provider_meta.navidrome.id = "42"
provider_meta.podcast.feed = "https://feed.example.com/rss"
provider_meta.radio.name = "Station"
`
	// Map order is random, so write more than once to catch unsorted keys.
	for range 20 {
		var b strings.Builder
		WriteTrackTOML(&b, fullTOMLTrack())
		if got := b.String(); got != want {
			t.Fatalf("WriteTrackTOML:\n got:\n%s\nwant:\n%s", got, want)
		}
	}
}

// An HTTP path is always a stream, so only a stream at another path writes
// the stream key.
func TestWriteTrackTOMLStreamKey(t *testing.T) {
	tests := []struct {
		name  string
		track Track
		want  bool
	}{
		{name: "provider URI stream", track: Track{Path: "tidal://track/7", Stream: true}, want: true},
		{name: "HTTP stream", track: Track{Path: "https://radio.example/live", Stream: true}},
		{name: "provider URI without the flag", track: Track{Path: "spotify:track:abc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			WriteTrackTOML(&b, tt.track)
			if got := strings.Contains(b.String(), "stream = true\n"); got != tt.want {
				t.Fatalf("stream key written = %v, want %v:\n%s", got, tt.want, b.String())
			}
		})
	}
}

// A ProviderMeta key goes into the file raw, so a key that could end the line
// or the section must not reach the file.
func TestWriteTrackTOMLDropsUnsafeMetaKeys(t *testing.T) {
	tests := []struct {
		name string
		key  string
		keep bool
	}{
		{name: "provider key", key: "navidrome.id", keep: true},
		{name: "mixed case", key: "albumID", keep: true},
		{name: "dash and underscore", key: "x-y_z.1", keep: true},
		{name: "empty", key: ""},
		{name: "newline section", key: "x\n[[dir]]\npath"},
		{name: "carriage return", key: "x\ry"},
		{name: "equals", key: "a=b"},
		{name: "space", key: "a b"},
		{name: "bracket", key: "a]"},
		{name: "hash", key: "#a"},
		{name: "quote", key: `a"b`},
		{name: "non-ASCII", key: "ø.id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Track{Path: "/a.mp3", ProviderMeta: map[string]string{tt.key: "v", "ok.id": "1"}}
			var b strings.Builder
			b.WriteString("[[entry]]\n")
			WriteTrackTOML(&b, in)
			var got []map[string]string
			tomlutil.ParseNamedSections([]byte(b.String()), []string{"entry", "dir"}, func(s string, f map[string]string) {
				if s != "entry" {
					t.Errorf("key %q wrote a [[%s]] section:\n%s", tt.key, s, b.String())
				}
				got = append(got, f)
			})
			if len(got) != 1 {
				t.Fatalf("parsed %d sections, want 1:\n%s", len(got), b.String())
			}
			want := map[string]string{"ok.id": "1"}
			if tt.keep {
				want[tt.key] = "v"
			}
			if meta := TrackFromTOML(got[0]).ProviderMeta; !reflect.DeepEqual(meta, want) {
				t.Errorf("ProviderMeta = %q, want %q:\n%s", meta, want, b.String())
			}
		})
	}
}

// Older versions marked an exclusive Mixcloud show with a provider_meta key.
// TrackFromTOML reads it as Restricted and keeps it out of ProviderMeta.
func TestTrackFromTOMLReadsLegacyRestrictedKey(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]string
		want   Track
	}{
		{
			name:   "legacy exclusive show",
			fields: map[string]string{"path": "/a", "provider_meta.mixcloud.exclusive": "true", "provider_meta.mixcloud.key": "/c/a/"},
			want:   Track{Path: "/a", Restricted: true, ProviderMeta: map[string]string{"mixcloud.key": "/c/a/"}},
		},
		{
			name:   "legacy key set to false",
			fields: map[string]string{"path": "/a", "provider_meta.mixcloud.exclusive": "false"},
			want:   Track{Path: "/a"},
		},
		{
			name:   "restricted key",
			fields: map[string]string{"path": "/a", "restricted": "true"},
			want:   Track{Path: "/a", Restricted: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TrackFromTOML(tt.fields); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TrackFromTOML = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestTrackFromTOMLIgnoresUnknownKeysAndBadNumbers(t *testing.T) {
	got := TrackFromTOML(map[string]string{
		"path":         "/a.mp3",
		"year":         "not a year",
		"played_at":    "2026-05-06T22:09:11Z",
		"feed":         "yes",
		"provider_met": "typo",
	})
	want := Track{Path: "/a.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TrackFromTOML = %+v, want %+v", got, want)
	}
}
