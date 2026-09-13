package model

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
)

// — Message types used by tea.Cmd constructors —

// devicesListedMsg carries the result of listing audio output devices.
type devicesListedMsg struct {
	devices []player.AudioDevice
	err     error
}

// deviceSwitchedMsg signals that an audio device switch attempt completed.
type deviceSwitchedMsg struct {
	name string
	err  error
}

// SetEQPresetMsg is sent by Lua plugins to change the EQ preset by name.
// If Bands is non-nil, the bands are applied and the name becomes a custom label.
type SetEQPresetMsg struct {
	Name  string
	Bands *[10]float64 // nil = use built-in preset bands or keep current
}

// ShowStatusMsg is sent by Lua plugins to display a message in the status bar.
// Duration <= 0 falls back to the default status TTL.
type ShowStatusMsg struct {
	Text     string
	Duration time.Duration
}

type tracksLoadedMsg struct {
	tracks        []playlist.Track
	playlistID    string
	providerName  string
	playlistExact bool
	paged         bool // first page of a provider.TrackPager load
	total         int  // total tracks reported by the pager (paged only)
	gen           uint64
	err           error
}

type playlistsLoadedMsg struct {
	playlists    []playlist.PlaylistInfo
	providerName string
	gen          uint64
	err          error
}

// feedsLoadedMsg carries tracks resolved from remote feed/M3U URLs,
// along with the original source URLs so downstream handlers can identify
// the source (e.g. YouTube Radio) without re-scanning external state.
type feedsLoadedMsg struct {
	tracks   []playlist.Track
	urls     []string // original source URLs that produced these tracks
	autoPlay bool     // whether to start playback automatically
}

// feedTrackResolvedMsg carries episodes resolved from a feed track in the playlist.
type feedTrackResolvedMsg struct {
	tracks []playlist.Track
}

// lyricsLoadedMsg carries parsed LRC output.
type lyricsLoadedMsg struct {
	lines []lyrics.Line
	err   error
	query string
	gen   uint64
}

// netSearchResultsMsg carries the result set of a yt-dlp/sc-dlp search query
// so the UI can present a picker rather than auto-queuing.
type netSearchResultsMsg struct {
	tracks []playlist.Track
	err    error
	query  string
	gen    uint64
}

// streamPlayedMsg signals that async stream Play() completed.
type streamPlayedMsg struct {
	path string
	gen  uint64
	err  error
}

// streamPreloadedMsg signals that async stream Preload() completed.
type streamPreloadedMsg struct {
	path string
	gen  uint64
}

type attachNotifierMsg struct{ notifier playback.Notifier }

// ytdlResolvedMsg carries a lazily resolved yt-dlp track (direct audio URL).
type ytdlResolvedMsg struct {
	index int
	track playlist.Track
	err   error
}

// ytdlBatchMsg carries an incrementally loaded batch of yt-dlp tracks.
// The gen field ties the response to a specific batch session so stale
// responses from a previous or reloaded playlist are discarded.
type ytdlBatchMsg struct {
	gen    uint64 // batch session generation
	tracks []playlist.Track
	err    error
}

// ytdlSavedMsg signals that an async yt-dlp download-to-disk completed.
type ytdlSavedMsg struct {
	path string
	err  error
}

// — Navidrome browser message types —

// navArtistsLoadedMsg carries the full artist list from a provider browser.
type navArtistsLoadedMsg struct {
	artists []provider.ArtistInfo
	gen     uint64
	err     error
}

// navAlbumsLoadedMsg carries one page of albums and the fetch offset.
type navAlbumsLoadedMsg struct {
	albums []provider.AlbumInfo
	offset int  // the offset this page was requested at
	isLast bool // true when the server returned fewer than the requested page size
	gen    uint64
	err    error
}

// navTracksLoadedMsg carries the track list from a provider.AlbumTrackLoader.
type navTracksLoadedMsg struct {
	tracks []playlist.Track
	gen    uint64
	err    error
}

