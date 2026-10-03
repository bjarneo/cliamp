package model

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
	"github.com/bjarneo/cliamp/tracksave"
)

// ipcLibraryRequest carries a provider or saved-playlist operation to
// handleIPCLibrary. Reply receives the result of the job.
type ipcLibraryRequest struct {
	Op       string
	Provider string
	Playlist string
	Query    string
	Artist   string
	Album    string
	Sort     string
	Offset   int
	Limit    int
	Index    int
	NewName  string
	Track    *ipc.TrackInfo
	Tracks   []ipc.TrackInfo
	Context  context.Context
	Reply    chan ipc.Response
}

// ipcURLRequest carries url.load to handleIPCURL. Play starts the first
// added track even when something already plays. Without it the URL is
// appended and plays only when the player was idle.
type ipcURLRequest struct {
	URL     string
	Play    bool
	Context context.Context
	Reply   chan ipc.Response
}

// ipcSaveRequest, ipcLyricsRequest and ipcHistoryRequest carry the save,
// lyrics, history and history.clear operations.
type ipcSaveRequest struct {
	Context context.Context
	Reply   chan ipc.Response
}

type ipcLyricsRequest struct {
	Context context.Context
	Reply   chan ipc.Response
}

type ipcHistoryRequest struct {
	Op    string
	Limit int
	Reply chan ipc.Response
}

type ipcProviderLoadResult struct {
	request  ipcLibraryRequest
	tracks   []playlist.Track
	provider string // Name of the provider that served the tracks
	loaded   string
	source   string // Provider key and ID for the runtime snapshot
	err      error
}

// ipcPlaylistRenamedMsg tells Update that an IPC rename of a playlist of the
// named provider succeeded. Update follows the rename, then replies.
type ipcPlaylistRenamedMsg struct {
	provider         string
	oldName, newName string
	reply            chan ipc.Response
}

// ipcHistoryClearedMsg tells Update that IPC history.clear emptied the
// history. Update refreshes the views of it, then replies.
type ipcHistoryClearedMsg struct {
	reply chan ipc.Response
}

type ipcURLLoadResult struct {
	request ipcURLRequest
	tracks  []playlist.Track
	err     error
}

type ipcFeedLoadResult struct {
	op       string
	feed     playlist.Track
	jobs     *ipc.JobStore
	jobID    string
	revision uint64
	tracks   []playlist.Track
	err      error
}

// handleIPCURL resolves the URL of request in a command. A job.cancel request
// or an IPC server shutdown cancels the request context, which stops the
// resolve.
func (m *Model) handleIPCURL(request ipcURLRequest) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.URLContext(requestContext(request.Context), request.URL)
		return ipcURLLoadResult{request: request, tracks: tracks, err: err}
	}
}

func (m *Model) handleIPCURLResult(result ipcURLLoadResult) tea.Cmd {
	if result.request.Context != nil && result.request.Context.Err() != nil {
		return nil
	}
	if result.err != nil {
		result.request.Reply <- ipc.Response{OK: false, Error: result.err.Error()}
		return nil
	}
	if len(result.tracks) == 0 {
		result.request.Reply <- ipc.Response{OK: false, Error: "no tracks found at URL"}
		return nil
	}
	// A Play request jumps to the first newly added track, so a caller that
	// asked to play a URL hears it even when something is already playing.
	// Without it the tracks are appended and only start when the player is
	// idle, which is the right default for a plain append.
	wasStopped := !m.player.IsPlaying()
	start := m.appendTracks(result.tracks...)
	result.request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(result.tracks, m.trackFavoriteLookup(true)), Total: len(result.tracks)}
	if result.request.Play {
		m.stopPlayback()
		m.player.ClearPreload()
		m.playlist.SetIndex(start)
		m.plCursor = start
		m.adjustScroll()
		return m.playCurrentTrack()
	}
	if wasStopped {
		return m.playCurrentTrack()
	}
	return nil
}

func (m *Model) handleIPCSave(request ipcSaveRequest) tea.Cmd {
	track, index := m.currentPlaybackTrack()
	if index < 0 {
		request.Reply <- ipc.Response{OK: false, Error: "nothing to save"}
		return nil
	}
	directory := m.downloadsDirectory
	return func() tea.Msg {
		path, err := tracksave.SaveTo(requestContext(request.Context), track, directory)
		if err != nil {
			request.Reply <- ipc.Response{OK: false, Error: err.Error()}
		} else {
			request.Reply <- ipc.Response{OK: true, Output: path}
		}
		return nil
	}
}

