// Package podcast provides Apple podcast discovery and publisher RSS episodes.
package podcast

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
)

var (
	_ playlist.Provider            = (*Provider)(nil)
	_ playlist.RefreshablePlaylist = (*Provider)(nil)
	_ provider.CatalogLoader       = (*Provider)(nil)
	_ provider.CatalogSearcher     = (*Provider)(nil)
	_ provider.Searcher            = (*Provider)(nil)
	_ provider.FavoriteToggler     = (*Provider)(nil)
	_ provider.SectionedList       = (*Provider)(nil)
	_ provider.SectionTitler       = (*Provider)(nil)
	_ provider.ArtistBrowser       = (*Provider)(nil)
	_ provider.AlbumTrackLoader    = (*Provider)(nil)
	_ provider.BrowseLabeler       = (*Provider)(nil)
	_ provider.BrowseEntryProvider = (*Provider)(nil)
	_ provider.BrowseModeProvider  = (*Provider)(nil)
	_ provider.SubscriptionLister  = (*Provider)(nil)
)

// Provider keeps subscriptions available without waiting for the directory.
// Shows use their feed URL as identity, including across refreshes and restarts.
type Provider struct {
	mu               sync.Mutex
	client           *client
	country          string
	shows            map[string]show
	subscriptions    []show
	subscriptionPath string
	storeErr         error
	catalog          []show
	catalogVisible   int
	categoryCache    map[string][]show
	searchResults    []show
	searchGeneration uint64
	generation       uint64

	// progress guards its own state; it is not covered by mu.
	progress *progressStore
}

// New creates an always-available provider. Country selects Apple's charts;
// an empty or invalid two-letter country code defaults to US, without detection.
func New(country string) *Provider {
	country = strings.ToLower(strings.TrimSpace(country))
	if len(country) != 2 || country[0] < 'a' || country[0] > 'z' || country[1] < 'a' || country[1] > 'z' {
		country = "us"
	}
	p := &Provider{
		client:        newClient(),
		country:       country,
		shows:         make(map[string]show),
		categoryCache: make(map[string][]show),
		progress:      newProgressStore(),
	}
	dir, err := appdir.Dir()
	if err != nil {
		p.storeErr = fmt.Errorf("podcast subscriptions directory: %w", err)
		return p
	}
	p.subscriptionPath = filepath.Join(dir, "podcast_subscriptions.json")
	data, err := os.ReadFile(p.subscriptionPath)
	if os.IsNotExist(err) {
		return p
	}
	if err == nil {
		err = json.Unmarshal(data, &p.subscriptions)
	}
	if err != nil {
		p.subscriptions = nil
		p.storeErr = fmt.Errorf("load podcast subscriptions: %w", err)
		return p
	}
	p.subscriptions = slices.DeleteFunc(p.subscriptions, func(s show) bool { return !validHTTPURL(s.FeedURL) })
	p.subscriptions = uniqueShows(p.subscriptions)
	for _, s := range p.subscriptions {
		p.shows[s.FeedURL] = s
	}
	return p
}

func (*Provider) Name() string { return "Podcasts" }

func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var lists []playlist.PlaylistInfo
	appendShows := func(prefix, section string, shows []show) {
		for _, s := range shows {
			name := s.Title
			if p.subscribedLocked(s.FeedURL) {
				name = "[subscribed] " + name
			}
			lists = append(lists, playlist.PlaylistInfo{
				ID: prefix + ":" + s.FeedURL, Name: name, TrackCount: s.EpisodeCount, Section: section,
			})
		}
	}
	if p.searchResults != nil {
		appendShows("s", "Search Results", p.searchResults)
	} else {
		appendShows("f", "Subscriptions", p.subscriptions)
		appendShows("c", p.SectionTitle("c"), p.catalog[:p.catalogVisible])
	}
	return lists, nil
}

