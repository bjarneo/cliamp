package radio

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestStationFromTrack(t *testing.T) {
	station := CatalogStation{
		Name: "Jazz [live]", URL: "https://radio.example/live",
		Country: "Norway", State: "Oslo", Codec: "MP3", Bitrate: 192,
		Tags: "jazz,smooth jazz", Homepage: "https://radio.example",
	}
	for _, title := range []string{station.Name, formatCatalogName(station), "Artist - Current Song"} {
		track := stationTrack(station)
		track.Title = title
		got, ok := StationFromTrack(track)
		if !ok || got != station {
			t.Fatalf("StationFromTrack(%q) = %+v, %v; want %+v", title, got, ok, station)
		}
	}
	// Wrapper resolution changes the playback URL, not the station being saved.
	resolved := stationTrack(station)
	resolved.Path = "https://cdn.example/audio"
	if got, ok := StationFromTrack(resolved); !ok || got != station {
		t.Fatalf("resolved station = %+v, %v; want %+v", got, ok, station)
	}

	for _, tc := range []struct {
		name   string
		change func(*playlist.Track)
	}{
		{"unmarked stream", func(t *playlist.Track) { t.ProviderMeta = nil }},
		{"missing name", func(t *playlist.Track) { delete(t.ProviderMeta, "radio.name") }},
		{"missing URL", func(t *playlist.Track) { delete(t.ProviderMeta, "radio.url") }},
		{"local playback path", func(t *playlist.Track) { t.Path = "/tmp/music.mp3" }},
		{"ssh playback URL", func(t *playlist.Track) { t.Path = "ssh://host/music" }},
		{"local file", func(t *playlist.Track) { t.Path = "/tmp/music.mp3"; t.ProviderMeta["radio.url"] = t.Path }},
		{"ssh URL", func(t *playlist.Track) { t.Path = "ssh://host/music"; t.ProviderMeta["radio.url"] = t.Path }},
		{"not live", func(t *playlist.Track) { t.Realtime = false }},
		{"not stream", func(t *playlist.Track) { t.Stream = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			track := stationTrack(station)
			tc.change(&track)
			if _, ok := StationFromTrack(track); ok {
				t.Fatal("non-station recognized as a radio favorite target")
			}
		})
	}
}

func TestBrowseTracksCanBeFavoritedWithoutCatalog(t *testing.T) {
	for _, route := range []string{browseCountriesID, browseTagsID} {
		t.Run(route, func(t *testing.T) {
			station := CatalogStation{
				Name: "Jazz FM", URL: "https://jazz.example/stream", Country: "Norway",
				Codec: "MP3", Bitrate: 192, Tags: "jazz", Homepage: "https://jazz.example",
			}
			d := &directory{stations: []CatalogStation{station}}
			d.serve(t)
			p := newPlaceProvider(t, "")
			id := "jazz"
			if route == browseCountriesID {
				id = (Place{Code: "NO"}).ID()
			}
			tracks, err := p.GenreBrowserFor(route).GenreTracks(id, SortVotes)
			if err != nil || len(tracks) != 1 {
				t.Fatalf("browse = %v, %v", tracks, err)
			}
			got, ok := StationFromTrack(tracks[0])
			if !ok || got != station {
				t.Fatalf("lost station identity: %+v, %v", got, ok)
			}
			if len(p.catalog) != 0 {
				t.Fatal("test requires a station absent from the Catalog")
			}
			if added, err := p.favorites.Toggle(got); err != nil || !added {
				t.Fatalf("favorite = %v, %v", added, err)
			}
			reloaded := LoadFavorites()
			saved := reloaded.Stations()
			if len(saved) != 1 || saved[0] != station {
				t.Fatalf("persisted station = %+v; want %+v", saved, station)
			}
		})
	}
}
