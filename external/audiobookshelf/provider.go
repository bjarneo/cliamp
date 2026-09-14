package audiobookshelf

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

var (
	_ provider.ArtistBrowser    = (*Provider)(nil)
	_ provider.AlbumBrowser     = (*Provider)(nil)
	_ provider.AlbumTrackLoader = (*Provider)(nil)
	_ provider.PlaybackReporter = (*Provider)(nil)
	_ provider.Searcher         = (*Provider)(nil)
	_ provider.ProgressReporter = (*Provider)(nil)
	_ provider.ResumeTarget     = (*Provider)(nil)
	_ provider.TrackPosition    = (*Provider)(nil)
	_ provider.BrowseLabeler    = (*Provider)(nil)
)

const (
	prefixBook    = "b:"
	prefixPodcast = "p:"
	prefixHost    = "h:"

	sectionAudiobooks = "Audiobooks"
	sectionPodcasts   = "Podcasts"

	// chapterSlack absorbs rounding between chapter marks and file boundaries.
	chapterSlack = 1.0

	SortBooksByTitle  = "title"
	SortBooksByAdded  = "addedAt"
	SortBooksByAuthor = "author"

	searchItemLimit = 10
)

var bookSortTypes = []provider.SortType{
	{ID: SortBooksByTitle, Label: "By Title"},
	{ID: SortBooksByAdded, Label: "Recently Added"},
	{ID: SortBooksByAuthor, Label: "By Author"},
}

// Provider implements playlist.Provider for an Audiobookshelf server.
// Playlists() returns books and podcast shows in two sections; Tracks()
// returns a book's audio files or a show's episodes.
type Provider struct {
	client        *Client
	mu            sync.Mutex
	playlistCache []playlist.PlaylistInfo
	trackCache    map[string][]playlist.Track
	bookCache     []LibraryItem
	showCache     []LibraryItem
}

func newProvider(client *Client) *Provider {
	return &Provider{client: client}
}

// NewFromConfig returns a Provider from an AudiobookshelfConfig, or nil when
// the server URL or credentials are missing.
func NewFromConfig(cfg config.AudiobookshelfConfig) *Provider {
	if !cfg.IsSet() {
		return nil
	}
	return newProvider(NewClient(cfg.URL, cfg.Token, cfg.User, cfg.Password, cfg.Libraries))
}

// Name returns the display name used in the provider selector.
func (p *Provider) Name() string { return "Audiobookshelf" }

// Refresh clears cached catalog data so the next call re-fetches from the
// server. Implements playlist.Refresher.
func (p *Provider) Refresh() {
	p.mu.Lock()
	p.playlistCache = nil
	p.trackCache = nil
	p.bookCache = nil
	p.showCache = nil
	p.mu.Unlock()
	p.client.ClearCache()
}

// Playlists returns every book, then every podcast show, each tagged with its
// section. Results are cached after the first successful call.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	if p.playlistCache != nil {
		cached := p.playlistCache
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	libs, err := p.client.Libraries()
	if err != nil {
		return nil, fmt.Errorf("audiobookshelf: list libraries: %w", err)
	}

	var books, shows []playlist.PlaylistInfo
	for _, lib := range libs {
		items, err := p.client.Items(lib.ID)
		if err != nil {
			return nil, fmt.Errorf("audiobookshelf: list items for library %s: %w", lib.ID, err)
		}
		for _, it := range items {
			switch mediaTypeOf(lib, it) {
			case mediaTypePodcast:
				shows = append(shows, playlist.PlaylistInfo{
					ID:         prefixPodcast + it.ID,
					Name:       it.Media.Metadata.Title,
					TrackCount: it.Media.NumEpisodes,
					Section:    sectionPodcasts,
				})
			default:
				books = append(books, playlist.PlaylistInfo{
					ID:           prefixBook + it.ID,
					Name:         bookLabel(it),
					TrackCount:   it.Media.NumTracks,
					DurationSecs: int(it.Media.Duration),
					Section:      sectionAudiobooks,
				})
			}
		}
	}

	out := make([]playlist.PlaylistInfo, 0, len(books)+len(shows))
	out = append(out, books...)
	out = append(out, shows...)
	p.mu.Lock()
	p.playlistCache = out
	p.mu.Unlock()
	return out, nil
}