func ipcFeedLoadCmd(ctx context.Context, op string, feed playlist.Track, jobs *ipc.JobStore, jobID string, revision uint64) tea.Cmd {
	return func() tea.Msg {
		resolveCtx, cancel := context.WithTimeout(requestContext(ctx), 30*time.Second)
		defer cancel()
		tracks, err := resolve.Feed(resolveCtx, feed.Path)
		if err == nil {
			err = resolveCtx.Err()
		}
		return ipcFeedLoadResult{
			op: op, feed: feed, jobs: jobs, jobID: jobID, revision: revision,
			tracks: tracks, err: err,
		}
	}
}

func (m *Model) handleIPCFeedLoad(result ipcFeedLoadResult) tea.Cmd {
	ctx, ok := result.jobs.Context(result.jobID)
	if !ok || ctx.Err() != nil {
		return nil
	}
	if result.err == nil && len(result.tracks) == 0 {
		result.err = fmt.Errorf("no playable episodes found in feed")
	}
	if result.err != nil {
		err := v2InternalError()
		err.Detail = result.err.Error()
		m.failV2Job(result.jobs, result.jobID, err)
		return nil
	}
	if result.revision != 0 && result.revision != m.playlist.Revision() {
		m.failV2Job(result.jobs, result.jobID, v2ConflictError())
		return nil
	}
	// Expand before touching the playlist: playing a feed placeholder would
	// invoke the legacy feed resolver, which replaces the entire playlist.
	var cmd tea.Cmd
	if result.op == "track.play" {
		cmd = m.playAlbumImmediate(result.feed, result.tracks)
	} else {
		cmd = m.queueAlbumNext(result.feed, result.tracks)
	}
	// Capture completion with this mutation, not in a later waiter update.
	m.completeV2Job(result.jobs, result.jobID, m.v2PlaylistResponse())
	return cmd
}

