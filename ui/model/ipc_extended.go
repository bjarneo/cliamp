package model

import (
	"context"
	"fmt"
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

type ipcProviderLoadResult struct {
	request ipc.LibraryRequestMsg
	tracks  []playlist.Track
	loaded  string
	err     error
}

type ipcURLLoadResult struct {
	request ipc.URLRequestMsg
	tracks  []playlist.Track
	err     error
}

func (m *Model) handleIPCURL(request ipc.URLRequestMsg) tea.Cmd {
	return func() tea.Msg {
		tracks, err := resolve.URL(request.URL)
		return ipcURLLoadResult{request: request, tracks: tracks, err: err}
	}
}

func (m *Model) handleIPCURLResult(result ipcURLLoadResult) tea.Cmd {
	if result.err != nil {
		result.request.Reply <- ipc.Response{OK: false, Error: result.err.Error()}
		return nil
	}
	if len(result.tracks) == 0 {
		result.request.Reply <- ipc.Response{OK: false, Error: "no tracks found at URL"}
		return nil
	}
	wasStopped := !m.player.IsPlaying()
	m.playlist.Add(result.tracks...)
	m.loadedPlaylist = ""
	m.addToHeaderState(result.tracks)
	result.request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(result.tracks), Total: len(result.tracks)}
	if wasStopped {
		return m.playCurrentTrack()
	}
	return nil
}

func (m *Model) handleIPCSave(request ipc.SaveRequestMsg) tea.Cmd {
	track, index := m.currentPlaybackTrack()
	if index < 0 {
		request.Reply <- ipc.Response{OK: false, Error: "nothing to save"}
		return nil
	}
	return func() tea.Msg {
		path, err := tracksave.Save(track)
		if err != nil {
			request.Reply <- ipc.Response{OK: false, Error: err.Error()}
		} else {
			request.Reply <- ipc.Response{OK: true, Output: path}
		}
		return nil
	}
}

func (m *Model) handleIPCQueue(request ipc.QueueRequestMsg) tea.Cmd {
	switch request.Op {
	case "queue.list":
		request.Reply <- m.ipcQueueResponse()
	case "queue.play":
		if request.Index < 0 || request.Index >= m.playlist.Len() {
			request.Reply <- ipc.Response{OK: false, Error: "queue index out of range"}
			return nil
		}
		m.playlist.SetIndex(request.Index)
		m.plCursor = request.Index
		request.Reply <- m.ipcQueueResponse()
		return m.playCurrentTrack()
	case "queue.enqueue":
		if request.Index < 0 || request.Index >= m.playlist.Len() {
			request.Reply <- ipc.Response{OK: false, Error: "queue index out of range"}
			return nil
		}
		m.playlist.Queue(request.Index)
		m.normalizeQueueOverlay()
		request.Reply <- m.ipcQueueResponse()
	case "queue.remove":
		if request.Index == m.playlist.Index() {
			m.player.Stop()
			m.clearPlaybackTrack()
		}
		if !m.playlist.Remove(request.Index) {
			request.Reply <- ipc.Response{OK: false, Error: "queue index out of range"}
			return nil
		}
		m.normalizeQueueOverlay()
		request.Reply <- m.ipcQueueResponse()
	case "queue.move":
		if !m.playlist.Move(request.Index, request.To) {
			request.Reply <- ipc.Response{OK: false, Error: "invalid queue move"}
			return nil
		}
		m.normalizeQueueOverlay()
		request.Reply <- m.ipcQueueResponse()
	case "queue.clear":
		m.player.Stop()
		m.replacePlaylist(nil)
		m.clearPlaybackTrack()
		m.loadedPlaylist = ""
		request.Reply <- m.ipcQueueResponse()
	case "track.play", "track.queue":
		if request.Track == nil || request.Track.Path == "" {
			request.Reply <- ipc.Response{OK: false, Error: "track is required"}
			return nil
		}
		track := ipcTrackFromInfo(*request.Track)
		request.Reply <- ipc.Response{OK: true}
		if request.Op == "track.play" {
			return m.playTrackImmediate(track)
		}
		return m.queueTrackNext(track)
	default:
		request.Reply <- ipc.Response{OK: false, Error: "unknown queue operation"}
	}
	return nil
}

func (m *Model) ipcQueueResponse() ipc.Response {
	tracks := m.playlist.Tracks()
	items := make([]ipc.TrackInfo, len(tracks))
	for i, track := range tracks {
		items[i] = ipcTrackInfo(track, i, m.playlist.QueuePosition(i))
	}
	return ipc.Response{OK: true, Tracks: items, Index: m.playlist.Index(), Total: len(items)}
}

