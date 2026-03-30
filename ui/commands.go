package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cliamp/lyrics"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/provider"
	"cliamp/resolve"
)

// — Message types used by tea.Cmd constructors —

// SetEQPresetMsg is sent by Lua plugins to change the EQ preset by name.
// If Bands is non-nil, the bands are applied and the name becomes a custom label.
type SetEQPresetMsg struct {
	Name  string
	Bands *[10]float64 // nil = use built-in preset bands or keep current
}

type tracksLoadedMsg []playlist.Track

// feedsLoadedMsg carries tracks resolved from remote feed/M3U URLs,
// along with the original source URLs so downstream handlers can identify
// the source (e.g. YouTube Radio) without re-scanning external state.
type feedsLoadedMsg struct {
	tracks   []playlist.Track
	urls      []string // original source URLs that produced these tracks
	autoPlay bool     // whether to start playback automatically
}

// lyricsLoadedMsg carries parsed LRC output.
type lyricsLoadedMsg struct {
	lines []lyrics.Line
	err   error
}

// netSearchLoadedMsg carries tracks dynamically searched from the internet.
type netSearchLoadedMsg []playlist.Track

// streamPlayedMsg signals that async stream Play() completed.
type streamPlayedMsg struct{ err error }

// streamPreloadedMsg signals that async stream Preload() completed.
type streamPreloadedMsg struct{}

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

// navArtistsLoadedMsg carries the full artist list from a provider.ArtistBrowser.
type navArtistsLoadedMsg []provider.ArtistInfo

// navAlbumsLoadedMsg carries one page of albums and the fetch offset.
type navAlbumsLoadedMsg struct {
	albums []provider.AlbumInfo
	offset int  // the offset this page was requested at
	isLast bool // true when the server returned fewer than the requested page size
}

// navTracksLoadedMsg carries the track list from a provider.AlbumTrackLoader.
type navTracksLoadedMsg []playlist.Track

// provAuthDoneMsg signals that interactive provider authentication completed.
type provAuthDoneMsg struct{ err error }

// — Command constructors —

// authenticateProviderCmd runs the interactive auth flow for a provider.
func authenticateProviderCmd(auth playlist.Authenticator) tea.Cmd {
	return func() tea.Msg {
		return provAuthDoneMsg{err: auth.Authenticate()}
	}
}

func fetchPlaylistsCmd(prov playlist.Provider) tea.Cmd {
	return func() tea.Msg {
		pls, err := prov.Playlists()
		if err != nil {
			return err
		}
		return pls
	}
}

func fetchYTDLBatchCmd(gen uint64, pageURL string, start, count int) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.ResolveYTDLBatch(pageURL, start, count)
		return ytdlBatchMsg{gen: gen, tracks: tracks, err: err}
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

func fetchLyricsCmd(artist, title string) tea.Cmd {
	return func() tea.Msg {
		lines, err := lyrics.Fetch(artist, title)
		return lyricsLoadedMsg{lines: lines, err: err}
	}
}

func fetchNetSearchCmd(query string) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.Remote([]string{query})
		if err != nil {
			return err
		}
		return netSearchLoadedMsg(tracks)
	}
}

func playStreamCmd(p *player.Player, path string, knownDuration time.Duration) tea.Cmd {
	return func() tea.Msg {
		return streamPlayedMsg{err: p.Play(path, knownDuration)}
	}
}

func preloadStreamCmd(p *player.Player, path string, knownDuration time.Duration) tea.Cmd {
	return func() tea.Msg {
		p.Preload(path, knownDuration) // errors silently ignored
		return streamPreloadedMsg{}
	}
}

func playYTDLStreamCmd(p *player.Player, pageURL string, knownDuration time.Duration) tea.Cmd {
	return func() tea.Msg {
		return streamPlayedMsg{err: p.PlayYTDL(pageURL, knownDuration)}
	}
}

func preloadYTDLStreamCmd(p *player.Player, pageURL string, knownDuration time.Duration) tea.Cmd {
	return func() tea.Msg {
		p.PreloadYTDL(pageURL, knownDuration) // errors silently ignored
		return streamPreloadedMsg{}
	}
}

func saveYTDLCmd(pageURL string, saveDir string) tea.Cmd {
	return func() tea.Msg {
		path, err := resolve.DownloadYTDL(pageURL, saveDir)
		return ytdlSavedMsg{path: path, err: err}
	}
}