func (m *Model) handleIPCLibrary(request ipcLibraryRequest) tea.Cmd {
	if request.Context != nil && request.Context.Err() != nil {
		return nil
	}
	if request.Op == "provider.list" {
		items := make([]ipc.ProviderInfo, 0, len(m.providers))
		for _, entry := range m.providers {
			_, searchable := entry.Provider.(provider.Searcher)
			if _, ok := entry.Provider.(stationSearcher); ok {
				searchable = true
			}
			_, browseArtists := entry.Provider.(provider.ArtistBrowser)
			_, browseAlbums := entry.Provider.(provider.AlbumBrowser)
			_, catalog := entry.Provider.(provider.CatalogLoader)
			items = append(items, ipc.ProviderInfo{Key: entry.Key, Name: entry.Name, Searchable: searchable, BrowseArtists: browseArtists, BrowseAlbums: browseAlbums, Catalog: catalog})
		}
		request.Reply <- ipc.Response{OK: true, Providers: items}
		return nil
	}

	entry, ok := m.ipcProvider(request.Provider)
	if !ok {
		request.Reply <- ipc.Response{OK: false, Error: fmt.Sprintf("unknown provider %q", request.Provider)}
		return nil
	}

	switch request.Op {
	case "playlist.create":
		creator, ok := entry.Provider.(provider.PlaylistCreator)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support playlist creation"}
			return nil
		}
		return func() tea.Msg {
			if request.Context != nil && request.Context.Err() != nil {
				return nil
			}
			_, err := creator.CreatePlaylist(requestContext(request.Context), request.Playlist)
			request.Reply <- ipcResponseError(err)
			return nil
		}
	case "playlist.rename":
		renamer, ok := entry.Provider.(provider.PlaylistRenamer)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support playlist renaming"}
			return nil
		}
		return func() tea.Msg {
			if request.Context != nil && request.Context.Err() != nil {
				return nil
			}
			if err := renamer.RenamePlaylist(request.Playlist, request.NewName); err != nil {
				request.Reply <- ipcResponseError(err)
				return nil
			}
			return ipcPlaylistRenamedMsg{provider: entry.Provider.Name(), oldName: request.Playlist, newName: request.NewName, reply: request.Reply}
		}
	case "playlist.delete":
		deleter, ok := entry.Provider.(provider.PlaylistDeleter)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support playlist deletion"}
			return nil
		}
		return ipcMutationCmd(request.Context, request.Reply, func() error { return deleter.DeletePlaylist(request.Playlist) })
	case "playlist.remove":
		deleter, ok := entry.Provider.(provider.PlaylistDeleter)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support removing tracks"}
			return nil
		}
		return ipcMutationCmd(request.Context, request.Reply, func() error { return deleter.RemoveTrack(request.Playlist, request.Index) })
	case "playlist.add":
		writer, ok := entry.Provider.(provider.PlaylistWriter)
		if !ok || request.Track == nil {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support adding this track"}
			return nil
		}
		track := ipcTrackFromInfo(*request.Track)
		return ipcMutationCmd(request.Context, request.Reply, func() error {
			return writer.AddTrackToPlaylist(requestContext(request.Context), request.Playlist, track)
		})
	case "playlist.add_many":
		if len(request.Tracks) == 0 {
			request.Reply <- ipc.Response{OK: false, Error: "tracks are required"}
			return nil
		}
		tracks := make([]playlist.Track, len(request.Tracks))
		for i, info := range request.Tracks {
			tracks[i] = ipcTrackFromInfo(info)
		}
		return func() tea.Msg {
			if request.Context != nil && request.Context.Err() != nil {
				return nil
			}
			added, skipped, err := provider.AddTracks(requestContext(request.Context), entry.Provider, request.Playlist, tracks)
			if err != nil {
				request.Reply <- ipcResponseError(err)
			} else {
				request.Reply <- ipc.Response{OK: true, Total: added, Items: []string{fmt.Sprintf("skipped:%d", skipped)}}
			}
			return nil
		}
	case "playlist.replace":
		saver, ok := entry.Provider.(provider.PlaylistSaver)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support replacing playlists"}
			return nil
		}
		tracks := make([]playlist.Track, len(request.Tracks))
		for i, info := range request.Tracks {
			tracks[i] = ipcTrackFromInfo(info)
		}
		return ipcMutationCmd(request.Context, request.Reply, func() error { return saver.SavePlaylist(request.Playlist, tracks) })
	case "playlist.bookmark":
		// playlist.bookmark keeps its name for old scripts. It toggles the ♥
		// favorite of the track, as f does. The track can come from the queue
		// or from a provider list, so the saved rule applies only to a queue row.
		if request.Track == nil {
			request.Reply <- ipc.Response{OK: false, Error: "track is required"}
			return nil
		}
		track := ipcTrackFromInfo(*request.Track)
		cmd, err := m.togglePlaylistTrackFavorite(track, m.savedPlaylistRow(track))
		request.Reply <- ipcResponseError(err)
		return cmd
	case "provider.playlists":
		return func() tea.Msg {
			items, err := ipcProviderPlaylistInfos(entry)
			if err != nil {
				request.Reply <- ipc.Response{OK: false, Error: err.Error()}
				return nil
			}
			page, total := ipcPage(items, request.Offset, request.Limit, 200)
			request.Reply <- ipc.Response{OK: true, Playlists: page, Total: total}
			return nil
		}
	case "provider.catalog":
		loader, ok := entry.Provider.(provider.CatalogLoader)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support catalog paging"}
			return nil
		}
		return func() tea.Msg {
			limit := request.Limit
			if limit <= 0 || limit > 200 {
				limit = 50
			}
			added, err := loader.LoadCatalogPage(request.Offset, limit)
			if err != nil {
				request.Reply <- ipcResponseError(err)
				return nil
			}
			items, err := ipcProviderPlaylistInfos(entry)
			if err != nil {
				request.Reply <- ipcResponseError(err)
				return nil
			}
			request.Reply <- ipc.Response{OK: true, Playlists: items, Total: added}
			return nil
		}
	case "provider.tracks":
		favorite := m.trackFavoriteLookup(false)
		return func() tea.Msg {
			tracks, err := entry.Provider.Tracks(request.Playlist)
			if err != nil {
				request.Reply <- ipc.Response{OK: false, Error: err.Error()}
			} else {
				page, total := ipcPage(tracks, request.Offset, request.Limit, 200)
				request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(page, favorite), Playlist: request.Playlist, Total: total}
			}
			return nil
		}
	case "provider.load":
		name := entry.Provider.Name()
		return func() tea.Msg {
			tracks, err := entry.Provider.Tracks(request.Playlist)
			return ipcProviderLoadResult{request: request, tracks: tracks, provider: name, loaded: request.Playlist, source: entry.Key + ":" + request.Playlist, err: err}
		}
	case "provider.search":
		favorite := m.trackFavoriteLookup(false)
		return func() tea.Msg {
			limit := request.Limit
			if limit <= 0 || limit > 100 {
				limit = 25
			}
			tracks, err := ipcSearchProvider(requestContext(request.Context), entry.Provider, request.Query, min(100, request.Offset+limit))
			if err != nil {
				request.Reply <- ipc.Response{OK: false, Error: err.Error()}
			} else {
				page, total := ipcPage(tracks, request.Offset, limit, 100)
				request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(page, favorite), Total: total}
			}
			return nil
		}
	case "provider.artists":
		browser, ok := entry.Provider.(provider.ArtistBrowser)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support artist browsing"}
			return nil
		}
		return func() tea.Msg {
			artists, err := browser.Artists()
			if err != nil {
				request.Reply <- ipcResponseError(err)
				return nil
			}
			items := make([]ipc.ArtistInfo, len(artists))
			for i, artist := range artists {
				items[i] = ipc.ArtistInfo{ID: artist.ID, Name: artist.Name, AlbumCount: artist.AlbumCount}
			}
			page, total := ipcPage(items, request.Offset, request.Limit, 200)
			request.Reply <- ipc.Response{OK: true, Artists: page, Total: total}
			return nil
		}
	case "provider.artist_albums":
		browser, ok := entry.Provider.(provider.ArtistBrowser)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support artist browsing"}
			return nil
		}
		return func() tea.Msg {
			albums, err := browser.ArtistAlbums(request.Artist)
			if err != nil {
				request.Reply <- ipcResponseError(err)
			} else {
				page, total := ipcPage(albums, request.Offset, request.Limit, 200)
				request.Reply <- ipc.Response{OK: true, Albums: ipcAlbumInfos(page), Total: total}
			}
			return nil
		}
	case "provider.albums":
		browser, ok := entry.Provider.(provider.AlbumBrowser)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support album browsing"}
			return nil
		}
		return func() tea.Msg {
			sortType := request.Sort
			if sortType == "" {
				sortType = browser.DefaultAlbumSort()
			}
			limit := request.Limit
			if limit <= 0 || limit > 200 {
				limit = 100
			}
			albums, err := browser.AlbumList(sortType, request.Offset, limit)
			if err != nil {
				request.Reply <- ipcResponseError(err)
				return nil
			}
			sorts := browser.AlbumSortTypes()
			sortItems := make([]ipc.SortInfo, len(sorts))
			for i, item := range sorts {
				sortItems[i] = ipc.SortInfo{ID: item.ID, Label: item.Label}
			}
			request.Reply <- ipc.Response{OK: true, Albums: ipcAlbumInfos(albums), Sorts: sortItems, Total: len(albums)}
			return nil
		}
	case "provider.album_tracks", "provider.load_album":
		loader, ok := entry.Provider.(provider.AlbumTrackLoader)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support album tracks"}
			return nil
		}
		favorite := m.trackFavoriteLookup(false)
		name := entry.Provider.Name()
		return func() tea.Msg {
			tracks, err := loader.AlbumTracks(request.Album)
			if request.Op == "provider.load_album" {
				return ipcProviderLoadResult{request: request, tracks: tracks, provider: name, loaded: "album:" + request.Album, source: entry.Key + ":album:" + request.Album, err: err}
			}
			if err != nil {
				request.Reply <- ipcResponseError(err)
			} else {
				page, total := ipcPage(tracks, request.Offset, request.Limit, 200)
				request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(page, favorite), Total: total}
			}
			return nil
		}
	case "provider.favorite":
		favorites, ok := entry.Provider.(provider.FavoriteToggler)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support favorites"}
			return nil
		}
		return func() tea.Msg {
			_, _, err := favorites.ToggleFavorite(request.Playlist)
			request.Reply <- ipcResponseError(err)
			return nil
		}
	default:
		request.Reply <- ipc.Response{OK: false, Error: "unknown provider operation"}
		return nil
	}
}

