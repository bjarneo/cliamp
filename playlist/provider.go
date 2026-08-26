package playlist

import "errors"

// ErrNeedsAuth is returned by providers that require interactive sign-in
// before they can be used.
var ErrNeedsAuth = errors.New("sign-in required")

// PlaylistInfo describes a playlist with its name and track count.
//
// DurationSecs is optional: providers that can compute it cheaply should
// populate it so the UI can render a total runtime. A zero value means
// "unknown" and the UI will hide the duration column.
//
// Section is optional: providers may set it to group their playlists in the UI.
// Adjacent rows that share a Section are rendered under one header; a change of
// Section emits a "── header ──" divider. The radio provider uses
// SectionedList.IDPrefix instead and leaves Section empty.
//
// DirSourceCount is optional: providers that back playlists with [[dir]]
// directory sources set it so the UI can flag them in the list. A zero value
// means "none/unknown" and the UI hides the indicator.
type PlaylistInfo struct {
	ID             string
	Name           string
	TrackCount     int
	DurationSecs   int
	Section        string
	DirSourceCount int
}

// Provider is the interface for playlist sources (radio, Navidrome, Spotify, etc.).
type Provider interface {
	// Name returns the display name of this provider.
	Name() string

	// Playlists returns the available playlists from this provider.
	Playlists() ([]PlaylistInfo, error)

	// Tracks returns the tracks in the given playlist.
	Tracks(playlistID string) ([]Track, error)
}

// Authenticator is optionally implemented by providers that require sign-in.
type Authenticator interface {
	Authenticate() error
}

// Refresher is optionally implemented by providers that cache playlist or
// track data and support invalidating that cache so the next Playlists() /
// Tracks() call re-fetches from the source.
type Refresher interface {
	Refresh()
}