// provAuthDoneMsg signals that interactive provider authentication completed.
type provAuthDoneMsg struct {
	providerName string
	gen          uint64
	err          error
}

// ProvAuthURLMsg carries the OAuth URL produced by a provider's interactive
// auth flow so the TUI can display it. Used as a fallback when the launched
// browser doesn't reach the user (e.g. inside containers or headless envs).
type ProvAuthURLMsg struct {
	ProviderName string
	URL          string
}

// — Command constructors —

func AttachNotifier(notifier playback.Notifier) tea.Msg {
	return attachNotifierMsg{notifier: notifier}
}

func listDevicesCmd() tea.Cmd {
	return func() tea.Msg {
		devices, err := player.ListAudioDevices()
		return devicesListedMsg{devices: devices, err: err}
	}
}

func switchDeviceCmd(name string) tea.Cmd {
	return func() tea.Msg {
		err := player.SwitchAudioDevice(name)
		return deviceSwitchedMsg{name: name, err: err}
	}
}

// authenticateProviderCmd runs the interactive auth flow for a provider.
func authenticateProviderCmd(auth playlist.Authenticator, providerName string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		return provAuthDoneMsg{providerName: providerName, gen: gen, err: auth.Authenticate()}
	}
}

func fetchPlaylistsCmd(prov playlist.Provider, gen uint64) tea.Cmd {
	return func() tea.Msg {
		pls, err := prov.Playlists()
		return playlistsLoadedMsg{playlists: pls, providerName: prov.Name(), gen: gen, err: err}
	}
}

func fetchYTDLBatchCmd(gen uint64, pageURL string, start, count int) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.ResolveYTDLBatch(pageURL, start, count)
		return ytdlBatchMsg{gen: gen, tracks: tracks, err: err}
	}
}

func resolveFeedTrackCmd(feedURL string) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.Remote([]string{feedURL})
		if err != nil {
			return err
		}
		return feedTrackResolvedMsg{tracks: tracks}
	}
}

func resolveRemoteCmd(urls []string, autoPlay bool) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.Remote(urls)
		if err != nil {
			return err
		}
		return feedsLoadedMsg{tracks: tracks, urls: urls, autoPlay: autoPlay}
	}
}

func fetchLyricsCmd(artist, title, query string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		lines, err := lyrics.Fetch(artist, title)
		return lyricsLoadedMsg{lines: lines, err: err, query: query, gen: gen}
	}
}

func fetchTrackLyricsCmd(track playlist.Track, artist, title, query string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		if lines := lyrics.ParseEmbedded(track.EmbeddedLyrics); len(lines) > 0 {
			return lyricsLoadedMsg{lines: lines, query: query, gen: gen}
		}
		lines, err := lyrics.Fetch(artist, title)
		return lyricsLoadedMsg{lines: lines, err: err, query: query, gen: gen}
	}
}

func fetchNetSearchCmd(query string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.Remote([]string{query})
		return netSearchResultsMsg{tracks: tracks, err: err, query: query, gen: gen}
	}
}

func playStreamCmd(p player.Engine, path string, knownDuration time.Duration, gen uint64) tea.Cmd {
	return func() tea.Msg {
		return streamPlayedMsg{path: path, gen: gen, err: p.Play(path, knownDuration)}
	}
}

func preloadStreamCmd(p player.Engine, path string, knownDuration time.Duration, gen uint64) tea.Cmd {
	return func() tea.Msg {
		p.Preload(path, knownDuration) // errors silently ignored
		return streamPreloadedMsg{path: path, gen: gen}
	}
}

func preloadLocalCmd(p player.Engine, path string, knownDuration time.Duration, gen uint64) tea.Cmd {
	return func() tea.Msg {
		p.Preload(path, knownDuration)
		return streamPreloadedMsg{path: path, gen: gen}
	}
}

