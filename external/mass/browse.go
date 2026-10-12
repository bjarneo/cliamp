package mass

import (
	"context"
	"fmt"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
)

// folderPrefix marks the synthetic playlist ids of library folders.
const folderPrefix = pathPrefix + "home/"

// folderPageSize is how many rows one page of a library folder holds.
const folderPageSize = 500

// libraryFolder is a library section listed ahead of the user's playlists.
type libraryFolder struct {
	key, name, command string
	paged              bool // the command takes offset and limit
}

var libraryFolders = []libraryFolder{
	{"recent", "Recently played", "music/recently_played_items", false},
	{"artists", "Artists", "music/artists/library_items", true},
	{"albums", "Albums", "music/albums/library_items", true},
	{"tracks", "Tracks", "music/tracks/library_items", true},
	{"genres", "Genres", "music/genres/library_items", true},
	{"radios", "Radio stations", "music/radios/library_items", true},
}

func findFolder(id string) (libraryFolder, error) {
	key := strings.TrimPrefix(id, folderPrefix)
	for _, f := range libraryFolders {
		if f.key == key {
			return f, nil
		}
	}
	return libraryFolder{}, fmt.Errorf("mass: unknown library folder %q", id)
}

// folderItem is a row of a library folder. Track rows carry their length and
// album; artist, album, playlist, genre and radio rows carry what they have.
type folderItem struct {
	massTrack
	MediaType string `json:"media_type"`
}

// folderPage returns one page of the library folder id as rows, and the
// offset of the next page, or 0 after the last one. Rows can be artists,
// albums or playlists as well as tracks: Music Assistant plays any of them.
// Folder entries cannot be played and are left out.
func (p *Provider) folderPage(ctx context.Context, id string, offset int) ([]playlist.Track, int, error) {
	f, err := findFolder(id)
	if err != nil {
		return nil, 0, err
	}
	args := map[string]any{"limit": folderPageSize, "offset": offset, "summary": true}
	if !f.paged {
		if offset > 0 {
			return nil, 0, nil
		}
		args = map[string]any{
			"limit":               50,
			"media_types":         []string{"album", "track", "playlist", "artist", "genre"},
			"user_initiated_only": true,
		}
	}
	var raw []folderItem
	if err := p.rpcRequest(ctx, f.command, args, &raw); err != nil {
		return nil, 0, err
	}
	out := make([]playlist.Track, 0, len(raw))
	for _, it := range raw {
		if it.URI == "" || it.Name == "" || it.MediaType == "folder" {
			continue
		}
		out = append(out, p.trackFromMass(it.massTrack))
	}
	next := 0
	if f.paged && len(raw) == folderPageSize {
		next = offset + folderPageSize
	}
	return out, next, nil
}

// folderTracks returns every row of the library folder id.
func (p *Provider) folderTracks(id string) ([]playlist.Track, error) {
	var out []playlist.Track
	for offset := 0; ; {
		rows, next, err := p.folderPage(context.Background(), id, offset)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if next == 0 {
			return out, nil
		}
		offset = next
	}
}

// TracksPage loads library folders one page at a time, so a large library
// fills the playlist progressively. Other lists load whole. Implements
// provider.TrackPager.
func (p *Provider) TracksPage(id string, offset int) ([]playlist.Track, int, error) {
	if strings.HasPrefix(id, folderPrefix) {
		return p.folderPage(context.Background(), id, offset)
	}
	if offset > 0 {
		return nil, 0, nil
	}
	tracks, err := p.Tracks(id)
	return tracks, 0, err
}

// itemArgs builds the item_id / provider_instance_id_or_domain pair for a
// scheme://mediaType/provider/id URI. A library:// URI maps to "library".
func itemArgs(uri string) map[string]any {
	provider := "library"
	if colon := strings.Index(uri, "://"); colon > 0 {
		provider = uri[:colon]
	}
	return map[string]any{
		"item_id":                        itemIDFromURI(uri),
		"provider_instance_id_or_domain": provider,
	}
}
