package spotify

// maxResponseBody limits JSON API responses to 10 MB.
const maxResponseBody = 10 << 20

// Pagination limits for the Spotify Web API.
const (
	spotifyPlaylistPageSize = 50
	// spotifyTrackPageSize is capped at 50 because /v1/playlists/{id}/items
	// silently truncates larger limits; requesting more would cause the loop
	// to skip items when offset advances by the requested limit.
	spotifyTrackPageSize = 50
)

// spotifyPlaylistItem is the raw playlist object returned by /v1/me/playlists.
type spotifyPlaylistItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SnapshotID    string `json:"snapshot_id"`
	Collaborative bool   `json:"collaborative"`
	Owner         struct {
		ID string `json:"id"`
	} `json:"owner"`
	Items *struct {
		Total int `json:"total"`
	} `json:"items"`
}

// playlistAccessible reports whether the playlist should be shown to the user.
// Playlists saved from other users (not owned, not collaborative) are excluded
// because the Spotify API returns 403 when listing their tracks.
// When userID is empty (fetch failed), all playlists are included as a fallback.
func playlistAccessible(item spotifyPlaylistItem, userID string) bool {
	if userID == "" {
		return true
	}
	return item.Owner.ID == userID || item.Collaborative
}