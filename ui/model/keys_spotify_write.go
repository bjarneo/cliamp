package model

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// keys_spotify_write.go implements the remote write-operation keys (like,
// remove, delete/unfollow, rename, follow) shared by the queue, nav browser,
// provider pane, and search overlay. Every operation is gated on the owning
// provider implementing the matching capability interface.

// writeOpTimeout bounds one remote write operation.
const writeOpTimeout = 15 * time.Second

// newLikeContext returns a fresh timeout context for a one-off write command.
func (m *Model) newLikeContext() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), writeOpTimeout)
	// The result is generation-guarded; nothing tracks cancel handles for
	// these short writes, so release the timer when the command finishes.
	time.AfterFunc(writeOpTimeout+time.Second, cancel)
	return ctx
}

// trackScheme returns the "scheme" prefix of a provider-owned URI path (the
// segment before the first ':' when it contains no '/'), or "" for plain
// filesystem paths and URLs.
func trackScheme(path string) string {
	if i := strings.IndexByte(path, ':'); i > 0 && !strings.ContainsRune(path[:i], '/') {
		return path[:i]
	}
	return ""
}

// providerOwnsPath reports whether the provider's CustomStreamer covers the
// given URI scheme.
func providerOwnsPath(p playlist.Provider, path string) bool {
	cs, ok := p.(provider.CustomStreamer)
	if !ok || path == "" {
		return false
	}
	for _, s := range cs.URISchemes() {
		if s != "" && (strings.HasPrefix(path, s+":") || path == s) {
			return true
		}
	}
	return false
}

// providerForTrack returns the provider that owns the track's URI scheme,
// preferring the active provider.
func (m Model) providerForTrack(path string) playlist.Provider {
	if m.provider != nil && providerOwnsPath(m.provider, path) {
		return m.provider
	}
	for _, pe := range m.providers {
		if pe.Provider != nil && providerOwnsPath(pe.Provider, path) {
			return pe.Provider
		}
	}
	return nil
}

// likerForTrack resolves the TrackLiker owning the track, or nil.
func (m Model) likerForTrack(track playlist.Track) provider.TrackLiker {
	prov := m.providerForTrack(track.Path)
	if prov == nil {
		return nil
	}
	l, _ := prov.(provider.TrackLiker)
	return l
}

// likeTrack toggles the liked state of a track on its owning provider.
func (m *Model) likeTrack(track playlist.Track) tea.Cmd {
	liker := m.likerForTrack(track)
	if liker == nil {
		return nil
	}
	return toggleTrackLikeCmd(m.newLikeContext(), liker, track, nextRequest(&m.requests.like))
}

// likeSelectedTrack handles the like key on the main queue (focus playlist).
func (m *Model) likeSelectedTrack() tea.Cmd {
	if m.plCursor < 0 || m.plCursor >= m.playlist.Len() {
		return nil
	}
	return m.likeTrack(m.playlist.Tracks()[m.plCursor])
}

// resetProviderQueueMirror marks the queue as no longer mirroring a remote
// provider playlist. Position-based remote writes (x remove) must not fire
// until a provider playlist is loaded again.
func (m *Model) resetProviderQueueMirror() {
	m.activeProviderPlaylistID = ""
	m.providerQueueLen = 0
	m.providerQueueLastPath = ""
}

// removeSelectedRemote handles the queue `x` key when the queue mirrors a
// remote provider playlist. handled reports whether the remote path (or its
// refusal toast) consumed the key; when false the caller falls back to the
// local queue removal.
func (m *Model) removeSelectedRemote() (cmd tea.Cmd, handled bool) {
	if m.loadedPlaylist != "" || m.activeProviderPlaylistID == "" {
		return nil, false
	}
	rem, ok := m.provider.(provider.PlaylistTrackRemover)
	if !ok {
		return nil, false
	}
	idx := m.plCursor
	if idx < 0 || idx >= m.playlist.Len() || idx >= m.providerQueueLen {
		m.status.Show("Track is not part of the loaded playlist", statusTTLDefault)
		return nil, true
	}
	if m.playlist.Shuffled() {
		m.status.Show("Unshuffle before removing from this playlist", statusTTLDefault)
		return nil, true
	}
	// Belt-and-braces: even when the mirror counters look right, verify the
	// last mirrored row still holds the track it was loaded with — a queue
	// replacement that slipped past the resets breaks position trust.
	if tracks := m.playlist.Tracks(); m.providerQueueLen > 0 && m.providerQueueLastPath != "" &&
		(m.providerQueueLen > len(tracks) || tracks[m.providerQueueLen-1].Path != m.providerQueueLastPath) {
		return nil, false
	}
	track := m.playlist.Tracks()[idx]
	if !providerOwnsPath(m.provider, track.Path) {
		// The row came from somewhere else (user-appended or replaced queue);
		// positions are no longer trustworthy for a remote remove.
		return nil, false
	}
	return removeRemoteTrackCmd(m.newLikeContext(), rem, m.provider.Name(), m.activeProviderPlaylistID, idx, track.DisplayName(), track.Path, nextRequest(&m.requests.provMutation)), true
}

// — provider pane: delete/unfollow (D) and rename (r) —

