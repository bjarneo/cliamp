package spotify

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	playlist4pb "github.com/devgianlu/go-librespot/proto/spotify/playlist4"
	"google.golang.org/protobuf/proto"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
)

// Spotify keeps the library as an ordered list with folders marked inline: a
// start-group entry opens one and an end-group entry with the same id closes
// it, and they nest. The Web API has no concept of folders and omits entries it
// will not serve, so the rootlist is both richer and cheaper -- one request for
// the whole library rather than one per fifty playlists.
const rootlistEnrichBudget = 3 * time.Second

const (
	rootlistGroupStart = "spotify:start-group:"
	rootlistGroupEnd   = "spotify:end-group:"
	rootlistPlaylist   = "spotify:playlist:"
)

// rootlistEntry is one row of the library: a playlist, or a folder boundary.
type rootlistEntry struct {
	URI        string
	Name       string
	TrackCount int
	Owner      string
	Revision   []byte // playlist version, for cache invalidation
	FolderID   string // set for boundaries
	FolderOpen bool   // true for start-group, false for end-group
}

// isFolder reports whether the entry opens or closes a folder rather than
// naming a playlist.
func (e rootlistEntry) isFolder() bool { return e.FolderID != "" }

// rootlist reads the library through Spotify's own client protocol. It returns
// entries in the order Spotify stores them, folder boundaries included.
func (p *SpotifyProvider) rootlist(ctx context.Context) ([]rootlistEntry, error) {
	sess := p.session
	if sess == nil || sess.sess == nil {
		return nil, fmt.Errorf("spotify: rootlist: no session")
	}
	user := sess.sess.Username()
	if user == "" {
		return nil, fmt.Errorf("spotify: rootlist: no username")
	}

	hm := fmt.Sprintf("hm://playlist/v2/user/%s/rootlist?decorate=revision,length,attributes,timestamp,owner", user)
	resp, err := sess.sess.Spclient().RequestHm(ctx, "GET", hm, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("spotify: rootlist: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("spotify: rootlist: http status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("spotify: rootlist: read: %w", err)
	}

	var content playlist4pb.SelectedListContent
	if err := proto.Unmarshal(body, &content); err != nil {
		return nil, fmt.Errorf("spotify: rootlist: parse: %w", err)
	}

	return parseRootlist(&content)
}

// parseRootlist turns a rootlist response into ordered entries. It is split out
// from the request so the shapes Spotify can answer with stay testable.
func parseRootlist(content *playlist4pb.SelectedListContent) ([]rootlistEntry, error) {
	items := content.GetContents().GetItems()
	// meta_items runs parallel to items and carries the name, track count and
	// owner. An entry without one is unnameable, and unnamed entries are hidden
	// below -- so if none arrived at all, this undocumented endpoint answered a
	// shape we cannot read, and silently hiding the whole library would be the
	// worst way to say so. Fail instead, and let the Web API serve the list.
	metas := content.GetContents().GetMetaItems()
	if len(items) > 0 && len(metas) == 0 {
		return nil, fmt.Errorf("spotify: rootlist: %d items without metadata", len(items))
	}

	entries := make([]rootlistEntry, 0, len(items))
	for i, item := range items {
		uri := item.GetUri()
		switch {
		case strings.HasPrefix(uri, rootlistGroupStart):
			id, name := parseGroupStart(uri)
			entries = append(entries, rootlistEntry{URI: uri, Name: name, FolderID: id, FolderOpen: true})
		case strings.HasPrefix(uri, rootlistGroupEnd):
			entries = append(entries, rootlistEntry{
				URI:      uri,
				FolderID: strings.TrimPrefix(uri, rootlistGroupEnd),
			})
		case strings.HasPrefix(uri, rootlistPlaylist):
			e := rootlistEntry{URI: uri}
			if i < len(metas) {
				m := metas[i]
				e.Name = m.GetAttributes().GetName()
				e.TrackCount = int(m.GetLength())
				e.Owner = m.GetOwnerUsername()
				e.Revision = m.GetRevision()
			}
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// parseGroupStart splits a start-group URI into its id and display name. The
// name is URL-encoded with spaces as "+", so "Late+Night+%26+Chill" is "Late Night & Chill".
func parseGroupStart(uri string) (id, name string) {
	rest := strings.TrimPrefix(uri, rootlistGroupStart)
	id, encoded, found := strings.Cut(rest, ":")
	if !found {
		return rest, ""
	}
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		decoded = strings.ReplaceAll(encoded, "+", " ")
	}
	return id, decoded
}

// playlistIDFromURI returns the bare id of a spotify:playlist: URI.
func playlistIDFromURI(uri string) string {
	return strings.TrimPrefix(uri, rootlistPlaylist)
}

// playlistsFromRootlist builds the library rows from Spotify's own client
// protocol, prepending Liked Songs and appending saved albums so the result
// matches what the Web API path produces.
func (p *SpotifyProvider) playlistsFromRootlist(ctx context.Context) ([]playlist.PlaylistInfo, error) {
	entries, err := p.rootlist(ctx)
	if err != nil {
		return nil, err
	}

	lists := make([]playlist.PlaylistInfo, 0, len(entries)+8)

	// Liked Songs and saved albums still come from the Web API, which can be
	// throttled independently of the client protocol. Neither is worth losing
	// the whole library over, so they share one short budget and are skipped on
	// failure. Saved albums paginate, so budgeting them together bounds the
	// delay regardless of how many requests that takes.
	enrich, cancelEnrich := context.WithTimeout(ctx, rootlistEnrichBudget)
	defer cancelEnrich()

	if liked, err := p.savedTracksInfo(enrich); err == nil {
		lists = append(lists, liked)
	} else {
		applog.Warn("spotify: liked songs count unavailable: %v", err)
		lists = append(lists, playlist.PlaylistInfo{
			ID:      savedTracksPlaylistID,
			Name:    "Your Music",
			Section: "Library",
		})
	}

	p.applyRevisions(entries)
	lists = append(lists, p.rootlistPlaylists(entries, p.session.sess.Username())...)

	if albums, err := p.savedAlbums(enrich); err == nil {
		lists = append(lists, albums...)
	} else {
		applog.Warn("spotify: saved albums unavailable: %v", err)
	}

	return lists, nil
}

// rootlistPlaylists converts library entries into the provider's playlist rows.
// Folder boundaries do not become rows of their own: the pane already draws a
// heading whenever the section changes, so naming a playlist's enclosing folder
// as its section renders the library grouped the way Spotify shows it, in
// Spotify's own order, without a tree widget. Playlists outside any folder keep
// the ownership sections the Web API path uses.
func (p *SpotifyProvider) rootlistPlaylists(entries []rootlistEntry, userID string) []playlist.PlaylistInfo {
	lists := make([]playlist.PlaylistInfo, 0, len(entries))
	type openFolder struct{ id, name string }
	var folders []openFolder // innermost last

	for _, e := range entries {
		switch {
		case e.isFolder() && e.FolderOpen:
			folders = append(folders, openFolder{id: e.FolderID, name: e.Name})
			continue
		case e.isFolder():
			// Only close the folder this end-group actually names, so a
			// malformed list cannot reparent everything that follows it.
			if n := len(folders); n > 0 && folders[n-1].id == e.FolderID {
				folders = folders[:n-1]
			}
			continue
		}

		if skipRootlistEntry(e) {
			continue
		}

		section := "Followed playlists"
		if userID != "" && e.Owner == userID {
			section = "Your playlists"
		}
		if len(folders) > 0 {
			// Nested folders read as a path so a child is distinguishable from
			// a sibling of its parent.
			names := make([]string, len(folders))
			for i, f := range folders {
				names[i] = f.name
			}
			section = strings.Join(names, " / ")
		}

		lists = append(lists, playlist.PlaylistInfo{
			ID:         playlistIDFromURI(e.URI),
			Name:       e.Name,
			TrackCount: e.TrackCount,
			Section:    section,
		})
	}
	return lists
}

// applyRevisions drops a playlist's cached tracks when the library reports a
// revision different from the one they were read at. The Web API listing does
// the same with snapshot_id, but in auto mode the client protocol leads and
// that listing never runs, so without this an edit made elsewhere would never
// reach a committed cache. An entry with no revision says nothing, so it
// changes nothing: the library does not report one for every playlist.
func (p *SpotifyProvider) applyRevisions(entries []rootlistEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range entries {
		if e.isFolder() || len(e.Revision) == 0 {
			continue
		}
		id := playlistIDFromURI(e.URI)
		rev := hex.EncodeToString(e.Revision)
		cached, ok := p.trackCache[id]
		if ok && cached.revision == rev {
			continue
		}
		if ok {
			// Dropping only the resolve would leave a chain paging this list
			// to re-resolve against the new revision and splice the two
			// snapshots into one accumulation, which no later check can catch
			// when the edit left the total alone. Discard the whole read.
			p.discardLoadLocked(id)
		}
		p.trackCache[id] = &playlistCache{revision: rev}
	}
}

// spotifyOwner is the owner the library reports for Spotify's own entries.
const spotifyOwner = "spotify"

// skipRootlistEntry reports whether a library entry cannot be presented as a
// playlist. The library keeps references Spotify no longer serves -- they
// arrive with no name and resolve 404 -- and Spotify-owned entries with no
// tracks, such as DJ, which are generated live rather than being a list. A
// user's own empty playlist is still worth showing, so emptiness alone is not
// enough to hide something.
func skipRootlistEntry(e rootlistEntry) bool {
	if e.Name == "" {
		return true
	}
	return e.Owner == spotifyOwner && e.TrackCount == 0
}
