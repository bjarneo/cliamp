package tidal

import "encoding/json"

// apiArtist is the Tidal artist object.
type apiArtist struct {
	ID   json.Number `json:"id"`
	Name string      `json:"name"`
}

// apiAlbum is the Tidal album object. releaseDate is "YYYY-MM-DD".
type apiAlbum struct {
	ID             json.Number `json:"id"`
	Title          string      `json:"title"`
	NumberOfTracks int         `json:"numberOfTracks"`
	ReleaseDate    string      `json:"releaseDate"`
	Artist         apiArtist   `json:"artist"`
}

// apiTrack is the Tidal track object. Album is absent when the track is
// nested inside an albums/{id}/tracks response.
type apiTrack struct {
	ID             json.Number `json:"id"`
	Title          string      `json:"title"`
	TrackNumber    int         `json:"trackNumber"`
	Duration       int         `json:"duration"`
	AllowStreaming bool        `json:"allowStreaming"`
	StreamReady    bool        `json:"streamReady"`
	Artist         apiArtist   `json:"artist"`
	Artists        []apiArtist `json:"artists"`
	Album          *apiAlbum   `json:"album"`
}

// apiPlaylist is the Tidal playlist object. Playlists are keyed by UUID, not
// numeric ID.
type apiPlaylist struct {
	UUID           string `json:"uuid"`
	Title          string `json:"title"`
	NumberOfTracks int    `json:"numberOfTracks"`
	Duration       int    `json:"duration"`
}

// apiPlaylistItem wraps an entry in a playlistsAndFavoritePlaylists response.
type apiPlaylistItem struct {
	Playlist apiPlaylist `json:"playlist"`
}

// apiList is a paginated Tidal list response.
type apiList[T any] struct {
	Items              []T `json:"items"`
	TotalNumberOfItems int `json:"totalNumberOfItems"`
}

// apiFavoriteItem wraps an entry in a users/{id}/favorites/* response.
type apiFavoriteItem[T any] struct {
	Item T `json:"item"`
}

// apiPlaybackInfo is the tracks/{id}/playbackinfopostpaywall response. The
// manifest is base64-encoded; its format depends on manifestMimeType (see
// manifest.go). AudioQuality is the quality the server actually delivered,
// which may be lower than requested; BitDepth/SampleRate are present on newer
// API responses only.
type apiPlaybackInfo struct {
	AudioQuality     string `json:"audioQuality"`
	ManifestMimeType string `json:"manifestMimeType"`
	Manifest         string `json:"manifest"`
	BitDepth         int    `json:"bitDepth"`
	SampleRate       int    `json:"sampleRate"`
}