func (p *Provider) LoadCatalogPage(offset, limit int) (int, error) {
	if offset < 0 || limit <= 0 {
		return 0, nil
	}
	p.mu.Lock()
	catalog, generation := p.catalog, p.generation
	p.mu.Unlock()
	if catalog == nil {
		var err error
		catalog, err = p.client.top(context.Background(), p.country)
		if err != nil {
			return 0, err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.generation {
		return 0, nil
	}
	if p.catalog == nil {
		p.catalog = append([]show{}, catalog...)
		for _, s := range catalog {
			p.shows[s.FeedURL] = s
		}
	}
	catalog = p.catalog
	if offset >= len(catalog) {
		return 0, nil
	}
	end := offset + min(limit, len(catalog)-offset)
	p.catalogVisible = max(p.catalogVisible, end)
	return end - offset, nil
}

// SearchCatalog searches shows, not episodes. Enter on a result loads its feed
// into the playlist; it never fetches one episode from every matching show.
func (p *Provider) SearchCatalog(query string) (int, error) {
	p.mu.Lock()
	p.searchGeneration++
	generation := p.searchGeneration
	p.mu.Unlock()
	shows, err := p.search(context.Background(), query)
	if err != nil {
		return 0, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.searchGeneration {
		return 0, nil
	}
	for _, s := range shows {
		p.shows[s.FeedURL] = s
	}
	p.searchResults = append([]show{}, shows...)
	return len(shows), nil
}

func (p *Provider) ClearSearch() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.searchGeneration++
	p.searchResults = nil
}

func (p *Provider) IsSearching() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.searchResults != nil
}

func (p *Provider) search(ctx context.Context, query string) ([]show, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	var shows []show
	lower := strings.ToLower(query)
	if strings.Contains(query, "://") || strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:") {
		if !validHTTPURL(query) {
			return nil, fmt.Errorf("feed URL must be an absolute HTTP(S) URL without userinfo")
		}
		tracks, err := resolve.Feed(ctx, query)
		if err != nil {
			return nil, err
		}
		if len(tracks) == 0 {
			return nil, fmt.Errorf("no playable episodes found in feed")
		}
		s := show{FeedURL: query, Title: query, EpisodeCount: len(tracks)}
		if tracks[0].Artist != "" {
			s.Title = tracks[0].Artist
		}
		s.Artwork = tracks[0].AlbumArtURL
		shows = []show{s}
	} else {
		var err error
		shows, err = p.client.search(ctx, query, "")
		if err != nil {
			return nil, err
		}
	}
	return shows, nil
}

// SearchTracks uses the existing collection-result expansion for Ctrl+F and
// IPC. Feed also makes a saved result usable by consumers without that metadata.
func (p *Provider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	shows, err := p.search(ctx, query)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(shows) > limit {
		shows = shows[:limit]
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	tracks := make([]playlist.Track, 0, len(shows))
	for _, s := range shows {
		p.shows[s.FeedURL] = s
		tracks = append(tracks, playlist.Track{
			Path: s.FeedURL, Title: s.Title, Artist: s.Author, Genre: s.Genre,
			AlbumArtURL: s.Artwork, Stream: true, Feed: true,
			ProviderMeta: map[string]string{playlist.MetaKind: playlist.MetaKindAlbum, playlist.MetaAlbumID: s.FeedURL},
		})
	}
	return tracks, nil
}

func (*Provider) BrowseLabels() (string, string) { return "Genre", "Show" }

// Categories are not artists whose entire discography can be queued at once.
func (*Provider) BrowseModes() []provider.BrowseMode {
	return []provider.BrowseMode{provider.BrowseArtistAlbums}
}

func (*Provider) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{{
		ID: "browse:categories", Name: "Browse Categories", Section: "Discover",
		Mode: provider.BrowseArtistAlbums, OpenInPlaylist: true,
	}}
}

func (*Provider) Artists() ([]provider.ArtistInfo, error) {
	artists := make([]provider.ArtistInfo, 0, len(categories))
	for _, c := range categories {
		artists = append(artists, provider.ArtistInfo{ID: c.ID, Name: c.Name})
	}
	return artists, nil
}

