package model

import (
	"context"
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
	Bands *[10]float64 // nil = use built-in preset bands or keep current for a custom label
}

// SetEQBandMsg is sent by Lua plugins to adjust one band of the Custom curve.
type SetEQBandMsg struct {
	Band int
	Gain float64
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
	gen           uint64
	resumeIdx     int
	resumeOffset  time.Duration
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
		msg := tracksLoadedMsg{tracks: tracks, playlistID: playlistID, providerName: prov.Name(), playlistExact: !expanded, gen: gen}
		if rt, ok := prov.(provider.ResumeTarget); ok {
			if idx, offset := rt.ResumeTarget(playlistID, tracks); offset > 0 && idx >= 0 && idx < len(tracks) {
				msg.resumeIdx, msg.resumeOffset = idx, offset
			}
		}
		return msg
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

// spotAlbumAction is what to do with an album's tracks once they arrive.
type spotAlbumAction int

const (
	spotAlbumPlay      spotAlbumAction = iota // start the album now
	spotAlbumAppend                           // add to the end of the queue
	spotAlbumQueueNext                        // play right after the current track
)

type spotAlbumTracksMsg struct {
	tracks []playlist.Track
	album  playlist.Track
	action spotAlbumAction
	err    error
	gen    uint64
}

// fetchSpotAlbumTracksCmd expands an album placeholder from the search results
// into its tracks. Album entries carry no streamable path of their own, so this
// runs before the album can reach the player.
func fetchSpotAlbumTracksCmd(loader provider.AlbumTrackLoader, album playlist.Track, action spotAlbumAction, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tracks, err := loader.AlbumTracks(album.AlbumID())
		return spotAlbumTracksMsg{tracks: tracks, album: album, action: action, err: err, gen: gen}
	}
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