func playYTDLStreamCmd(p player.Engine, pageURL string, knownDuration time.Duration, gen uint64) tea.Cmd {
	return func() tea.Msg {
		return streamPlayedMsg{path: pageURL, gen: gen, err: p.PlayYTDL(pageURL, knownDuration)}
	}
}

func preloadYTDLStreamCmd(p player.Engine, pageURL string, knownDuration time.Duration, gen uint64) tea.Cmd {
	return func() tea.Msg {
		p.PreloadYTDL(pageURL, knownDuration) // errors silently ignored
		return streamPreloadedMsg{path: pageURL, gen: gen}
	}
}

func saveYTDLCmd(pageURL string, saveDir string) tea.Cmd {
	return func() tea.Msg {
		path, err := resolve.DownloadYTDL(pageURL, saveDir)
		return ytdlSavedMsg{path: path, err: err}
	}
}

func fetchTracksCmd(prov playlist.Provider, playlistID string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := prov.Tracks(playlistID)
		if err != nil {
			return tracksLoadedMsg{playlistID: playlistID, providerName: prov.Name(), gen: gen, err: err}
		}
		// Resolve PLS/M3U wrapper URLs to actual stream URLs so the
		// player receives a direct audio stream instead of a playlist file.
		tracks, expanded := resolveWrapperURLs(tracks)
		return tracksLoadedMsg{tracks: tracks, playlistID: playlistID, providerName: prov.Name(), playlistExact: !expanded, gen: gen}
	}
}

// resolveWrapperURLs expands any PLS/M3U track paths into the actual stream
// URLs they contain. Non-wrapper tracks are passed through unchanged.
func resolveWrapperURLs(tracks []playlist.Track) ([]playlist.Track, bool) {
	var out []playlist.Track
	expanded := false
	for _, t := range tracks {
		if playlist.IsURL(t.Path) && (playlist.IsPLS(t.Path) || playlist.IsM3U(t.Path)) {
			resolved, err := resolve.Remote([]string{t.Path})
			if err == nil && len(resolved) > 0 {
				expanded = true
				// Preserve the original title/artist on resolved tracks.
				for i := range resolved {
					if resolved[i].Title == "" || resolved[i].Title == resolved[i].Path {
						resolved[i].Title = t.Title
					}
					if resolved[i].Artist == "" {
						resolved[i].Artist = t.Artist
					}
					if t.Realtime {
						resolved[i].Realtime = true
					}
				}
				out = append(out, resolved...)
				continue
			}
		}
		out = append(out, t)
	}
	return out, expanded
}

const navAlbumPageSize = 100

func fetchNavArtistsCmd(b provider.ArtistBrowser, gen uint64) tea.Cmd {
	return func() tea.Msg {
		artists, err := b.Artists()
		return navArtistsLoadedMsg{artists: artists, gen: gen, err: err}
	}
}

func fetchNavArtistAlbumsCmd(b provider.ArtistBrowser, artistID string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		albums, err := b.ArtistAlbums(artistID)
		// Artist album lists are complete in one call — treat as last page.
		return navAlbumsLoadedMsg{albums: albums, offset: 0, isLast: true, gen: gen, err: err}
	}
}

func fetchNavAlbumListCmd(b provider.AlbumBrowser, sortType string, offset int, gen uint64) tea.Cmd {
	return func() tea.Msg {
		albums, err := b.AlbumList(sortType, offset, navAlbumPageSize)
		return navAlbumsLoadedMsg{
			albums: albums,
			offset: offset,
			isLast: len(albums) < navAlbumPageSize,
			gen:    gen,
			err:    err,
		}
	}
}

func fetchNavAlbumTracksCmd(l provider.AlbumTrackLoader, albumID string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := l.AlbumTracks(albumID)
		return navTracksLoadedMsg{tracks: tracks, gen: gen, err: err}
	}
}

// catalogSearchMsg carries the result of a provider.CatalogSearcher.SearchCatalog call.
type catalogSearchMsg struct {
	count        int
	providerName string
	gen          uint64
	err          error
}

