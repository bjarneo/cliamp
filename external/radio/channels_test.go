package radio

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// channelServer serves a channel list with one live channel and one song
// channel, in that order, and the song list of the song channel.
func channelServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var listRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/stations":
			listRequests.Add(1)
			fmt.Fprint(w, `{"stations":[
				{"id":"lofi","name":"Lofi","stream":"https://radio.cliamp.stream/lofi/stream","tracks":0},
				{"id":"omarchy","name":"Omarchy","stream":"https://radio.cliamp.stream/omarchy/stream","tracks":2,"tracks_url":"https://radio.cliamp.stream/omarchy/tracks"},
				{"id":"","name":"No ID","stream":"https://radio.cliamp.stream/x/stream"},
				{"id":"bad","name":"Bad stream","stream":"ftp://radio.cliamp.stream/bad"}
			]}`)
		case "/omarchy/tracks":
			fmt.Fprint(w, `{"station":"omarchy","count":3,"tracks":[
				{"id":"a1","title":"Write the Missing Plugin","artist":"Ryan","album":"Omarchy","duration":181.6,"url":"https://radio.cliamp.stream/omarchy/tracks/a1"},
				{"id":"b2","title":"Omarchee","duration":0,"url":"https://radio.cliamp.stream/omarchy/tracks/b2"},
				{"id":"c3","title":"No URL","url":"file:///etc/passwd"}
			]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &listRequests
}

func TestChannelProviderPlaylists(t *testing.T) {
	srv, _ := channelServer(t)
	installCatalogClient(t, srv.URL)
	p := NewChannels()

	infos, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	// Song channels come first, and invalid channels are skipped.
	if len(infos) != 2 {
		t.Fatalf("Playlists() = %#v, want the two valid channels", infos)
	}
	if infos[0].ID != "omarchy" || infos[0].Name != "Omarchy" || infos[0].TrackCount != 2 {
		t.Errorf("first row = %#v, want the Omarchy channel with 2 tracks", infos[0])
	}
	if infos[1].ID != "lofi" || infos[1].Name != "Lofi · live" || infos[1].TrackCount != 0 {
		t.Errorf("second row = %#v, want the live Lofi channel", infos[1])
	}
}

func TestChannelProviderTracks(t *testing.T) {
	srv, _ := channelServer(t)
	installCatalogClient(t, srv.URL)
	p := NewChannels()

	t.Run("songs", func(t *testing.T) {
		tracks, err := p.Tracks("omarchy")
		if err != nil {
			t.Fatalf("Tracks: %v", err)
		}
		if len(tracks) != 2 {
			t.Fatalf("Tracks() = %#v, want two songs with HTTP URLs", tracks)
		}
		first := tracks[0]
		if first.Path != "https://radio.cliamp.stream/omarchy/tracks/a1" || first.Title != "Write the Missing Plugin" ||
			first.Artist != "Ryan" || first.Album != "Omarchy" || first.DurationSecs != 182 {
			t.Errorf("first song = %#v", first)
		}
		for _, track := range tracks {
			if !track.Stream || track.Realtime {
				t.Errorf("song %q: Stream = %v, Realtime = %v, want a remote file that is not live", track.Title, track.Stream, track.Realtime)
			}
		}
	})

	t.Run("live", func(t *testing.T) {
		tracks, err := p.Tracks("lofi")
		if err != nil {
			t.Fatalf("Tracks: %v", err)
		}
		if len(tracks) != 1 || tracks[0].Path != "https://radio.cliamp.stream/lofi/stream" || !tracks[0].Realtime {
			t.Errorf("Tracks() = %#v, want the live Lofi stream", tracks)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		if _, err := p.Tracks("missing"); err == nil {
			t.Error("Tracks() for an unknown channel returned no error")
		}
	})
}

func TestChannelProviderCachesUntilRefresh(t *testing.T) {
	srv, listRequests := channelServer(t)
	installCatalogClient(t, srv.URL)
	p := NewChannels()

	for range 2 {
		if _, err := p.Playlists(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Tracks("lofi"); err != nil {
		t.Fatal(err)
	}
	if got := listRequests.Load(); got != 1 {
		t.Errorf("channel list requests = %d, want 1 before Refresh", got)
	}

	p.Refresh()
	if _, err := p.Playlists(); err != nil {
		t.Fatal(err)
	}
	if got := listRequests.Load(); got != 2 {
		t.Errorf("channel list requests = %d, want 2 after Refresh", got)
	}
}

func TestChannelProviderRetriesAfterFailure(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	good, _ := channelServer(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		good.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	installCatalogClient(t, srv.URL)
	p := NewChannels()

	if _, err := p.Playlists(); err == nil {
		t.Fatal("Playlists() returned no error for a 503")
	}
	fail.Store(false)
	infos, err := p.Playlists()
	if err != nil || len(infos) != 2 {
		t.Errorf("Playlists() after recovery = %#v, %v, want two channels", infos, err)
	}
}