func fetchTracksCmd(prov playlist.Provider, playlistID string) tea.Cmd {
	return func() tea.Msg {
		tracks, err := prov.Tracks(playlistID)
		if err != nil {
			return err
		}
		// Resolve PLS/M3U wrapper URLs to actual stream URLs so the
		// player receives a direct audio stream instead of a playlist file.
		tracks = resolveWrapperURLs(tracks)
		return tracksLoadedMsg(tracks)
	}
}

// resolveWrapperURLs expands any PLS/M3U track paths into the actual stream
// URLs they contain. Non-wrapper tracks are passed through unchanged.
func resolveWrapperURLs(tracks []playlist.Track) []playlist.Track {
	var out []playlist.Track
	for _, t := range tracks {
		if playlist.IsURL(t.Path) && (playlist.IsPLS(t.Path) || playlist.IsM3U(t.Path)) {
			resolved, err := resolve.Remote([]string{t.Path})
			if err == nil && len(resolved) > 0 {
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
	return out
}

const navAlbumPageSize = 100

func fetchNavArtistsCmd(b provider.ArtistBrowser) tea.Cmd {
	return func() tea.Msg {
		artists, err := b.Artists()
		if err != nil {
			return err
		}
		return navArtistsLoadedMsg(artists)
	}
}

func fetchNavArtistAlbumsCmd(b provider.ArtistBrowser, artistID string) tea.Cmd {
	return func() tea.Msg {
		albums, err := b.ArtistAlbums(artistID)
		if err != nil {
			return err
		}
		// Artist album lists are complete in one call — treat as last page.
		return navAlbumsLoadedMsg{albums: albums, offset: 0, isLast: true}
	}
}

func fetchNavAlbumListCmd(b provider.AlbumBrowser, sortType string, offset int) tea.Cmd {
	return func() tea.Msg {
		albums, err := b.AlbumList(sortType, offset, navAlbumPageSize)
		if err != nil {
			return err
		}
		return navAlbumsLoadedMsg{
			albums: albums,
			offset: offset,
			isLast: len(albums) < navAlbumPageSize,
		}
	}
}

func fetchNavAlbumTracksCmd(l provider.AlbumTrackLoader, albumID string) tea.Cmd {
	return func() tea.Msg {
		tracks, err := l.AlbumTracks(albumID)
		if err != nil {
			return err
		}
		return navTracksLoadedMsg(tracks)
	}
}

// catalogSearchMsg carries the result of a provider.CatalogSearcher.SearchCatalog call.
type catalogSearchMsg struct {
	count int
	err   error
}

func fetchCatalogSearchCmd(s provider.CatalogSearcher, query string) tea.Cmd {
	return func() tea.Msg {
		count, err := s.SearchCatalog(query)
		return catalogSearchMsg{count: count, err: err}
	}
}

// — Catalog batch loading for providers with lazy catalogs —

// catalogBatchSize is the number of catalog entries to fetch per page.
const catalogBatchSize = 100

// catalogBatchMsg carries the result of a provider.CatalogLoader.LoadCatalogPage call.
type catalogBatchMsg struct {
	added int
	err   error
}

func fetchCatalogBatchCmd(loader provider.CatalogLoader, offset, limit int) tea.Cmd {
	return func() tea.Msg {
		added, err := loader.LoadCatalogPage(offset, limit)
		return catalogBatchMsg{added: added, err: err}
	}
}

// — Spotify search + add-to-playlist messages —

type spotSearchResultsMsg struct {
	tracks []playlist.Track
	err    error
}

type spotPlaylistsMsg struct {
	playlists []playlist.PlaylistInfo
	err       error
}

type spotAddedMsg struct {
	name string
	err  error
}

type spotCreatedMsg struct {
	name string
	err  error
}

func fetchSpotSearchCmd(s provider.Searcher, query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tracks, err := s.SearchTracks(ctx, query, 20)
		return spotSearchResultsMsg{tracks: tracks, err: err}
	}
}

func fetchSpotPlaylistsCmd(prov playlist.Provider) tea.Cmd {
	return func() tea.Msg {
		playlists, err := prov.Playlists()
		return spotPlaylistsMsg{playlists: playlists, err: err}
	}
}

func addToSpotPlaylistCmd(w provider.PlaylistWriter, playlistID string, track playlist.Track, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := w.AddTrackToPlaylist(ctx, playlistID, track)
		return spotAddedMsg{name: name, err: err}
	}
}

func createSpotPlaylistCmd(c provider.PlaylistCreator, w provider.PlaylistWriter, name string, track playlist.Track) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		id, err := c.CreatePlaylist(ctx, name)
		if err != nil {
			return spotCreatedMsg{name: name, err: err}
		}
		err = w.AddTrackToPlaylist(ctx, id, track)
		return spotCreatedMsg{name: name, err: err}
	}
}