func fetchCatalogSearchCmd(s provider.CatalogSearcher, providerName, query string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		count, err := s.SearchCatalog(query)
		return catalogSearchMsg{count: count, providerName: providerName, gen: gen, err: err}
	}
}

// — Catalog batch loading for providers with lazy catalogs —

// catalogBatchSize is the number of catalog entries to fetch per page.
const catalogBatchSize = 100

// catalogBatchMsg carries the result of a provider.CatalogLoader.LoadCatalogPage call.
type catalogBatchMsg struct {
	added        int
	providerName string
	gen          uint64
	err          error
}

func fetchCatalogBatchCmd(loader provider.CatalogLoader, offset, limit int, providerName string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		added, err := loader.LoadCatalogPage(offset, limit)
		return catalogBatchMsg{added: added, providerName: providerName, gen: gen, err: err}
	}
}

// — Spotify search + add-to-playlist messages —

type spotSearchResultsMsg struct {
	tracks       []playlist.Track
	err          error
	providerName string
	query        string
	gen          uint64
}

type spotPlaylistsMsg struct {
	playlists    []playlist.PlaylistInfo
	err          error
	providerName string
	gen          uint64
}

type spotAddedMsg struct {
	name         string
	err          error
	providerName string
	gen          uint64
}

type spotCreatedMsg struct {
	name         string
	err          error
	providerName string
	gen          uint64
}

func fetchSpotSearchCmd(ctx context.Context, s provider.Searcher, providerName, query string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := s.SearchTracks(ctx, query, 20)
		return spotSearchResultsMsg{tracks: tracks, err: err, providerName: providerName, query: query, gen: gen}
	}
}

func fetchSpotPlaylistsCmd(prov playlist.Provider, gen uint64) tea.Cmd {
	return func() tea.Msg {
		playlists, err := prov.Playlists()
		if err == nil && prov.Name() == "Local" {
			filtered := playlists[:0]
			for _, pl := range playlists {
				if pl.Name != history.PlaylistName {
					filtered = append(filtered, pl)
				}
			}
			playlists = filtered
		}
		return spotPlaylistsMsg{playlists: playlists, err: err, providerName: prov.Name(), gen: gen}
	}
}

func addToSpotPlaylistCmd(ctx context.Context, w provider.PlaylistWriter, playlistID string, track playlist.Track, providerName, name string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		err := w.AddTrackToPlaylist(ctx, playlistID, track)
		return spotAddedMsg{name: name, err: err, providerName: providerName, gen: gen}
	}
}

func createSpotPlaylistCmd(ctx context.Context, c provider.PlaylistCreator, w provider.PlaylistWriter, providerName, name string, track playlist.Track, gen uint64) tea.Cmd {
	return func() tea.Msg {
		id, err := c.CreatePlaylist(ctx, name)
		if err != nil {
			return spotCreatedMsg{name: name, err: err, providerName: providerName, gen: gen}
		}
		err = w.AddTrackToPlaylist(ctx, id, track)
		return spotCreatedMsg{name: name, err: err, providerName: providerName, gen: gen}
	}
}

// — Incremental provider track paging —

// providerTrackPageSize is the page size requested from provider.TrackPager.
const providerTrackPageSize = 200

// tracksAppendedMsg carries one incrementally loaded page of provider playlist
// tracks. The first page arrives as a tracksLoadedMsg instead (replace
// semantics); this message only extends an already-loaded playlist.
type tracksAppendedMsg struct {
	tracks       []playlist.Track
	total        int
	offset       int
	playlistID   string
	providerName string
	gen          uint64
	err          error
}