// Tracks returns a book's audio files or a podcast show's episodes.
// Results are cached per playlist id.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	p.mu.Lock()
	if cached, ok := p.trackCache[playlistID]; ok {
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	itemID, podcast := parsePlaylistID(playlistID)
	if itemID == "" {
		return nil, fmt.Errorf("audiobookshelf: unknown playlist id %q", playlistID)
	}

	item, err := p.client.Item(itemID)
	if err != nil {
		return nil, fmt.Errorf("audiobookshelf: load item %s: %w", itemID, err)
	}

	var out []playlist.Track
	if podcast {
		out = p.episodeTracks(item)
	} else {
		out = p.bookTracks(item)
	}

	p.mu.Lock()
	if p.trackCache == nil {
		p.trackCache = make(map[string][]playlist.Track)
	}
	p.trackCache[playlistID] = out
	p.mu.Unlock()
	return out, nil
}

func (p *Provider) bookTracks(item LibraryItem) []playlist.Track {
	md := item.Media.Metadata
	year := parseYear(md.PublishedYear)
	total := strconv.FormatFloat(item.Media.Duration, 'f', -1, 64)

	out := make([]playlist.Track, 0, len(item.Media.Tracks))
	for _, at := range item.Media.Tracks {
		out = append(out, playlist.Track{
			Path:         p.client.StreamURL(item.ID, at.Ino),
			Title:        fileTitle(at, item.Media.Chapters),
			Artist:       md.AuthorName,
			Album:        md.Title,
			Year:         year,
			TrackNumber:  at.Index,
			DurationSecs: int(at.Duration),
			Stream:       true,
			ProviderMeta: map[string]string{
				provider.MetaAudiobookshelfID:     item.ID,
				provider.MetaAudiobookshelfOffset: strconv.FormatFloat(at.StartOffset, 'f', -1, 64),
				provider.MetaAudiobookshelfTotal:  total,
			},
		})
	}
	return out
}

func (p *Provider) episodeTracks(item LibraryItem) []playlist.Track {
	md := item.Media.Metadata
	eps := append([]Episode(nil), item.Media.Episodes...)
	sort.SliceStable(eps, func(i, j int) bool { return eps[i].PublishedAt > eps[j].PublishedAt })

	out := make([]playlist.Track, 0, len(eps))
	for _, ep := range eps {
		duration := ep.AudioTrack.Duration
		if duration <= 0 {
			duration = ep.AudioFile.Duration
		}
		ino := ep.AudioFile.Ino
		if ino == "" {
			ino = ep.AudioTrack.Ino
		}
		out = append(out, playlist.Track{
			Path:         p.client.StreamURL(item.ID, ino),
			Title:        ep.Title,
			Artist:       md.Author,
			Album:        md.Title,
			TrackNumber:  ep.Index,
			DurationSecs: int(duration),
			Stream:       true,
			ProviderMeta: map[string]string{
				provider.MetaAudiobookshelfID:      item.ID,
				provider.MetaAudiobookshelfEpisode: ep.ID,
				provider.MetaAudiobookshelfTotal:   strconv.FormatFloat(duration, 'f', -1, 64),
			},
		})
	}
	return out
}

