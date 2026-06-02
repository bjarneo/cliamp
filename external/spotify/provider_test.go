package spotify

import "testing"

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

// TestPlaylistAccessible verifies the visibility filter that hides playlists
// the current Spotify user can't list tracks for (would otherwise return 403).
func TestPlaylistAccessible(t *testing.T) {
	const me = "user123"

	tests := []struct {
		name          string
		ownerID       string
		collaborative bool
		userID        string
		want          bool
	}{
		{"own playlist", me, false, me, true},
		{"own collaborative", me, true, me, true},
		{"other user's playlist", "otheruser", false, me, false},
		{"other user's collaborative", "otheruser", true, me, true},
		{"no userID fallback", "otheruser", false, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := spotifyPlaylistItem{
				ID:            "pl1",
				Name:          "Test",
				Collaborative: tt.collaborative,
			}
			item.Owner.ID = tt.ownerID

			got := playlistAccessible(item, tt.userID)
			if got != tt.want {
				t.Errorf("playlistAccessible(owner=%q, collaborative=%v, userID=%q) = %v, want %v",
					tt.ownerID, tt.collaborative, tt.userID, got, tt.want)
			}
		})
	}
}