func (m *Model) handleIPCLibrary(request ipc.LibraryRequestMsg) tea.Cmd {
	if request.Op == "provider.list" {
		items := make([]ipc.ProviderInfo, 0, len(m.providers))
		for _, entry := range m.providers {
			_, searchable := entry.Provider.(provider.Searcher)
			if _, ok := entry.Provider.(provider.CatalogSearcher); ok {
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
			_, err := creator.CreatePlaylist(context.Background(), request.Playlist)
			request.Reply <- ipcResponseError(err)
			return nil
		}
	case "playlist.rename":
		renamer, ok := entry.Provider.(provider.PlaylistRenamer)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support playlist renaming"}
			return nil
		}
		return ipcMutationCmd(request.Reply, func() error { return renamer.RenamePlaylist(request.Playlist, request.NewName) })
	case "playlist.delete":
		deleter, ok := entry.Provider.(provider.PlaylistDeleter)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support playlist deletion"}
			return nil
		}
		return ipcMutationCmd(request.Reply, func() error { return deleter.DeletePlaylist(request.Playlist) })
	case "playlist.remove":
		deleter, ok := entry.Provider.(provider.PlaylistDeleter)
		if !ok {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support removing tracks"}
			return nil
		}
		return ipcMutationCmd(request.Reply, func() error { return deleter.RemoveTrack(request.Playlist, request.Index) })
	case "playlist.add":
		writer, ok := entry.Provider.(provider.PlaylistWriter)
		if !ok || request.Track == nil {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support adding this track"}
			return nil
		}
		track := ipcTrackFromInfo(*request.Track)
		return ipcMutationCmd(request.Reply, func() error { return writer.AddTrackToPlaylist(context.Background(), request.Playlist, track) })
	case "playlist.bookmark":
		bookmarks, ok := entry.Provider.(provider.BookmarkSetter)
		if !ok || request.Track == nil {
			request.Reply <- ipc.Response{OK: false, Error: "provider does not support bookmarks"}
			return nil
		}
		return ipcMutationCmd(request.Reply, func() error { return bookmarks.SetBookmarkByPath(request.Playlist, request.Track.Path) })
	case "provider.playlists":
		return func() tea.Msg {
			items, err := ipcProviderPlaylistInfos(entry)
			if err != nil {
				request.Reply <- ipc.Response{OK: false, Error: err.Error()}
				return nil
			}
			request.Reply <- ipc.Response{OK: true, Playlists: items}
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
		return func() tea.Msg {
			tracks, err := entry.Provider.Tracks(request.Playlist)
			if err != nil {
				request.Reply <- ipc.Response{OK: false, Error: err.Error()}
			} else {
				request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(tracks), Total: len(tracks)}
			}
			return nil
		}
	case "provider.load":
		return func() tea.Msg {
			tracks, err := entry.Provider.Tracks(request.Playlist)
			return ipcProviderLoadResult{request: request, tracks: tracks, loaded: request.Playlist, err: err}
		}
	case "provider.search":
		return func() tea.Msg {
			limit := request.Limit
			if limit <= 0 || limit > 100 {
				limit = 25
			}
			tracks, err := ipcSearchProvider(entry.Provider, request.Query, limit)
			if err != nil {
				request.Reply <- ipc.Response{OK: false, Error: err.Error()}
			} else {
				request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(tracks), Total: len(tracks)}
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
			request.Reply <- ipc.Response{OK: true, Artists: items, Total: len(items)}
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
				request.Reply <- ipc.Response{OK: true, Albums: ipcAlbumInfos(albums), Total: len(albums)}
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
		return func() tea.Msg {
			tracks, err := loader.AlbumTracks(request.Album)
			if request.Op == "provider.load_album" {
				return ipcProviderLoadResult{request: request, tracks: tracks, loaded: "album:" + request.Album, err: err}
			}
			if err != nil {
				request.Reply <- ipcResponseError(err)
			} else {
				request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(tracks), Total: len(tracks)}
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

func ipcProviderPlaylistInfos(entry ProviderEntry) ([]ipc.PlaylistInfo, error) {
	lists, err := entry.Provider.Playlists()
	if err != nil {
		return nil, err
	}
	items := make([]ipc.PlaylistInfo, len(lists))
	for i, list := range lists {
		items[i] = ipc.PlaylistInfo{ID: list.ID, Name: list.Name, Provider: entry.Key, Section: list.Section, TrackCount: list.TrackCount, DurationSecs: list.DurationSecs}
		if sectioned, ok := entry.Provider.(provider.SectionedList); ok {
			items[i].Favoritable = sectioned.IsFavoritableID(list.ID)
			items[i].Favorite = strings.HasPrefix(list.ID, "f:")
		}
	}
	return items, nil
}

func ipcMutationCmd(reply chan ipc.Response, mutate func() error) tea.Cmd {
	return func() tea.Msg {
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

func ipcSearchProvider(source playlist.Provider, query string, limit int) ([]playlist.Track, error) {
	if searcher, ok := source.(provider.Searcher); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return searcher.SearchTracks(ctx, query, limit)
	}
	catalog, ok := source.(provider.CatalogSearcher)
	if !ok {
		return nil, fmt.Errorf("provider does not support search")
	}
	if _, err := catalog.SearchCatalog(query); err != nil {
		return nil, err
	}
	defer catalog.ClearSearch()
	lists, err := source.Playlists()
	if err != nil {
		return nil, err
	}
	if len(lists) > limit {
		lists = lists[:limit]
	}
	tracks := make([]playlist.Track, 0, len(lists))
	for _, list := range lists {
		items, err := source.Tracks(list.ID)
		if err == nil && len(items) > 0 {
			tracks = append(tracks, items[0])
		}
	}
	return tracks, nil
}

func (m *Model) handleIPCProviderLoad(result ipcProviderLoadResult) tea.Cmd {
	if result.err != nil {
		result.request.Reply <- ipc.Response{OK: false, Error: result.err.Error()}
		return nil
	}
	m.replacePlaylist(result.tracks)
	m.loadedPlaylist = result.loaded
	m.setHeaderStateFromTracks(result.tracks)
	m.playlist.SetIndex(0)
	m.plCursor = 0
	result.request.Reply <- ipc.Response{OK: true, Tracks: ipcTrackInfos(result.tracks), Total: len(result.tracks)}
	return m.playCurrentTrack()
}

func (m *Model) handleIPCLyrics(request ipc.LyricsRequestMsg) tea.Cmd {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		request.Reply <- ipc.Response{OK: false, Error: "no current track"}
		return nil
	}
	return func() tea.Msg {
		lines := lyrics.ParseEmbedded(track.EmbeddedLyrics)
		var err error
		if len(lines) == 0 {
			lines, err = lyrics.Fetch(track.Artist, track.Title)
		}
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

func (m *Model) handleIPCHistory(request ipc.HistoryRequestMsg) tea.Cmd {
	return func() tea.Msg {
		if m.historyStore == nil {
			request.Reply <- ipc.Response{OK: true}
			return nil
		}
		if request.Op == "history.clear" {
			request.Reply <- ipcResponseError(m.historyStore.Clear())
			return nil
		}
		entries, err := m.historyStore.Recent(request.Limit)
		if err != nil {
			request.Reply <- ipc.Response{OK: false, Error: err.Error()}
			return nil
		}
		items := make([]ipc.HistoryInfo, len(entries))
		for i, entry := range entries {
			items[i] = ipc.HistoryInfo{Track: ipcTrackInfo(entry.Track, i, 0), PlayedAt: entry.PlayedAt.Format(time.RFC3339)}
		}
		request.Reply <- ipc.Response{OK: true, History: items}
		return nil
	}
}

func (m *Model) ipcProvider(key string) (ProviderEntry, bool) {
	for _, entry := range m.providers {
		if strings.EqualFold(entry.Key, key) {
			return entry, true
		}
	}
	return ProviderEntry{}, false
}

func ipcTrackInfos(tracks []playlist.Track) []ipc.TrackInfo {
	items := make([]ipc.TrackInfo, len(tracks))
	for i, track := range tracks {
		items[i] = ipcTrackInfo(track, i, 0)
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

func ipcTrackInfo(track playlist.Track, index, queuePosition int) ipc.TrackInfo {
	return ipc.TrackInfo{
		Title: track.Title, Artist: track.Artist, Album: track.Album, Genre: track.Genre,
		Path: track.Path, AlbumArtURL: track.AlbumArtURL, Year: track.Year,
		TrackNumber: track.TrackNumber, DurationSecs: track.DurationSecs, Index: index,
		QueuePosition: queuePosition, Stream: track.Stream, Realtime: track.Realtime,
		Bookmark: track.Bookmark, Unplayable: track.Unplayable,
	}
}

func ipcTrackFromInfo(info ipc.TrackInfo) playlist.Track {
	return playlist.Track{
		Title: info.Title, Artist: info.Artist, Album: info.Album, Genre: info.Genre,
		Path: info.Path, AlbumArtURL: info.AlbumArtURL, Year: info.Year,
		TrackNumber: info.TrackNumber, DurationSecs: info.DurationSecs,
		Stream: info.Stream || playlist.IsURL(info.Path), Realtime: info.Realtime,
		Bookmark: info.Bookmark, Unplayable: info.Unplayable,
	}
}