func (p *Provider) ArtistAlbums(id string) ([]provider.AlbumInfo, error) {
	idx := slices.IndexFunc(categories, func(c category) bool { return c.ID == id })
	if idx < 0 {
		return nil, fmt.Errorf("unknown podcast category %q", id)
	}
	p.mu.Lock()
	shows, cached := p.categoryCache[id]
	generation := p.generation
	p.mu.Unlock()
	if !cached {
		var err error
		shows, err = p.client.search(context.Background(), categories[idx].Name, id)
		if err != nil {
			return nil, err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.generation {
		return nil, nil
	}
	p.categoryCache[id] = shows
	albums := make([]provider.AlbumInfo, 0, len(shows))
	for _, s := range shows {
		p.shows[s.FeedURL] = s
		albums = append(albums, provider.AlbumInfo{
			ID: s.FeedURL, Name: s.Title, Artist: s.Author, Genre: s.Genre, TrackCount: s.EpisodeCount,
		})
	}
	return albums, nil
}

func (p *Provider) Tracks(id string) ([]playlist.Track, error) {
	return p.AlbumTracksContext(context.Background(), id)
}

func (p *Provider) AlbumTracks(id string) ([]playlist.Track, error) { return p.Tracks(id) }

func (p *Provider) AlbumTracksContext(ctx context.Context, id string) ([]playlist.Track, error) {
	feedURL := showFeedURL(id)
	if feedURL == "" {
		return nil, fmt.Errorf("invalid podcast show ID %q", id)
	}
	tracks, err := resolve.Feed(ctx, feedURL)
	if err != nil {
		return nil, fmt.Errorf("podcast episodes: %w", err)
	}
	p.mu.Lock()
	s := p.shows[feedURL]
	p.mu.Unlock()
	for i := range tracks {
		if s.Title != "" {
			tracks[i].Artist = s.Title
			tracks[i].Album = s.Title
		}
		tracks[i].Genre = s.Genre
		if tracks[i].AlbumArtURL == "" {
			tracks[i].AlbumArtURL = s.Artwork
		}
	}
	return tracks, nil
}

func showFeedURL(id string) string {
	if prefix, suffix, ok := strings.Cut(id, ":"); ok && (prefix == "f" || prefix == "c" || prefix == "s") {
		id = suffix
	}
	if !validHTTPURL(id) {
		return ""
	}
	return id
}

// Subscriptions returns the subscribed shows in stored order, without a
// network call. The ID is the feed URL, which AlbumTracks accepts.
func (p *Provider) Subscriptions() []provider.SubscriptionInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	subs := make([]provider.SubscriptionInfo, 0, len(p.subscriptions))
	for _, s := range p.subscriptions {
		subs = append(subs, provider.SubscriptionInfo{ID: s.FeedURL, Name: s.Title, Author: s.Author})
	}
	return subs
}

func (p *Provider) subscribedLocked(feedURL string) bool {
	return slices.ContainsFunc(p.subscriptions, func(s show) bool { return s.FeedURL == feedURL })
}

// ToggleFavorite subscribes/unsubscribes using the existing provider favorite
// action. Failed writes leave the in-memory subscriptions unchanged.
func (p *Provider) ToggleFavorite(id string) (bool, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.storeErr != nil {
		return false, "", p.storeErr
	}
	s, ok := p.shows[showFeedURL(id)]
	if !ok {
		return false, "", fmt.Errorf("unknown podcast show %q", id)
	}
	added := !p.subscribedLocked(s.FeedURL)
	subscriptions := slices.Clone(p.subscriptions)
	if added {
		subscriptions = append([]show{s}, subscriptions...)
	} else {
		subscriptions = slices.DeleteFunc(subscriptions, func(other show) bool { return other.FeedURL == s.FeedURL })
	}
	data, err := json.MarshalIndent(subscriptions, "", "  ")
	if err == nil {
		err = fileutil.WriteFileAtomic(p.subscriptionPath, append(data, '\n'), 0o600)
	}
	if err != nil {
		return false, s.Title, fmt.Errorf("save podcast subscriptions: %w", err)
	}
	p.subscriptions = subscriptions
	return added, s.Title, nil
}

func (*Provider) IDPrefix(id string) string {
	prefix, _, _ := strings.Cut(id, ":")
	return prefix
}

func (*Provider) IsFavoritableID(id string) bool { return showFeedURL(id) != "" }

func (p *Provider) SectionTitle(prefix string) string {
	switch prefix {
	case "f":
		return "Subscriptions"
	case "c":
		return "Top Shows (" + strings.ToUpper(p.country) + ")"
	case "s":
		return "Search Results"
	case "browse":
		return "Discover"
	}
	return ""
}

func (p *Provider) Refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	p.searchGeneration++
	p.catalog = nil
	p.catalogVisible = 0
	p.categoryCache = make(map[string][]show)
	p.searchResults = nil
}

func (*Provider) CanRefreshPlaylist(id string) bool { return showFeedURL(id) != "" }