func ipcProviderPlaylistInfos(entry provider.Entry) ([]ipc.PlaylistInfo, error) {
	lists, err := entry.Provider.Playlists()
	if err != nil {
		return nil, err
	}
	items := make([]ipc.PlaylistInfo, len(lists))
	for i, list := range lists {
		items[i] = ipc.PlaylistInfo{ID: list.ID, Name: list.Name, Provider: entry.Key, Section: list.Section, TrackCount: list.TrackCount, DurationSecs: list.DurationSecs, Favorite: list.Favorite}
		if sectioned, ok := entry.Provider.(provider.SectionedList); ok {
			items[i].Favoritable = sectioned.IsFavoritableID(list.ID)
		}
	}
	return items, nil
}

func ipcMutationCmd(ctx context.Context, reply chan ipc.Response, mutate func() error) tea.Cmd {
	return func() tea.Msg {
		if ctx != nil && ctx.Err() != nil {
			return nil
		}
		reply <- ipcResponseError(mutate())
		return nil
	}
}

func ipcResponseError(err error) ipc.Response {
	if err != nil {
		return ipc.Response{OK: false, Error: err.Error()}
	}
	return ipc.Response{OK: true}
}

// stationSearcher is a station catalog that can search without the search
// state of its pane, as the radio provider does with SearchStations.
type stationSearcher interface {
	SearchStations(ctx context.Context, query string, limit int) ([]playlist.Track, error)
}