// fetchTracksPageCmd loads one page of a paginated provider playlist. The
// offset=0 page reports through tracksLoadedMsg so the existing replace
// semantics apply; later pages report through tracksAppendedMsg.
func fetchTracksPageCmd(p provider.TrackPager, providerName, playlistID string, offset, limit int, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, total, err := p.TracksPage(playlistID, offset, limit)
		if err == nil {
			// Expand PLS/M3U wrapper URLs on every page, not just the first.
			var expanded bool
			tracks, expanded = resolveWrapperURLs(tracks)
			if offset > 0 {
				return tracksAppendedMsg{tracks: tracks, total: total, offset: offset, playlistID: playlistID, providerName: providerName, gen: gen}
			}
			return tracksLoadedMsg{
				tracks:        tracks,
				playlistID:    playlistID,
				providerName:  providerName,
				playlistExact: !expanded,
				paged:         true,
				total:         total,
				gen:           gen,
			}
		}
		if offset == 0 {
			return tracksLoadedMsg{
				playlistID:   playlistID,
				providerName: providerName,
				paged:        true,
				total:        total,
				gen:          gen,
				err:          err,
			}
		}
		return tracksAppendedMsg{total: total, offset: offset, playlistID: playlistID, providerName: providerName, gen: gen, err: err}
	}
}

// — Multi-type provider search —

// spotSearchAllMsg carries provider.MultiSearcher.SearchAll results.
type spotSearchAllMsg struct {
	results      provider.SearchResults
	err          error
	providerName string
	query        string
	gen          uint64
}

func fetchSpotSearchAllCmd(ctx context.Context, s provider.MultiSearcher, providerName, query string, limit int, gen uint64) tea.Cmd {
	return func() tea.Msg {
		results, err := s.SearchAll(ctx, query, limit)
		return spotSearchAllMsg{results: results, err: err, providerName: providerName, query: query, gen: gen}
	}
}

// spotDrillLoadedMsg carries the album/artist/playlist list drilled into from
// the multi-type search results. crumb identifies the drill level it fills.
type spotDrillLoadedMsg struct {
	crumb        string
	albums       []provider.AlbumInfo
	tracks       []playlist.Track
	err          error
	providerName string
	gen          uint64
}

func fetchSpotAlbumTracksCmd(ctx context.Context, l provider.AlbumTrackLoader, providerName, albumID, crumb string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := l.AlbumTracks(albumID)
		return spotDrillLoadedMsg{crumb: crumb, tracks: tracks, err: err, providerName: providerName, gen: gen}
	}
}

// fetchSpotArtistCmd drills into an artist by loading their albums, which
// drill into tracks in turn.
func fetchSpotArtistCmd(ctx context.Context, prov playlist.Provider, providerName string, artist provider.ArtistInfo, crumb string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ab, ok := prov.(provider.ArtistBrowser)
		if !ok {
			return spotDrillLoadedMsg{crumb: crumb, err: fmt.Errorf("artist drill-down not supported"), providerName: providerName, gen: gen}
		}
		albums, err := ab.ArtistAlbums(artist.ID)
		return spotDrillLoadedMsg{crumb: crumb, albums: albums, err: err, providerName: providerName, gen: gen}
	}
}

func fetchSpotPlaylistTracksCmd(ctx context.Context, prov playlist.Provider, providerName, playlistID, crumb string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		// Deliberately Tracks(), not TrackPager: a pager read at offset 0
		// would rewrite the provider's page cursor for this playlist ID and
		// corrupt an incremental queue load of the same playlist in flight.
		tracks, err := prov.Tracks(playlistID)
		return spotDrillLoadedMsg{crumb: crumb, tracks: tracks, err: err, providerName: providerName, gen: gen}
	}
}

// — Artist screen —

// artistDetailMsg carries the ArtistDetail backing the artist screen. The
// artistID is part of the guard identity so a second open supersedes the
// first even when the provider name is unchanged.
type artistDetailMsg struct {
	artistID     string
	detail       provider.ArtistDetail
	err          error
	providerName string
	gen          uint64
}

func fetchArtistDetailCmd(ctx context.Context, l provider.ArtistDetailLoader, providerName, artistID string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		detail, err := l.ArtistDetail(artistID)
		return artistDetailMsg{artistID: artistID, detail: detail, err: err, providerName: providerName, gen: gen}
	}
}

