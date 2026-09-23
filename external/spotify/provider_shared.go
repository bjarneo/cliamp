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
	// spotifyAlbumPageSize is the maximum /v1/me/albums accepts per request.
	spotifyAlbumPageSize = 50
	// spotifyArtistAlbumsPageSize is 10: the February 2026 API cut
	// /v1/artists/{id}/albums to a maximum of 10 per request.
	spotifyArtistAlbumsPageSize = 10
)

// Synthetic playlist IDs for library rows that are not real playlists.
const (
	topTracksID      = "TOP TRACKS"
	recentlyPlayedID = "RECENTLY PLAYED"
)

// savedAlbumIDPrefix marks a PlaylistInfo.ID as a saved album rather than a
// playlist, so Tracks() routes it to AlbumTracks. Real playlist and album IDs
// are bare base62, so the "spotify:album:" prefix never collides with one.
const savedAlbumIDPrefix = "spotify:album:"

// savedTracksPlaylistID is the synthetic list ID standing in for Liked Songs,
// which Spotify does not expose through /v1/me/playlists.
const savedTracksPlaylistID = "YOUR MUSIC"

// savedAlbumSection is the UI section header for the user's saved albums.
const savedAlbumSection = "Saved albums"

// isSavedAlbumID reports whether id is a saved-album playlist entry and returns
// the bare album ID.
func isSavedAlbumID(id string) (albumID string, ok bool) {
	rest, ok := strings.CutPrefix(id, savedAlbumIDPrefix)
	return rest, ok
}

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
	ID   string `json:"id"`
	Name string `json:"name"`
}

// artistNames joins the artist display names with ", ".
func artistNames(artists []spotifyArtist) string {
	names := make([]string, len(artists))
	for i, a := range artists {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
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
		Name        string         `json:"name"`
		ReleaseDate string         `json:"release_date"`
		Images      []spotifyImage `json:"images"`
	} `json:"album"`
	Show struct {
		Name   string         `json:"name"`
		Images []spotifyImage `json:"images"`
	} `json:"show"`
	Images       []spotifyImage `json:"images"`       // episodes carry their own
	ReleaseDate  string         `json:"release_date"` // episodes carry this at top level
	DurationMs   int            `json:"duration_ms"`
	TrackNumber  int            `json:"track_number"`
	Popularity   int            `json:"popularity"` // 0-100; deprecated but present on full track objects
	IsPlayable   *bool          `json:"is_playable"`
	Restrictions struct {
		Reason string `json:"reason"`
	} `json:"restrictions"`
}

// spotifyImage is one cover-art size from the Spotify Web API. Track objects
// already carry these, so album art costs no extra request.
type spotifyImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// pickCoverImage chooses the smallest image at least coverTargetPx wide,
// falling back to the largest available. Spotify typically offers 640/300/64:
// the huge one wastes bandwidth for a notification icon or media-control
// thumbnail, and the 64px one is too coarse once scaled.
func pickCoverImage(images []spotifyImage) string {
	const coverTargetPx = 300

	// bestW starts below zero so an image whose width Spotify omitted (decoded
	// as 0) still counts as a fallback.
	best, bestW := "", -1
	smallestOK, smallestOKW := "", 0
	for _, img := range images {
		if img.URL == "" {
			continue
		}
		if img.Width > bestW {
			best, bestW = img.URL, img.Width
		}
		if img.Width >= coverTargetPx && (smallestOKW == 0 || img.Width < smallestOKW) {
			smallestOK, smallestOKW = img.URL, img.Width
		}
	}
	if smallestOK != "" {
		return smallestOK
	}
	return best
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
	Images      []spotifyImage  `json:"images"`
}

// albumFromItem converts an album search hit into an album placeholder Track.
//
// The result is deliberately not playable: Path carries the spotify:album: URI
// so the entry is identifiable, but go-librespot cannot stream an album URI.
// Callers must expand it through SearchTracks' companion AlbumTracks before
// queueing it, which playlist.Track.IsAlbum signals to the UI.
func albumFromItem(a *spotifyAlbumItem) playlist.Track {
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
		Artist: artistNames(a.Artists),
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
	art := pickCoverImage(t.Album.Images)
	if t.Type == "episode" {
		artist = t.Show.Name
		album = t.Show.Name
		if art = pickCoverImage(t.Images); art == "" {
			art = pickCoverImage(t.Show.Images)
		}
	}

	releaseDate := t.Album.ReleaseDate
	if releaseDate == "" {
		releaseDate = t.ReleaseDate
	}
	year := releaseYear(releaseDate)

	path := t.URI
	if path == "" {
		path = fmt.Sprintf("spotify:track:%s", t.ID) // fallback if uri is absent
	}

	return playlist.Track{
		Path:         path,
		Title:        t.Name,
		Artist:       artist,
		Album:        album,
		AlbumArtURL:  art,
		Year:         year,
		Stream:       false, // must be false: true causes togglePlayPause to stop+restart instead of pause/resume
		DurationSecs: t.DurationMs / 1000,
		TrackNumber:  t.TrackNumber,
		Unplayable:   (t.IsPlayable != nil && !*t.IsPlayable) || t.Restrictions.Reason != "",
	}
}

// releaseYear parses the leading 4-digit year from a Spotify release_date
// ("1994", "1994-03-29", "1994-03"); returns 0 when absent or non-numeric.
func releaseYear(date string) int {
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil {
			return y
		}
	}
	return 0
}

// itemKey returns the dedupe/cache key for a track item: the canonical URI,
// falling back to the ID when the URI is absent.
func itemKey(t *spotifyItem) string {
	if t.URI != "" {
		return t.URI
	}
	return t.ID
}
