package spotify

import "testing"

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