// startProviderUnfollow arms the inline confirmation for the playlist row
// under the provider pane cursor.
func (m *Model) startProviderUnfollow() {
	if _, ok := m.provider.(provider.PlaylistFollower); !ok {
		return
	}
	if m.provCursor < 0 || m.provCursor >= len(m.providerLists) {
		return
	}
	pl := m.providerLists[m.provCursor]
	if isSyntheticProviderRow(pl.ID) {
		return // Library rows are not real playlists; nothing to unfollow
	}
	m.provConfirm = provConfirmState{active: true, playlistID: pl.ID, name: pl.Name, owned: pl.Owned}
}

// handleProvConfirmKey resolves the pending delete/unfollow confirmation.
// Any key other than the confirm keys cancels (matching the playlist manager's
// y/n pattern).
func (m *Model) handleProvConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter", "y", "Y":
		c := m.provConfirm
		m.provConfirm = provConfirmState{}
		f, ok := m.provider.(provider.PlaylistFollower)
		if !ok {
			return nil
		}
		return unfollowPlaylistCmd(m.newLikeContext(), f, m.provider.Name(), c.playlistID, c.name, c.owned, nextRequest(&m.requests.provMutation))
	default:
		m.provConfirm = provConfirmState{}
	}
	return nil
}

// startProviderRename opens the inline rename input for an owned playlist row.
func (m *Model) startProviderRename() {
	if _, ok := m.provider.(provider.RemotePlaylistRenamer); !ok {
		return
	}
	if m.provCursor < 0 || m.provCursor >= len(m.providerLists) {
		return
	}
	pl := m.providerLists[m.provCursor]
	if !pl.Owned {
		m.status.Show("Only playlists you own can be renamed", statusTTLDefault)
		return
	}
	m.provRename = provRenameState{active: true, playlistID: pl.ID, oldName: pl.Name, name: pl.Name}
}

// handleProvRenameKey edits and commits the provider-pane rename input.
func (m *Model) handleProvRenameKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyEscape:
		m.provRename = provRenameState{}
	case tea.KeyEnter:
		name := strings.TrimSpace(m.provRename.name)
		if name == "" {
			m.status.Show("Playlist name is required", statusTTLDefault)
			return nil
		}
		if name == m.provRename.oldName {
			m.provRename = provRenameState{}
			return nil
		}
		r, ok := m.provider.(provider.RemotePlaylistRenamer)
		if !ok {
			return nil
		}
		ren := m.provRename
		m.provRename = provRenameState{}
		return renamePlaylistCmd(m.newLikeContext(), r, m.provider.Name(), ren.playlistID, name, nextRequest(&m.requests.provMutation))
	default:
		if msg.Code == tea.KeySpace && msg.Text == "" {
			m.insertText("provider-rename", &m.provRename.name, " ")
			return nil
		}
		m.editText("provider-rename", &m.provRename.name, msg)
	}
	return nil
}

// — follow toggles —

// followKey builds the session-local follow-state key for an artist/playlist.
func followKey(kind, providerName, id string) string {
	return kind + ":" + providerName + ":" + id
}

func (m *Model) setFollowed(key string, followed bool) {
	if m.followState == nil {
		m.followState = make(map[string]bool)
	}
	m.followState[key] = followed
}

// toggleFollowArtist follows/unfollows an artist on the nav browser's
// provider. The provider interfaces expose no follow-state query, so the
// first toggle of a session assumes the artist is unfollowed.
func (m *Model) toggleFollowArtist(artist provider.ArtistInfo) tea.Cmd {
	f, ok := m.navBrowser.prov.(provider.ArtistFollower)
	if !ok {
		return nil
	}
	provName := m.navBrowser.prov.Name()
	follow := !m.followState[followKey("artist", provName, artist.ID)]
	return followArtistCmd(m.newLikeContext(), f, provName, artist.ID, artist.Name, follow, nextRequest(&m.requests.follow))
}

// navArtistBrowserFor returns the ArtistBrowser the nav overlay is currently
// listing artists for, when it belongs to the named provider.
func (m Model) navArtistBrowserFor(providerName string) (provider.ArtistBrowser, bool) {
	if !m.navBrowser.visible || m.navBrowser.prov == nil || m.navBrowser.prov.Name() != providerName {
		return nil, false
	}
	if m.navBrowser.screen != navBrowseScreenList {
		return nil, false
	}
	if m.navBrowser.mode != navBrowseModeByArtist && m.navBrowser.mode != navBrowseModeByArtistAlbum {
		return nil, false
	}
	ab, ok := m.navBrowser.prov.(provider.ArtistBrowser)
	return ab, ok
}

// navTrackLikerAvailable reports whether the track under the nav cursor has a
// provider that supports liking it.
func (m Model) navTrackLikerAvailable() bool {
	if m.navBrowser.screen != navBrowseScreenTracks || len(m.navBrowser.tracks) == 0 {
		return false
	}
	idx := m.navBrowser.cursor
	if m.navBrowser.search != "" && m.navBrowser.cursor < len(m.navBrowser.searchIdx) {
		idx = m.navBrowser.searchIdx[m.navBrowser.cursor]
	}
	if idx < 0 || idx >= len(m.navBrowser.tracks) {
		return false
	}
	return m.likerForTrack(m.navBrowser.tracks[idx]) != nil
}