// artistAlbumTracksMsg carries the tracks of one album drilled into from the
// artist screen's Discography section. crumb identifies the drill level it
// fills, like spotDrillLoadedMsg does for the search tabs.
type artistAlbumTracksMsg struct {
	artistID     string
	crumb        string
	tracks       []playlist.Track
	err          error
	providerName string
	gen          uint64
}

func fetchArtistAlbumTracksCmd(ctx context.Context, l provider.AlbumTrackLoader, providerName, artistID, albumID, crumb string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := l.AlbumTracks(albumID)
		return artistAlbumTracksMsg{artistID: artistID, crumb: crumb, tracks: tracks, err: err, providerName: providerName, gen: gen}
	}
}

// — Provider write operations —

// trackLikeToggledMsg carries the outcome of a provider.TrackLiker toggle.
type trackLikeToggledMsg struct {
	liked bool
	err   error
	gen   uint64
}

func toggleTrackLikeCmd(ctx context.Context, l provider.TrackLiker, track playlist.Track, gen uint64) tea.Cmd {
	return func() tea.Msg {
		liked, err := l.ToggleTrackLike(ctx, track)
		return trackLikeToggledMsg{liked: liked, err: err, gen: gen}
	}
}

// playlistUnfollowedMsg carries the outcome of a provider.PlaylistFollower
// unfollow (which deletes owned playlists on most providers).
type playlistUnfollowedMsg struct {
	playlistID   string
	name         string
	owned        bool
	err          error
	providerName string
	gen          uint64
}

func unfollowPlaylistCmd(ctx context.Context, f provider.PlaylistFollower, providerName, playlistID, name string, owned bool, gen uint64) tea.Cmd {
	return func() tea.Msg {
		err := f.UnfollowPlaylistByID(ctx, playlistID)
		return playlistUnfollowedMsg{playlistID: playlistID, name: name, owned: owned, err: err, providerName: providerName, gen: gen}
	}
}

// playlistRenamedMsg carries the outcome of a provider.RemotePlaylistRenamer
// rename.
type playlistRenamedMsg struct {
	playlistID   string
	newName      string
	err          error
	providerName string
	gen          uint64
}

func renamePlaylistCmd(ctx context.Context, r provider.RemotePlaylistRenamer, providerName, playlistID, newName string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		err := r.RenamePlaylistByID(ctx, playlistID, newName)
		return playlistRenamedMsg{playlistID: playlistID, newName: newName, err: err, providerName: providerName, gen: gen}
	}
}

// remoteTrackRemovedMsg carries the outcome of removing a track from a remote
// playlist.
type remoteTrackRemovedMsg struct {
	playlistID   string
	position     int
	trackPath    string
	trackName    string
	err          error
	providerName string
	gen          uint64
}

func removeRemoteTrackCmd(ctx context.Context, r provider.PlaylistTrackRemover, providerName, playlistID string, position int, track playlist.Track, gen uint64) tea.Cmd {
	return func() tea.Msg {
		err := r.RemoveTrackFromPlaylist(ctx, playlistID, position, track)
		return remoteTrackRemovedMsg{playlistID: playlistID, position: position, trackPath: track.Path, trackName: track.DisplayName(), err: err, providerName: providerName, gen: gen}
	}
}

// artistFollowedMsg carries the outcome of a provider.ArtistFollower toggle.
type artistFollowedMsg struct {
	artistID     string
	artistName   string
	follow       bool
	err          error
	providerName string
	gen          uint64
}

func followArtistCmd(ctx context.Context, f provider.ArtistFollower, providerName, artistID, artistName string, follow bool, gen uint64) tea.Cmd {
	return func() tea.Msg {
		var err error
		if follow {
			err = f.FollowArtist(ctx, artistID)
		} else {
			err = f.UnfollowArtist(ctx, artistID)
		}
		return artistFollowedMsg{artistID: artistID, artistName: artistName, follow: follow, err: err, providerName: providerName, gen: gen}
	}
}