// Artists returns book authors merged with podcast hosts, sorted by name. A
// host who is also a book author keeps the server's author id.
func (p *Provider) Artists() ([]provider.ArtistInfo, error) {
	libs, err := p.client.Libraries()
	if err != nil {
		return nil, fmt.Errorf("audiobookshelf: list libraries: %w", err)
	}

	var out []provider.ArtistInfo
	byName := make(map[string]int)
	for _, lib := range libs {
		if lib.MediaType != mediaTypeBook {
			continue
		}
		authors, err := p.client.Authors(lib.ID)
		if err != nil {
			return nil, fmt.Errorf("audiobookshelf: list authors for library %s: %w", lib.ID, err)
		}
		for _, a := range authors {
			if i, ok := byName[strings.ToLower(a.Name)]; ok {
				out[i].AlbumCount += a.NumBooks
				continue
			}
			byName[strings.ToLower(a.Name)] = len(out)
			out = append(out, provider.ArtistInfo{ID: a.ID, Name: a.Name, AlbumCount: a.NumBooks})
		}
	}

	shows, err := p.shows()
	if err != nil {
		return nil, err
	}
	for _, it := range shows {
		host := itemAuthor(it)
		if host == "" {
			continue
		}
		if i, ok := byName[strings.ToLower(host)]; ok {
			out[i].AlbumCount++
			continue
		}
		byName[strings.ToLower(host)] = len(out)
		out = append(out, provider.ArtistInfo{ID: hostID(host), Name: host, AlbumCount: 1})
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// ArtistAlbums returns one author's books and the shows they host, sorted by
// title.
func (p *Provider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	var items []LibraryItem
	name := strings.TrimPrefix(artistID, prefixHost)
	if !strings.HasPrefix(artistID, prefixHost) {
		author, err := p.client.Author(artistID)
		if err != nil {
			return nil, fmt.Errorf("audiobookshelf: list books for author %s: %w", artistID, err)
		}
		items = append(items, author.LibraryItems...)
		name = author.Name
	}

	shows, err := p.shows()
	if err != nil {
		return nil, err
	}
	for _, it := range shows {
		if name != "" && strings.EqualFold(itemAuthor(it), name) {
			items = append(items, it)
		}
	}
	sortItems(items, SortBooksByTitle)

	out := make([]provider.AlbumInfo, 0, len(items))
	for _, it := range items {
		out = append(out, albumInfo(it, artistID))
	}
	return out, nil
}

// AlbumList returns one page of the full catalog: books and podcast shows.
func (p *Provider) AlbumList(sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	books, err := p.books()
	if err != nil {
		return nil, err
	}
	shows, err := p.shows()
	if err != nil {
		return nil, err
	}
	sorted := make([]LibraryItem, 0, len(books)+len(shows))
	sorted = append(sorted, books...)
	sorted = append(sorted, shows...)
	sortItems(sorted, sortType)

	if offset < 0 {
		offset = 0
	}
	if offset >= len(sorted) {
		return nil, nil
	}
	end := len(sorted)
	if size > 0 && offset+size < end {
		end = offset + size
	}

	out := make([]provider.AlbumInfo, 0, end-offset)
	for _, it := range sorted[offset:end] {
		out = append(out, albumInfo(it, ""))
	}
	return out, nil
}

func (p *Provider) AlbumSortTypes() []provider.SortType { return bookSortTypes }

func (p *Provider) DefaultAlbumSort() string { return SortBooksByTitle }

// AlbumTracks returns the audio files of one book or the episodes of one show.
func (p *Provider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	return p.Tracks(albumID)
}

// SearchTracks searches every visible library and returns the tracks of the
// matching books and shows. Implements provider.Searcher.
func (p *Provider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	if limit <= 0 {
		limit = 50
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("audiobookshelf: search tracks: %w", err)
	}
	libs, err := p.client.Libraries()
	if err != nil {
		return nil, fmt.Errorf("audiobookshelf: list libraries: %w", err)
	}

	var out []playlist.Track
	for _, lib := range libs {
		if err := ctx.Err(); err != nil {
			return out, fmt.Errorf("audiobookshelf: search tracks: %w", err)
		}
		hits, err := p.client.Search(lib.ID, query, searchItemLimit)
		if err != nil {
			return nil, fmt.Errorf("audiobookshelf: search library %s: %w", lib.ID, err)
		}
		for _, hit := range hits {
			if err := ctx.Err(); err != nil {
				return out, fmt.Errorf("audiobookshelf: search tracks: %w", err)
			}
			id := prefixBook + hit.ID
			if mediaTypeOf(lib, hit) == mediaTypePodcast {
				id = prefixPodcast + hit.ID
			}
			tracks, err := p.Tracks(id)
			if err != nil {
				continue
			}
			out = append(out, tracks...)
			if len(out) >= limit {
				return out[:limit], nil
			}
		}
	}
	return out, nil
}

// books returns every item in every book library, cached after the first call.
func (p *Provider) books() ([]LibraryItem, error) {
	p.mu.Lock()
	cached := p.bookCache
	p.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	out, err := p.itemsOfType(mediaTypeBook)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.bookCache = out
	p.mu.Unlock()
	return out, nil
}

func (p *Provider) itemsOfType(mediaType string) ([]LibraryItem, error) {
	libs, err := p.client.Libraries()
	if err != nil {
		return nil, fmt.Errorf("audiobookshelf: list libraries: %w", err)
	}

	out := make([]LibraryItem, 0)
	for _, lib := range libs {
		if lib.MediaType != mediaType {
			continue
		}
		items, err := p.client.Items(lib.ID)
		if err != nil {
			return nil, fmt.Errorf("audiobookshelf: list items for library %s: %w", lib.ID, err)
		}
		for _, it := range items {
			// The list endpoint may omit mediaType.
			if it.MediaType == "" {
				it.MediaType = mediaType
			}
			out = append(out, it)
		}
	}
	return out, nil
}

func (p *Provider) shows() ([]LibraryItem, error) {
	p.mu.Lock()
	cached := p.showCache
	p.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	out, err := p.itemsOfType(mediaTypePodcast)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.showCache = out
	p.mu.Unlock()
	return out, nil
}

func hostID(name string) string { return prefixHost + name }

// itemAuthor returns a book's author or a show's host.
func itemAuthor(it LibraryItem) string {
	if name := strings.TrimSpace(it.Media.Metadata.AuthorName); name != "" {
		return name
	}
	return strings.TrimSpace(it.Media.Metadata.Author)
}

func albumInfo(it LibraryItem, artistID string) provider.AlbumInfo {
	md := it.Media.Metadata
	if it.MediaType == mediaTypePodcast {
		return provider.AlbumInfo{
			ID:         prefixPodcast + it.ID,
			Name:       md.Title,
			Artist:     itemAuthor(it),
			ArtistID:   hostID(itemAuthor(it)),
			TrackCount: it.Media.NumEpisodes,
		}
	}
	if artistID == "" && len(md.Authors) > 0 {
		artistID = md.Authors[0].ID
	}
	return provider.AlbumInfo{
		ID:         prefixBook + it.ID,
		Name:       md.Title,
		Artist:     md.AuthorName,
		ArtistID:   artistID,
		Year:       parseYear(md.PublishedYear),
		TrackCount: it.Media.NumTracks,
	}
}

func sortItems(items []LibraryItem, sortType string) {
	switch sortType {
	case SortBooksByAdded:
		sort.SliceStable(items, func(i, j int) bool { return items[i].AddedAt > items[j].AddedAt })
	case SortBooksByAuthor:
		sort.SliceStable(items, func(i, j int) bool {
			ai := strings.ToLower(itemAuthor(items[i]))
			aj := strings.ToLower(itemAuthor(items[j]))
			if ai == aj {
				return strings.ToLower(items[i].Media.Metadata.Title) < strings.ToLower(items[j].Media.Metadata.Title)
			}
			return ai < aj
		})
	default:
		sort.SliceStable(items, func(i, j int) bool {
			return strings.ToLower(items[i].Media.Metadata.Title) < strings.ToLower(items[j].Media.Metadata.Title)
		})
	}
}

func parsePlaylistID(id string) (string, bool) {
	switch {
	case strings.HasPrefix(id, prefixBook):
		return strings.TrimPrefix(id, prefixBook), false
	case strings.HasPrefix(id, prefixPodcast):
		return strings.TrimPrefix(id, prefixPodcast), true
	}
	return "", false
}

func mediaTypeOf(lib Library, it LibraryItem) string {
	if it.MediaType != "" {
		return it.MediaType
	}
	return lib.MediaType
}

func bookLabel(it LibraryItem) string {
	md := it.Media.Metadata
	if md.AuthorName == "" {
		return md.Title
	}
	return md.AuthorName + " — " + md.Title
}

// fileTitle prefers the chapter name when a file covers exactly one chapter,
// then the file's embedded title, then its filename.
func fileTitle(at AudioTrack, chapters []Chapter) string {
	if title := soleChapterTitle(at, chapters); title != "" {
		return title
	}
	if at.Title != "" {
		return at.Title
	}
	return at.Metadata.Filename
}

func soleChapterTitle(at AudioTrack, chapters []Chapter) string {
	if at.Duration <= 0 {
		return ""
	}
	start, end := at.StartOffset, at.StartOffset+at.Duration
	found := ""
	for _, ch := range chapters {
		if ch.End <= start+chapterSlack || ch.Start >= end-chapterSlack {
			continue
		}
		if found != "" {
			return ""
		}
		found = ch.Title
	}
	return found
}

func parseYear(s string) int {
	year, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return year
}

func metaFloat(t playlist.Track, key string) float64 {
	v, err := strconv.ParseFloat(t.Meta(key), 64)
	if err != nil {
		return 0
	}
	return v
}

// finishSlack is how close to the end of a book counts as finished.
const finishSlack = 5.0

func (p *Provider) CanReportPlayback(track playlist.Track) bool {
	return track.Meta(provider.MetaAudiobookshelfID) != ""
}

func (p *Provider) CanTrackPosition(track playlist.Track) bool {
	return p.CanReportPlayback(track)
}

func (p *Provider) ReportNowPlaying(track playlist.Track, position time.Duration, _ bool) error {
	if position <= 0 {
		return nil
	}
	return p.report(track, position, false)
}

func (p *Provider) ReportScrobble(track playlist.Track, elapsed, _ time.Duration, _ bool) error {
	return p.report(track, elapsed, true)
}

// TrackPosition returns the server-side position for track, read live so a
// revisited track reflects progress reported during this session.
func (p *Provider) TrackPosition(ctx context.Context, track playlist.Track) time.Duration {
	itemID := track.Meta(provider.MetaAudiobookshelfID)
	if itemID == "" {
		return 0
	}
	list, err := p.client.Progress(ctx)
	if err != nil {
		return 0
	}
	mp, ok := findProgress(list, itemID, track.Meta(provider.MetaAudiobookshelfEpisode))
	if !ok || mp.IsFinished || mp.CurrentTime <= 0 {
		return 0
	}
	offset := mp.CurrentTime - metaFloat(track, provider.MetaAudiobookshelfOffset)
	if offset <= 0 || (track.DurationSecs > 0 && offset >= float64(track.DurationSecs)) {
		return 0
	}
	return time.Duration(offset * float64(time.Second))
}

// ReportProgress pushes an interim listening position.
func (p *Provider) ReportProgress(track playlist.Track, position time.Duration) error {
	return p.report(track, position, false)
}

func (p *Provider) report(track playlist.Track, position time.Duration, complete bool) error {
	itemID := track.Meta(provider.MetaAudiobookshelfID)
	if itemID == "" {
		return nil
	}
	offset := metaFloat(track, provider.MetaAudiobookshelfOffset)
	total := metaFloat(track, provider.MetaAudiobookshelfTotal)
	if total <= 0 {
		total = float64(track.DurationSecs)
	}
	current := offset + position.Seconds()
	finished := complete && total > 0 && current >= total-finishSlack
	episodeID := track.Meta(provider.MetaAudiobookshelfEpisode)
	if err := p.client.UpdateProgress(itemID, episodeID, current, total, finished); err != nil {
		return fmt.Errorf("audiobookshelf: update progress for item %s: %w", itemID, err)
	}
	return nil
}

// ResumeTarget returns where to continue an item: the track index and the
// offset into it. Podcast shows resume the first in-progress episode in
// display order.
func (p *Provider) ResumeTarget(playlistID string, tracks []playlist.Track) (int, time.Duration) {
	itemID, podcast := parsePlaylistID(playlistID)
	if itemID == "" || len(tracks) == 0 {
		return 0, 0
	}

	list, err := p.client.Progress(context.Background())
	if err != nil {
		return 0, 0
	}

	if podcast {
		for i, t := range tracks {
			mp, ok := findProgress(list, itemID, t.Meta(provider.MetaAudiobookshelfEpisode))
			if !ok || mp.IsFinished || mp.CurrentTime <= 0 {
				continue
			}
			if t.DurationSecs == 0 {
				return i, 0
			}
			return i, time.Duration(mp.CurrentTime * float64(time.Second))
		}
		return 0, 0
	}

	mp, ok := findProgress(list, itemID, "")
	if !ok || mp.IsFinished || mp.CurrentTime <= 0 {
		return 0, 0
	}
	for i, t := range tracks {
		start := metaFloat(t, provider.MetaAudiobookshelfOffset)
		end := start + float64(t.DurationSecs)
		if mp.CurrentTime < end || i == len(tracks)-1 {
			offset := mp.CurrentTime - start
			if offset <= 0 || (t.DurationSecs > 0 && offset >= float64(t.DurationSecs)) {
				return i, 0
			}
			return i, time.Duration(offset * float64(time.Second))
		}
	}
	return 0, 0
}

func findProgress(list []MediaProgress, itemID, episodeID string) (MediaProgress, bool) {
	for _, mp := range list {
		if mp.LibraryItemID == itemID && mp.EpisodeID == episodeID {
			return mp, true
		}
	}
	return MediaProgress{}, false
}

// BrowseLabels returns vocabulary covering both media types.
func (p *Provider) BrowseLabels() (string, string) { return "Author", "Title" }
