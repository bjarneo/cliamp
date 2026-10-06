package spotify

import (
	"context"
	"errors"
	"testing"
)

// TestSpotifyTrackPageSizeRespectsAPILimit asserts spotifyTrackPageSize stays
// within the Spotify Web API's silent 50-item cap; see the constant's comment
// in provider.go for why exceeding it silently drops tracks.
func TestSpotifyTrackPageSizeRespectsAPILimit(t *testing.T) {
	tests := []struct {
		name string
		got  int
		max  int
	}{
		{"spotifyTrackPageSize within /v1/playlists/{id}/items cap", spotifyTrackPageSize, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got > tt.max {
				t.Fatalf("page size = %d, want <= %d (Spotify Web API cap)", tt.got, tt.max)
			}
		})
	}
}

// TestTrackFromItem verifies the playlist-item to Track mapping, especially
// that podcast episodes keep their spotify:episode: URI (regression for the
// 404 when episodes were forced to spotify:track:). See issue #228.
func TestTrackFromItem(t *testing.T) {
	playable := true
	unplayable := false

	t.Run("music track", func(t *testing.T) {
		item := &spotifyItem{
			ID: "abc", Name: "Aerodynamic", Type: "track",
			URI: "spotify:track:abc", DurationMs: 212000, TrackNumber: 3,
			IsPlayable: &playable,
			Artists:    []spotifyArtist{{Name: "Daft Punk"}},
		}
		item.Album.Name = "Discovery"
		item.Album.ReleaseDate = "2001-03-12"

		got := trackFromItem(item)
		if got.Path != "spotify:track:abc" {
			t.Errorf("Path = %q, want spotify:track:abc", got.Path)
		}
		if got.Artist != "Daft Punk" || got.Album != "Discovery" || got.Year != 2001 {
			t.Errorf("got %q / %q / %d, want Daft Punk / Discovery / 2001", got.Artist, got.Album, got.Year)
		}
		if got.DurationSecs != 212 {
			t.Errorf("DurationSecs = %d, want 212", got.DurationSecs)
		}
	})

	t.Run("podcast episode keeps episode uri", func(t *testing.T) {
		item := &spotifyItem{
			ID: "ep1", Name: "Episode 42", Type: "episode",
			URI: "spotify:episode:ep1", DurationMs: 3600000, ReleaseDate: "2024-06-01",
		}
		item.Show.Name = "The Show"

		got := trackFromItem(item)
		if got.Path != "spotify:episode:ep1" {
			t.Errorf("Path = %q, want spotify:episode:ep1 (not spotify:track:)", got.Path)
		}
		if got.Artist != "The Show" || got.Album != "The Show" {
			t.Errorf("episode artist/album = %q / %q, want show name", got.Artist, got.Album)
		}
		if got.Year != 2024 {
			t.Errorf("Year = %d, want 2024 (from top-level release_date)", got.Year)
		}
	})

	t.Run("search episode without show name", func(t *testing.T) {
		// /v1/search returns simplified episode objects with no show field.
		item := &spotifyItem{
			ID: "ep2", Name: "JRE #2000", Type: "episode",
			URI: "spotify:episode:ep2", DurationMs: 10800000, ReleaseDate: "2023-08-01",
		}
		got := trackFromItem(item)
		if got.Path != "spotify:episode:ep2" {
			t.Errorf("Path = %q, want spotify:episode:ep2", got.Path)
		}
		if got.Title != "JRE #2000" {
			t.Errorf("Title = %q, want JRE #2000", got.Title)
		}
	})

	t.Run("missing uri falls back to track id", func(t *testing.T) {
		got := trackFromItem(&spotifyItem{ID: "xyz", Name: "No URI"})
		if got.Path != "spotify:track:xyz" {
			t.Errorf("Path = %q, want spotify:track:xyz fallback", got.Path)
		}
	})

	t.Run("unplayable track flagged", func(t *testing.T) {
		got := trackFromItem(&spotifyItem{ID: "u", URI: "spotify:track:u", IsPlayable: &unplayable})
		if !got.Unplayable {
			t.Error("Unplayable = false, want true")
		}
	})
}

func TestAlbumFromItemYear(t *testing.T) {
	tests := []struct {
		releaseDate string
		want        int
	}{
		{releaseDate: "1994-02-01", want: 1994},
		{releaseDate: "1994-02", want: 1994},
		{releaseDate: "1994", want: 1994},
		{releaseDate: "", want: 0},
		{releaseDate: "n/a", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.releaseDate, func(t *testing.T) {
			got := albumFromItem(&spotifyAlbumItem{ID: "al", Name: "Album", ReleaseDate: tt.releaseDate})
			if got.Year != tt.want {
				t.Errorf("Year = %d, want %d", got.Year, tt.want)
			}
		})
	}
}

// TestAuthenticateCancelsEarlierFlow runs three overlapping sign-ins. Each
// new call must cancel the one before it, also after an older call returns
// late, and Close must cancel the last one.
func TestAuthenticateCancelsEarlierFlow(t *testing.T) {
	type flow struct {
		ctx     context.Context
		release chan struct{}
	}
	started := make(chan flow)
	orig := signIn
	t.Cleanup(func() { signIn = orig })
	signIn = func(ctx context.Context, _ string, _ *Session) (*Session, error) {
		f := flow{ctx, make(chan struct{})}
		started <- f
		<-f.release
		return nil, ctx.Err()
	}

	p := New(nil, "client", 320)
	errs := make(chan error, 3)
	var flows []flow
	// finish lets flow i return and checks that it ended as canceled.
	finish := func(i int) {
		t.Helper()
		close(flows[i].release)
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Fatalf("sign-in %d error = %v, want context.Canceled", i+1, err)
		}
	}
	for i := range 3 {
		go func() { errs <- p.Authenticate() }()
		flows = append(flows, <-started)
		if i == 0 {
			continue
		}
		if flows[i-1].ctx.Err() == nil {
			t.Fatalf("sign-in %d did not cancel sign-in %d", i+1, i)
		}
		// The older call returns after the newer call took over.
		finish(i - 1)
	}
	p.Close()
	if flows[2].ctx.Err() == nil {
		t.Fatal("Close did not cancel the last sign-in")
	}
	finish(2)
}