// ipcSearchProvider runs an IPC search on source with SearchTracks or
// SearchStations. Neither one changes the search of the pane. A provider with
// only a catalog search gets an error, because that search replaces the pane
// search.
func ipcSearchProvider(ctx context.Context, source playlist.Provider, query string, limit int) ([]playlist.Track, error) {
	ctx, cancel := context.WithTimeout(requestContext(ctx), 30*time.Second)
	defer cancel()
	if searcher, ok := source.(provider.Searcher); ok {
		return searcher.SearchTracks(ctx, query, limit)
	}
	if stations, ok := source.(stationSearcher); ok {
		return stations.SearchStations(ctx, query, limit)
	}
	return nil, fmt.Errorf("provider does not support search")
}

func (m *Model) handleIPCProviderLoad(result ipcProviderLoadResult) tea.Cmd {
	if result.request.Context != nil && result.request.Context.Err() != nil {
		return nil
	}
	if result.err != nil {
		result.request.Reply <- ipc.Response{OK: false, Error: result.err.Error()}
		return nil
	}
	// This replaces the queue wholesale, so retire any in-flight paged load:
	// its later pages would otherwise still pass the generation guard and
	// append onto the list loaded here.
	m.retireTracksPaging()
	m.replacePlaylist(result.tracks)
	m.setLoadedLocalPlaylist(result.provider, result.loaded)
	if m.loadedPlaylist == "" {
		m.playlistSource = result.source
	}
	m.setHeaderStateFromTracks(result.tracks)
	m.playlist.SetIndex(0)
	m.plCursor = 0
	result.request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(result.tracks, m.trackFavoriteLookup(true)), Playlist: result.request.Playlist, Total: len(result.tracks)}
	return m.playCurrentTrack()
}

// handleIPCPlaylistRenamed moves the loaded playlist to the new name when
// the local provider renamed it, as the manager rename key does.
func (m *Model) handleIPCPlaylistRenamed(msg ipcPlaylistRenamedMsg) {
	if m.localProvider != nil && msg.provider == m.localProvider.Name() {
		m.renameLoadedPlaylist(msg.oldName, msg.newName)
	}
	msg.reply <- ipc.Response{OK: true}
}

func requestContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func ipcPage[T any](items []T, offset, limit, max int) ([]T, int) {
	total := len(items)
	if offset < 0 || offset >= total {
		return nil, total
	}
	if limit <= 0 || limit > max {
		limit = max
	}
	end := min(total, offset+limit)
	return items[offset:end], total
}

// handleIPCLyrics looks up the lyrics of the track that plays in the same
// order and with the same artist and title as the lyrics overlay.
func (m *Model) handleIPCLyrics(request ipcLyricsRequest) tea.Cmd {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		request.Reply <- ipc.Response{OK: false, Error: "no current track"}
		return nil
	}
	artist, title := m.lyricsArtistTitle()
	lookups := lyricsLookups(track, m.trackLyricsSources())
	ctx := requestContext(request.Context)
	return func() tea.Msg {
		lines, err := lyrics.Lookup(ctx, track.EmbeddedLyrics, artist, title, lookups...)
		if err != nil {
			request.Reply <- ipc.Response{OK: false, Error: err.Error()}
			return nil
		}
		items := make([]ipc.LyricLine, len(lines))
		for i, line := range lines {
			items[i] = ipc.LyricLine{Start: line.Start.Seconds(), Text: line.Text}
		}
		request.Reply <- ipc.Response{OK: true, Lyrics: items}
		return nil
	}
}

