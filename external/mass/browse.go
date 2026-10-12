package mass

import (
	"context"
	"fmt"
	"strings"
)

// kindFolder is the media_type of folder rows, which cannot be played.
const kindFolder = "folder"

// browseItem is one row of a library folder: an artist, album, track,
// playlist, genre or radio station.
type browseItem struct {
	URI      string
	Name     string
	Kind     string
	Subtitle string // artist line
}

// massBrowseItem mirrors a media item returned by the library commands.
type massBrowseItem struct {
	URI       string      `json:"uri"`
	Name      string      `json:"name"`
	MediaType string      `json:"media_type"`
	Artists   []massNamed `json:"artists"`
}

// folderPrefix marks the synthetic playlist ids of library folders.
const folderPrefix = pathPrefix + "home/"

// libraryFolder is a library section listed ahead of the user's playlists.
type libraryFolder struct {
	key, name, command string
}

var libraryFolders = []libraryFolder{
	{"recent", "Recently played", "music/recently_played_items"},
	{"artists", "Artists", "music/artists/library_items"},
	{"albums", "Albums", "music/albums/library_items"},
	{"tracks", "Tracks", "music/tracks/library_items"},
	{"genres", "Genres", "music/genres/library_items"},
	{"radios", "Radio stations", "music/radios/library_items"},
}

func folderArgs(key string) map[string]any {
	if key == "recent" {
		return map[string]any{
			"limit":               50,
			"media_types":         []string{"album", "track", "playlist", "artist", "genre"},
			"user_initiated_only": true,
		}
	}
	return map[string]any{"limit": 200, "offset": 0, "summary": true}
}

// openFolder lists the library folder with playlist id id.
func (p *Provider) openFolder(ctx context.Context, id string) ([]browseItem, error) {
	key := strings.TrimPrefix(id, folderPrefix)
	for _, f := range libraryFolders {
		if f.key == key {
			return p.browse(ctx, f.command, folderArgs(key))
		}
	}
	return nil, fmt.Errorf("mass: unknown library folder %q", id)
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

func (p *Provider) browse(ctx context.Context, command string, args map[string]any) ([]browseItem, error) {
	var raw []massBrowseItem
	if err := p.rpcRequest(ctx, command, args, &raw); err != nil {
		return nil, err
	}
	out := make([]browseItem, 0, len(raw))
	for _, it := range raw {
		if it.Name == "" {
			continue
		}
		out = append(out, browseItem{URI: it.URI, Name: it.Name, Kind: it.MediaType, Subtitle: namedJoin(it.Artists)})
	}
	return out, nil
}
