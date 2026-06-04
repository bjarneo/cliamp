package spotify

import (
	"fmt"
	"strconv"
	"strings"

	"cliamp/playlist"
)

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

type spotifyArtist struct {
	Name string `json:"name"`
}

// spotifyItem is a track or podcast episode object from the Spotify Web API.
// Playlists can hold both; episodes carry a show instead of artists/album.
type spotifyItem struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Type    string          `json:"type"` // "track" or "episode"
	URI     string          `json:"uri"`  // canonical spotify:track:... / spotify:episode:...
	Artists []spotifyArtist `json:"artists"`
	Album   struct {
		Name        string `json:"name"`
		ReleaseDate string `json:"release_date"`
	} `json:"album"`
	Show struct {
		Name string `json:"name"`
	} `json:"show"`
	ReleaseDate  string `json:"release_date"` // episodes carry this at top level
	DurationMs   int    `json:"duration_ms"`
	TrackNumber  int    `json:"track_number"`
	IsPlayable   *bool  `json:"is_playable"`
	Restrictions struct {
		Reason string `json:"reason"`
	} `json:"restrictions"`
}

// trackFromItem converts a Spotify playlist/library item into a playlist.Track,
// handling both music tracks and podcast episodes. It uses the canonical uri
// the API returns (spotify:track:... or spotify:episode:...) as the path, so
// the player routes episodes to go-librespot's episode metadata path; building
// "spotify:track:<id>" for an episode makes go-librespot request track metadata
// for an episode id, which 404s. Episodes carry no artists/album, so the show
// name fills those slots for display.
func trackFromItem(t *spotifyItem) playlist.Track {
	artists := make([]string, len(t.Artists))
	for i, a := range t.Artists {
		artists[i] = a.Name
	}
	artist := strings.Join(artists, ", ")
	album := t.Album.Name
	if t.Type == "episode" {
		artist = t.Show.Name
		album = t.Show.Name
	}

	releaseDate := t.Album.ReleaseDate
	if releaseDate == "" {
		releaseDate = t.ReleaseDate
	}
	var year int
	if len(releaseDate) >= 4 {
		if y, err := strconv.Atoi(releaseDate[:4]); err == nil {
			year = y
		}
	}

	path := t.URI
	if path == "" {
		path = fmt.Sprintf("spotify:track:%s", t.ID) // fallback if uri is absent
	}

	return playlist.Track{
		Path:         path,
		Title:        t.Name,
		Artist:       artist,
		Album:        album,
		Year:         year,
		Stream:       false, // must be false: true causes togglePlayPause to stop+restart instead of pause/resume
		DurationSecs: t.DurationMs / 1000,
		TrackNumber:  t.TrackNumber,
		Unplayable:   (t.IsPlayable != nil && !*t.IsPlayable) || t.Restrictions.Reason != "",
	}
}