func (m *Model) handleIPCHistory(request ipcHistoryRequest) tea.Cmd {
	favorite := m.trackFavoriteLookup(false)
	return func() tea.Msg {
		if m.historyStore == nil {
			request.Reply <- ipc.Response{OK: true}
			return nil
		}
		if request.Op == "history.clear" {
			if err := m.historyStore.Clear(); err != nil {
				request.Reply <- ipcResponseError(err)
				return nil
			}
			return ipcHistoryClearedMsg{reply: request.Reply}
		}
		entries, err := m.historyStore.Recent(request.Limit)
		if err != nil {
			request.Reply <- ipc.Response{OK: false, Error: err.Error()}
			return nil
		}
		items := make([]ipc.HistoryInfo, len(entries))
		for i, entry := range entries {
			items[i] = ipc.HistoryInfo{Track: ipcTrackInfo(entry.Track, i, 0, favorite(entry.Track)), PlayedAt: entry.PlayedAt.Format(time.RFC3339)}
		}
		request.Reply <- ipc.Response{OK: true, History: items}
		return nil
	}
}

// handleIPCHistoryCleared refreshes the views of the emptied history, as a
// history write does, then replies to history.clear.
func (m *Model) handleIPCHistoryCleared(msg ipcHistoryClearedMsg) tea.Cmd {
	cmd := m.refreshHistoryViews()
	msg.reply <- ipc.Response{OK: true}
	return cmd
}

func (m *Model) ipcProvider(key string) (provider.Entry, bool) {
	for _, entry := range m.providers {
		if strings.EqualFold(entry.Key, key) {
			return entry, true
		}
	}
	return provider.Entry{}, false
}

// ipcTrackInfos converts tracks for IPC. favorite reports the ♥ state of a
// track, see trackFavoriteLookup.
func ipcTrackInfos(tracks []playlist.Track, favorite func(playlist.Track) bool) []ipc.TrackInfo {
	items := make([]ipc.TrackInfo, len(tracks))
	for i, track := range tracks {
		items[i] = ipcTrackInfo(track, i, 0, favorite(track))
	}
	return items
}

func ipcAlbumInfos(albums []provider.AlbumInfo) []ipc.AlbumInfo {
	items := make([]ipc.AlbumInfo, len(albums))
	for i, album := range albums {
		items[i] = ipc.AlbumInfo{ID: album.ID, Name: album.Name, Artist: album.Artist, ArtistID: album.ArtistID, Year: album.Year, TrackCount: album.TrackCount, Genre: album.Genre}
	}
	return items
}

// ipcTrackInfo converts a track for IPC. The bookmark field keeps its JSON
// name for old scripts and reports the ♥ favorite state.
func ipcTrackInfo(track playlist.Track, index, queuePosition int, favorite bool) ipc.TrackInfo {
	return ipc.TrackInfo{
		Title: track.Title, Artist: track.Artist, Album: track.Album, Genre: track.Genre,
		Path: track.Path, AlbumArtURL: track.AlbumArtURL, Year: track.Year,
		TrackNumber: track.TrackNumber, DurationSecs: track.DurationSecs, Index: index,
		QueuePosition: queuePosition, Stream: track.Stream, Realtime: track.Realtime,
		Restricted: track.Restricted, Feed: track.Feed, Bookmark: favorite, Unplayable: track.Unplayable,
		DirSourced: track.DirSourced, ProviderMeta: maps.Clone(track.ProviderMeta),
	}
}

func ipcTrackFromInfo(info ipc.TrackInfo) playlist.Track {
	return playlist.Track{
		Title: info.Title, Artist: info.Artist, Album: info.Album, Genre: info.Genre,
		Path: info.Path, AlbumArtURL: info.AlbumArtURL, Year: info.Year,
		TrackNumber: info.TrackNumber, DurationSecs: info.DurationSecs,
		Stream: info.Stream || playlist.IsURL(info.Path), Realtime: info.Realtime,
		Restricted: info.Restricted, Feed: info.Feed, Unplayable: info.Unplayable,
		DirSourced: info.DirSourced, ProviderMeta: maps.Clone(info.ProviderMeta),
	}
}