// playlistFollowedMsg carries the outcome of a provider.PlaylistFollower
// follow toggle from the search results.
type playlistFollowedMsg struct {
	playlistID   string
	playlistName string
	follow       bool
	err          error
	providerName string
	gen          uint64
}

func followPlaylistCmd(ctx context.Context, f provider.PlaylistFollower, providerName, playlistID, playlistName string, follow bool, gen uint64) tea.Cmd {
	return func() tea.Msg {
		var err error
		if follow {
			err = f.FollowPlaylistByID(ctx, playlistID)
		} else {
			err = f.UnfollowPlaylistByID(ctx, playlistID)
		}
		return playlistFollowedMsg{playlistID: playlistID, playlistName: playlistName, follow: follow, err: err, providerName: providerName, gen: gen}
	}
}

// — Remote section of the write-to-playlist picker —

// plPickerRemoteMsg carries the playlist list of a remote write target so the
// picker can append a second section without blocking openPlaylistPicker.
type plPickerRemoteMsg struct {
	playlists    []playlist.PlaylistInfo
	err          error
	providerName string
}

func fetchPlPickerRemoteCmd(prov playlist.Provider) tea.Cmd {
	return func() tea.Msg {
		playlists, err := prov.Playlists()
		return plPickerRemoteMsg{playlists: playlists, err: err, providerName: prov.Name()}
	}
}

// pickerRemoteWriteMsg carries the outcome of an async write into a remote
// provider playlist (existing or newly created).
type pickerRemoteWriteMsg struct {
	providerName string
	name         string
	created      bool
	added        int
	skipped      int
	err          error
}

// addRemotePickerTracksCmd writes tracks into an existing remote playlist.
func addRemotePickerTracksCmd(ctx context.Context, prov playlist.Provider, playlistID, name string, tracks []playlist.Track) tea.Cmd {
	return func() tea.Msg {
		added, skipped, err := writeRemoteTracks(ctx, prov, playlistID, tracks)
		return pickerRemoteWriteMsg{providerName: prov.Name(), name: name, added: added, skipped: skipped, err: err}
	}
}

// createRemotePickerPlaylistCmd creates a remote playlist and writes tracks
// into it in one operation.
func createRemotePickerPlaylistCmd(ctx context.Context, prov playlist.Provider, name string, tracks []playlist.Track) tea.Cmd {
	return func() tea.Msg {
		c, ok := prov.(provider.PlaylistCreator)
		if !ok {
			return pickerRemoteWriteMsg{providerName: prov.Name(), name: name, err: fmt.Errorf("playlist creation is not supported")}
		}
		id, err := c.CreatePlaylist(ctx, name)
		if err != nil {
			return pickerRemoteWriteMsg{providerName: prov.Name(), name: name, err: err}
		}
		added, skipped, err := writeRemoteTracks(ctx, prov, id, tracks)
		return pickerRemoteWriteMsg{providerName: prov.Name(), name: name, created: true, added: added, skipped: skipped, err: err}
	}
}

// writeRemoteTracks adds tracks to a remote playlist, preferring the batch
// writer when the provider implements it.
func writeRemoteTracks(ctx context.Context, prov playlist.Provider, playlistID string, tracks []playlist.Track) (added, skipped int, err error) {
	if len(tracks) == 0 {
		return 0, 0, nil
	}
	if bw, ok := prov.(provider.PlaylistBatchWriter); ok {
		return bw.AddTracksToPlaylist(ctx, playlistID, tracks)
	}
	w, ok := prov.(provider.PlaylistWriter)
	if !ok {
		return 0, 0, fmt.Errorf("playlist writes are not supported")
	}
	for _, track := range tracks {
		if err := w.AddTrackToPlaylist(ctx, playlistID, track); err != nil {
			return added, skipped, err
		}
		added++
	}
	return added, skipped, nil
}
