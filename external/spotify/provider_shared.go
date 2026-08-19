package spotify

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
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
	ID         string `json:"id"`
	Name       string `json:"name"`
	SnapshotID string `json:"snapshot_id"`
	Owner      struct {
		ID string `json:"id"`
	} `json:"owner"`
	Items *struct {
		Total int `json:"total"`
	} `json:"items"`
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

// spotifyAlbumItem is a simplified album object from the Spotify Web API, as
// returned by /v1/search?type=album and /v1/albums/{id}.
type spotifyAlbumItem struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	AlbumType   string          `json:"album_type"` // "album", "single" or "compilation"
	URI         string          `json:"uri"`        // canonical spotify:album:...
	TotalTracks int             `json:"total_tracks"`
	ReleaseDate string          `json:"release_date"`
	Artists     []spotifyArtist `json:"artists"`
}

// albumFromItem converts an album search hit into an album placeholder Track.
//
// The result is deliberately not playable: Path carries the spotify:album: URI
// so the entry is identifiable, but go-librespot cannot stream an album URI.
// Callers must expand it through SearchTracks' companion AlbumTracks before
// queueing it, which playlist.Track.IsAlbum signals to the UI.
func albumFromItem(a *spotifyAlbumItem) playlist.Track {
	artists := make([]string, len(a.Artists))
	for i, ar := range a.Artists {
		artists[i] = ar.Name
	}

	var year int
	if len(a.ReleaseDate) >= 4 {
		if y, err := strconv.Atoi(a.ReleaseDate[:4]); err == nil {
			year = y
		}
	}

	uri := a.URI
	if uri == "" {
		uri = fmt.Sprintf("spotify:album:%s", a.ID)
	}

	return playlist.Track{
		Path:   uri,
		Title:  a.Name,
		Artist: strings.Join(artists, ", "),
		Album:  a.Name,
		Year:   year,
		ProviderMeta: map[string]string{
			playlist.MetaKind:    playlist.MetaKindAlbum,
			playlist.MetaAlbumID: a.ID,
		},
	}
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
