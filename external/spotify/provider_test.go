package spotify

import "testing"

// TestSpotifyTrackPageSizeRespectsAPILimit guards against re-introducing the
// [1/904]-instead-of-[1/1804] bug. The /v1/playlists/{id}/items endpoint
// silently caps `limit` at 50; if this constant exceeds that, the Tracks()
// loop advances offset by the requested limit while the server returned only
// 50, causing every other 50-item window to be skipped.
func TestSpotifyTrackPageSizeRespectsAPILimit(t *testing.T) {
	const spotifyAPIPlaylistItemsMaxLimit = 50
	if spotifyTrackPageSize > spotifyAPIPlaylistItemsMaxLimit {
		t.Fatalf("spotifyTrackPageSize = %d, want <= %d (Spotify Web API cap)",
			spotifyTrackPageSize, spotifyAPIPlaylistItemsMaxLimit)
	}
}

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
