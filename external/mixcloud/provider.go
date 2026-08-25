// Package mixcloud implements Mixcloud catalog browsing through the public
// REST API and playback through cliamp's existing yt-dlp pipeline.
package mixcloud

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
)

const (
	browseShowsID      = "mixcloud:browse:shows"
	browseCreatorsID   = "mixcloud:browse:creators"
	browseGenresID     = "mixcloud:browse:genres"
	recentID           = "discover:latest"
	popularID          = "discover:popular"
	streamID           = "account:stream"
	activityID         = "account:activity"
	uploadsID          = "account:uploads"
	favoritesID        = "account:favorites"
	listensID          = "account:listens"
	listenLaterID      = "account:listen-later"
	collectionPrefix   = "collection:"
	stylePrefix        = "style:"
	creatorUploadsID   = "creator:uploads:"
	creatorFavoritesID = "creator:favorites:"
	streamConcurrency  = 6
	accountSection     = "Your Mixcloud"
)

// Setup and provider limits are exported so the onboarding UI and runtime
// normalization cannot drift apart.
const (
	MinItems              = 1
	DefaultMaxItems       = 100
	MaxItemsLimit         = 500
	DefaultStreamCreators = 20
	MaxStreamCreators     = 100
)

var (
	_ playlist.Provider             = (*Provider)(nil)
	_ playlist.Refresher            = (*Provider)(nil)
	_ provider.Searcher             = (*Provider)(nil)
	_ provider.ArtistBrowser        = (*Provider)(nil)
	_ provider.TrackArtistResolver  = (*Provider)(nil)
	_ provider.BrowseEntryProvider  = (*Provider)(nil)
	_ provider.AlbumBrowser         = (*Provider)(nil)
	_ provider.AlbumTrackLoader     = (*Provider)(nil)
	_ provider.BrowseLabeler        = (*Provider)(nil)
	_ provider.GenreBrowser         = (*Provider)(nil)
	_ provider.GenreFavoriteToggler = (*Provider)(nil)
	_ provider.GenreSearcher        = (*Provider)(nil)
)

// DefaultStyles seeds useful music-style discovery when styles is omitted.
var DefaultStyles = []string{
	"ambient", "chillout", "deep-house", "disco", "drum-bass",
	"electronica", "funk", "hip-hop", "house", "jazz", "reggae",
	"soul", "techno", "trance", "world",
}

// Config holds settings for the Mixcloud provider.
type Config struct {
	Enabled        bool
	Username       string
	AccessToken    string
	CookiesFrom    string
	Styles         []string
	StylesSet      bool
	MaxItems       int
	StreamCreators int
	SaveStyles     func([]string) error
}

// IsSet reports whether Mixcloud should be registered.
func (c Config) IsSet() bool { return c.Enabled }

// Provider exposes Mixcloud shows as cliamp tracks. It deliberately stores
// stable Mixcloud page URLs rather than extracted media URLs; yt-dlp resolves
// the current stream only when playback begins, so queue entries do not expire.
type Provider struct {
	client         *client
	username       string
	styles         []string
	maxItems       int
	streamCreators int
	saveStyles     func([]string) error

	mu               sync.Mutex
	resolvedUsername string
}

// NewFromConfig returns nil unless Mixcloud is explicitly enabled.
func NewFromConfig(cfg Config) *Provider {
	if !cfg.Enabled {
		return nil
	}
	resolve.SetYTDLCookiesForHost("mixcloud.com", cfg.CookiesFrom)
	maxItems := cfg.MaxItems
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	maxItems = min(maxItems, MaxItemsLimit)
	streamCreators := cfg.StreamCreators
	if streamCreators <= 0 {
		streamCreators = DefaultStreamCreators
	}
	streamCreators = min(streamCreators, MaxStreamCreators)

	styles := normalizeStyles(cfg.Styles)
	if len(styles) == 0 && !cfg.StylesSet {
		styles = slices.Clone(DefaultStyles)
	}
	return &Provider{
		client:         newClient(cfg.AccessToken),
		username:       strings.TrimSpace(cfg.Username),
		styles:         styles,
		maxItems:       maxItems,
		streamCreators: streamCreators,
		saveStyles:     cfg.SaveStyles,
	}
}

func (p *Provider) Name() string { return "Mixcloud" }

// BrowseEntries makes the same show, creator, and genre routes available from the
// provider pane that users can otherwise reach through the N browser.
func (p *Provider) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{
		{
			ID: browseCreatorsID, Name: "Creators", Section: accountSection,
			Mode: provider.BrowseArtistAlbums, AfterID: favoritesID,
			AfterSection: accountSection, OpenInPlaylist: true,
		},
		{
			ID: browseShowsID, Name: "Shows", Section: "Browse",
			Mode: provider.BrowseAlbums, AfterSection: accountSection, OpenInPlaylist: true,
		},
		{
			ID: browseGenresID, Name: "Genres", Section: "Browse",
			Mode: provider.BrowseGenres, AfterSection: accountSection, OpenInPlaylist: true,
		},
	}
}

// Refresh clears account identity discovered through /me/. Catalog and track
// pages are intentionally not cached, so subsequent loads always fetch fresh
// releases and retain no expiring audio URLs.
func (p *Provider) Refresh() {
	p.mu.Lock()
	p.resolvedUsername = ""
	p.mu.Unlock()
}

// Playlists returns discovery views, account views, Mixcloud collections, and
// latest/popular views for each configured music style.
func (p *Provider) Playlists() ([]playlist.PlaylistInfo, error) {
	publicLists := []playlist.PlaylistInfo{
		{ID: recentID, Name: "Recent Releases", Section: "Discover"},
		{ID: popularID, Name: "Popular", Section: "Discover"},
	}
	styleLists := p.stylePlaylists()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !p.hasAccount() {
		return append(publicLists, styleLists...), nil
	}

	username, err := p.accountUsername(ctx)
	if err != nil {
		return append(publicLists, styleLists...), fmt.Errorf("mixcloud: account views unavailable: %w", err)
	}
	collections, err := p.client.playlists(ctx, p.accountConnection(username, "playlists"), p.maxItems)
	if err != nil {
		return append(publicLists, styleLists...), fmt.Errorf("mixcloud: account views unavailable: list collections: %w", err)
	}

	lists := []playlist.PlaylistInfo{
		{ID: streamID, Name: "Stream (Following Releases)", Section: accountSection},
		{ID: favoritesID, Name: "Favorites", Section: accountSection},
		{ID: uploadsID, Name: "Uploads", Section: accountSection},
		{ID: activityID, Name: "Profile Activity", Section: accountSection},
		{ID: listensID, Name: "Listening History", Section: accountSection},
	}
	if p.client.accessToken != "" {
		lists = append(lists, playlist.PlaylistInfo{ID: listenLaterID, Name: "Listen Later", Section: accountSection})
	}
	for _, collection := range collections {
		lists = append(lists, playlist.PlaylistInfo{
			ID:         collectionPrefix + collection.Key,
			Name:       collection.Name,
			TrackCount: collection.CloudcastCount,
			Section:    "Collections",
		})
	}
	lists = append(lists, publicLists...)
	return append(lists, styleLists...), nil
}

func (p *Provider) stylePlaylists() []playlist.PlaylistInfo {
	var lists []playlist.PlaylistInfo
	for _, style := range p.styleSnapshot() {
		label := styleLabel(style)
		lists = append(lists,
			playlist.PlaylistInfo{ID: stylePrefix + style + ":latest", Name: label + " — Latest", Section: "Music Styles"},
			playlist.PlaylistInfo{ID: stylePrefix + style + ":popular", Name: label + " — Popular", Section: "Music Styles"},
		)
	}
	return lists
}

// Tracks loads shows from one of the synthetic or account playlist views.
func (p *Provider) Tracks(playlistID string) ([]playlist.Track, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	var (
		shows []apiCloudcast
		err   error
	)
	switch playlistID {
	case recentID:
		shows, err = p.client.cloudcasts(ctx, "/discover/all/latest/", p.maxItems)
	case popularID:
		shows, err = p.client.cloudcasts(ctx, "/discover/all/popular/", p.maxItems)
	case streamID:
		shows, err = p.followingStream(ctx)
	case activityID:
		shows, err = p.profileActivity(ctx)
	case uploadsID:
		shows, err = p.accountCloudcasts(ctx, "cloudcasts")
	case favoritesID:
		shows, err = p.accountCloudcasts(ctx, "favorites")
	case listensID:
		shows, err = p.accountCloudcasts(ctx, "listens")
	case listenLaterID:
		if p.client.accessToken == "" {
			return nil, errors.New("mixcloud: Listen Later requires an access_token")
		}
		shows, err = p.client.cloudcasts(ctx, "/me/listen-later/", p.maxItems)
	default:
		switch {
		case strings.HasPrefix(playlistID, collectionPrefix):
			key := strings.TrimPrefix(playlistID, collectionPrefix)
			if !validAPIKey(key) || !strings.Contains(key, "/playlists/") {
				return nil, fmt.Errorf("mixcloud: invalid collection key %q", key)
			}
			shows, err = p.client.cloudcasts(ctx, ensureTrailingSlash(key)+"cloudcasts/", p.maxItems)
		case strings.HasPrefix(playlistID, stylePrefix):
			style, order, ok := parseStyleID(playlistID)
			if !ok {
				return nil, fmt.Errorf("mixcloud: invalid style playlist %q", playlistID)
			}
			shows, err = p.client.cloudcasts(ctx, discoverPath(style, order), p.maxItems)
		default:
			return nil, fmt.Errorf("mixcloud: unknown playlist %q", playlistID)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("mixcloud: load playlist %q: %w", playlistID, err)
	}
	return tracksFromCloudcasts(shows), nil
}

func (p *Provider) SearchTracks(ctx context.Context, query string, limit int) ([]playlist.Track, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("mixcloud: search shows: %w", err)
	}
	shows, err := p.client.searchCloudcasts(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: search shows: %w", err)
	}
	return tracksFromCloudcasts(shows), nil
}

// Artists lists the configured account followed creators. Without account
// configuration there is no meaningful finite creator list, so it is empty.
func (p *Provider) Artists() ([]provider.ArtistInfo, error) {
	if !p.hasAccount() {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	username, err := p.accountUsername(ctx)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: resolve account for creators: %w", err)
	}
	users, err := p.client.users(ctx, p.accountConnection(username, "following"), p.maxItems)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: list followed creators: %w", err)
	}
	artists := make([]provider.ArtistInfo, 0, len(users)+1)
	if username != "" {
		artists = append(artists, provider.ArtistInfo{ID: username, Name: username})
	}
	for _, user := range users {
		name := user.Name
		if name == "" {
			name = user.Username
		}
		artists = append(artists, provider.ArtistInfo{ID: user.Username, Name: name, AlbumCount: user.CloudcastCount})
	}
	return dedupeArtists(artists), nil
}

func (p *Provider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	username := strings.TrimSpace(artistID)
	if !validUsername(username) {
		return nil, fmt.Errorf("mixcloud: invalid creator username %q", artistID)
	}
	return []provider.AlbumInfo{
		{ID: creatorCollectionID(creatorUploadsID, username), Name: "Uploads", Artist: username, ArtistID: username},
		{ID: creatorCollectionID(creatorFavoritesID, username), Name: "Favorites", Artist: username, ArtistID: username},
	}, nil
}

// ArtistForTrack resolves a Mixcloud show back to its owning creator so the UI
// can jump directly from a selected show to that creator's Uploads/Favorites.
func (p *Provider) ArtistForTrack(track playlist.Track) (provider.ArtistInfo, bool) {
	username := strings.TrimSpace(track.Meta(provider.MetaMixcloudCreator))
	if !validUsername(username) {
		return provider.ArtistInfo{}, false
	}
	name := strings.TrimSpace(track.Artist)
	if name == "" {
		name = username
	}
	return provider.ArtistInfo{ID: username, Name: name}, true
}

func (p *Provider) AlbumList(sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	if size <= 0 {
		size = 50
	}
	apiPath, ok := p.albumSortPath(sortType)
	if !ok {
		return nil, fmt.Errorf("mixcloud: unknown show sort %q", sortType)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shows, _, err := p.client.cloudcastPage(ctx, apiPath, offset, size)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: list shows: %w", err)
	}
	return albumsFromCloudcasts(shows), nil
}

func (p *Provider) AlbumSortTypes() []provider.SortType {
	sorts := []provider.SortType{{ID: "latest", Label: "Recent Releases"}, {ID: "popular", Label: "Popular"}}
	for _, style := range p.styleSnapshot() {
		label := styleLabel(style)
		sorts = append(sorts,
			provider.SortType{ID: stylePrefix + style + ":latest", Label: label + " — Latest"},
			provider.SortType{ID: stylePrefix + style + ":popular", Label: label + " — Popular"},
		)
	}
	return sorts
}

func (p *Provider) DefaultAlbumSort() string { return "latest" }

// Genres returns Mixcloud's current public category catalogue, marking the
// entries pinned in config. Configured styles missing from the live catalogue
// are retained so a temporary API change never makes a favorite unreachable.
func (p *Provider) Genres() ([]provider.GenreInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	categories, err := p.client.categories(ctx)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: list genres: %w", err)
	}

	favorites := p.styleSnapshot()
	favoriteSet := make(map[string]bool, len(favorites))
	for _, style := range favorites {
		favoriteSet[style] = true
	}
	genres := make([]provider.GenreInfo, 0, len(categories)+len(favorites))
	seen := make(map[string]bool, len(categories)+len(favorites))
	for _, category := range categories {
		id := canonicalStyle(category.Slug)
		name := strings.TrimSpace(category.Name)
		if id == "" || name == "" || seen[id] {
			continue
		}
		seen[id] = true
		genres = append(genres, provider.GenreInfo{
			ID:       id,
			Name:     name,
			Group:    styleLabel(category.Format),
			Favorite: favoriteSet[id],
		})
	}
	for _, style := range favorites {
		if seen[style] {
			continue
		}
		genres = append(genres, provider.GenreInfo{ID: style, Name: styleLabel(style), Group: "Music", Favorite: true})
	}
	return genres, nil
}

func (p *Provider) GenreSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "latest", Label: "Latest"}, {ID: "popular", Label: "Popular"}}
}

func (p *Provider) SearchGenres(ctx context.Context, query string, limit int) ([]provider.GenreInfo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("mixcloud: search genres: %w", err)
	}
	tags, err := p.client.searchTags(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: search genres: %w", err)
	}
	favoriteSet := make(map[string]bool)
	for _, style := range p.styleSnapshot() {
		favoriteSet[style] = true
	}
	genres := make([]provider.GenreInfo, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		id := styleFromTagKey(tag.Key)
		if id == "" {
			id = canonicalStyle(tag.Name)
		}
		name := strings.TrimSpace(tag.Name)
		if id == "" || name == "" || seen[id] {
			continue
		}
		seen[id] = true
		genres = append(genres, provider.GenreInfo{ID: id, Name: name, Favorite: favoriteSet[id]})
	}
	return genres, nil
}

func (p *Provider) GenreTracks(genreID, sortType string) ([]playlist.Track, error) {
	genreID = canonicalStyle(genreID)
	if genreID == "" || (sortType != "latest" && sortType != "popular") {
		return nil, fmt.Errorf("mixcloud: invalid genre browse %q/%q", genreID, sortType)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	shows, err := p.client.cloudcasts(ctx, discoverPath(genreID, sortType), p.maxItems)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: browse genre %q: %w", genreID, err)
	}
	return tracksFromCloudcasts(shows), nil
}

// ToggleGenreFavorite updates both the live provider menu and the configured
// [mixcloud].styles list. Persistence happens before the in-memory change so a
// failed atomic write cannot make the UI disagree with disk.
func (p *Provider) ToggleGenreFavorite(genreID string) (bool, error) {
	genreID = canonicalStyle(genreID)
	if genreID == "" {
		return false, errors.New("mixcloud: invalid genre")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next := slices.Clone(p.styles)
	favorite := !slices.Contains(next, genreID)
	if favorite {
		next = append(next, genreID)
	} else {
		next = slices.DeleteFunc(next, func(style string) bool { return style == genreID })
	}
	if p.saveStyles != nil {
		if err := p.saveStyles(next); err != nil {
			return !favorite, fmt.Errorf("mixcloud: save genre favorites: %w", err)
		}
	}
	p.styles = next
	return favorite, nil
}

func (p *Provider) styleSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.styles)
}

func (p *Provider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	if username, connection, ok := parseCreatorCollectionID(albumID); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		shows, err := p.client.cloudcasts(ctx, userPath(username, connection), p.maxItems)
		if err != nil {
			return nil, fmt.Errorf("mixcloud: load creator %q %s: %w", username, connection, err)
		}
		tracks := tracksFromCloudcasts(shows)
		label := "Uploads"
		if connection == "favorites" {
			label = "Favorites"
		}
		for i := range tracks {
			tracks[i].Album = label
		}
		return tracks, nil
	}
	if !validAPIKey(albumID) {
		return nil, fmt.Errorf("mixcloud: invalid show key %q", albumID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	show, err := p.client.cloudcast(ctx, albumID)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: load show: %w", err)
	}
	track := trackFromCloudcast(show)
	if track.Path == "" {
		return nil, errors.New("mixcloud: show returned no playback URL or key")
	}
	return []playlist.Track{track}, nil
}

func (p *Provider) BrowseLabels() (artist, album string) { return "Creator", "Show" }

func (p *Provider) hasAccount() bool {
	return p.username != "" || p.client.accessToken != ""
}

func (p *Provider) accountUsername(ctx context.Context) (string, error) {
	// An access token makes /me the source of truth. This keeps account views
	// internally consistent when a configured public username is stale or
	// belongs to a different account than the token.
	if p.client.accessToken == "" && p.username != "" {
		return p.username, nil
	}
	p.mu.Lock()
	username := p.resolvedUsername
	p.mu.Unlock()
	if username != "" {
		return username, nil
	}
	if p.client.accessToken == "" {
		return "", errors.New("mixcloud: username or access_token is required for account views")
	}
	me, err := p.client.user(ctx, "/me/")
	if err != nil {
		return "", fmt.Errorf("mixcloud: resolve access-token account: %w", err)
	}
	if me.Username == "" {
		return "", errors.New("mixcloud: /me/ returned no username")
	}
	p.mu.Lock()
	p.resolvedUsername = me.Username
	p.mu.Unlock()
	return me.Username, nil
}

func (p *Provider) accountConnection(username, connection string) string {
	if p.client.accessToken != "" {
		return "/me/" + strings.Trim(connection, "/") + "/"
	}
	return userPath(username, connection)
}

func (p *Provider) accountCloudcasts(ctx context.Context, connection string) ([]apiCloudcast, error) {
	username, err := p.accountUsername(ctx)
	if err != nil {
		return nil, err
	}
	return p.client.cloudcasts(ctx, p.accountConnection(username, connection), p.maxItems)
}

func (p *Provider) profileActivity(ctx context.Context) ([]apiCloudcast, error) {
	username, err := p.accountUsername(ctx)
	if err != nil {
		return nil, err
	}
	activities, err := p.client.activities(ctx, p.accountConnection(username, "feed"), p.maxItems)
	if err != nil {
		return nil, err
	}
	var shows []apiCloudcast
	for _, activity := range activities {
		shows = append(shows, activity.Cloudcasts...)
	}
	return dedupeCloudcasts(shows, p.maxItems), nil
}

// followingStream approximates Mixcloud's website stream using the documented
// API: fetch followed creators, then merge each creator's newest uploads.
func (p *Provider) followingStream(ctx context.Context) ([]apiCloudcast, error) {
	username, err := p.accountUsername(ctx)
	if err != nil {
		return nil, err
	}
	users, err := p.client.users(ctx, p.accountConnection(username, "following"), p.streamCreators)
	if err != nil {
		return nil, fmt.Errorf("mixcloud: load followed creators for stream: %w", err)
	}
	if len(users) == 0 {
		return nil, nil
	}
	perCreator := max(2, (p.maxItems+len(users)-1)/len(users))

	type result struct {
		shows []apiCloudcast
		err   error
	}
	results := make(chan result, len(users))
	sem := make(chan struct{}, streamConcurrency)
	var wg sync.WaitGroup
	for _, user := range users {
		if user.Username == "" {
			continue
		}
		wg.Add(1)
		go func(username string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results <- result{err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			shows, err := p.client.cloudcasts(ctx, userPath(username, "cloudcasts"), perCreator)
			results <- result{shows: shows, err: err}
		}(user.Username)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var (
		shows    []apiCloudcast
		firstErr error
	)
	for result := range results {
		if result.err != nil {
			if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
				return nil, result.err
			}
			var apiErr *APIError
			if errors.As(result.err, &apiErr) && apiErr.StatusCode == 404 {
				// A followed creator may have disappeared between listing and
				// loading. Treat only that expected race as an empty source.
				continue
			}
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		shows = append(shows, result.shows...)
	}
	if firstErr != nil {
		return nil, fmt.Errorf("mixcloud: build following stream: %w", firstErr)
	}
	sort.SliceStable(shows, func(i, j int) bool { return shows[i].CreatedTime.After(shows[j].CreatedTime) })
	return dedupeCloudcasts(shows, p.maxItems), nil
}

func (p *Provider) albumSortPath(sortType string) (string, bool) {
	switch sortType {
	case "", "latest":
		return "/discover/all/latest/", true
	case "popular":
		return "/discover/all/popular/", true
	default:
		style, order, ok := parseStyleID(sortType)
		if !ok {
			return "", false
		}
		return discoverPath(style, order), true
	}
}

func tracksFromCloudcasts(shows []apiCloudcast) []playlist.Track {
	tracks := make([]playlist.Track, 0, len(shows))
	for _, show := range shows {
		track := trackFromCloudcast(show)
		if track.Path != "" {
			tracks = append(tracks, track)
		}
	}
	return tracks
}

func trackFromCloudcast(show apiCloudcast) playlist.Track {
	pageURL := mixcloudPageURL(show.URL)
	if pageURL == "" && validAPIKey(show.Key) {
		pageURL = "https://www.mixcloud.com" + ensureTrailingSlash(show.Key)
	}
	artist := show.User.Name
	if artist == "" {
		artist = show.User.Username
	}
	year := 0
	if !show.CreatedTime.IsZero() {
		year = show.CreatedTime.Year()
	}
	meta := map[string]string{provider.MetaMixcloudKey: show.Key}
	if validUsername(show.User.Username) {
		meta[provider.MetaMixcloudCreator] = show.User.Username
	}
	if show.IsExclusive {
		meta[provider.MetaMixcloudExclusive] = "true"
	}
	return playlist.Track{
		Path:         pageURL,
		Title:        show.Name,
		Artist:       artist,
		Genre:        tagNames(show.Tags),
		Year:         year,
		Stream:       true,
		DurationSecs: show.AudioLength,
		AlbumArtURL:  bestPicture(show.Pictures),
		ProviderMeta: meta,
	}
}

func mixcloudPageURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || (u.Hostname() != "mixcloud.com" && u.Hostname() != "www.mixcloud.com") {
		return ""
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func albumsFromCloudcasts(shows []apiCloudcast) []provider.AlbumInfo {
	albums := make([]provider.AlbumInfo, 0, len(shows))
	for _, show := range shows {
		if !validAPIKey(show.Key) {
			continue
		}
		artist := show.User.Name
		if artist == "" {
			artist = show.User.Username
		}
		year := 0
		if !show.CreatedTime.IsZero() {
			year = show.CreatedTime.Year()
		}
		albums = append(albums, provider.AlbumInfo{
			ID:         show.Key,
			Name:       show.Name,
			Artist:     artist,
			ArtistID:   show.User.Username,
			Year:       year,
			TrackCount: 1,
			Genre:      tagNames(show.Tags),
			Restricted: show.IsExclusive,
		})
	}
	return albums
}

func tagNames(tags []apiTag) string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		if name := strings.TrimSpace(tag.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func bestPicture(pictures apiPictures) string {
	if pictures.ExtraLarge != "" {
		return pictures.ExtraLarge
	}
	if pictures.Large != "" {
		return pictures.Large
	}
	return pictures.Medium
}

func userPath(username, connection string) string {
	return "/" + url.PathEscape(strings.TrimSpace(username)) + "/" + strings.Trim(connection, "/") + "/"
}

func validUsername(username string) bool {
	return username != "" && strings.TrimSpace(username) == username && !strings.ContainsAny(username, "/?#")
}

func creatorCollectionID(prefix, username string) string {
	return prefix + url.PathEscape(username)
}

func parseCreatorCollectionID(id string) (username, connection string, ok bool) {
	var encoded string
	switch {
	case strings.HasPrefix(id, creatorUploadsID):
		encoded = strings.TrimPrefix(id, creatorUploadsID)
		connection = "cloudcasts"
	case strings.HasPrefix(id, creatorFavoritesID):
		encoded = strings.TrimPrefix(id, creatorFavoritesID)
		connection = "favorites"
	default:
		return "", "", false
	}
	username, err := url.PathUnescape(encoded)
	if err != nil || !validUsername(username) {
		return "", "", false
	}
	return username, connection, true
}

func discoverPath(style, order string) string {
	return "/discover/" + url.PathEscape(style) + "/" + order + "/"
}

func parseStyleID(id string) (style, order string, ok bool) {
	if !strings.HasPrefix(id, stylePrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(id, stylePrefix)
	style, order, ok = strings.Cut(rest, ":")
	if !ok || normalizeStyle(style) != style || (order != "latest" && order != "popular") {
		return "", "", false
	}
	return style, order, true
}

func normalizeStyles(styles []string) []string {
	out := make([]string, 0, len(styles))
	seen := make(map[string]bool)
	for _, value := range styles {
		style := canonicalStyle(value)
		if style == "" || seen[style] {
			continue
		}
		seen[style] = true
		out = append(out, style)
	}
	return out
}

func canonicalStyle(value string) string {
	style := normalizeStyle(value)
	switch style {
	case "drum-and-bass":
		return "drum-bass"
	case "r-b", "r-and-b":
		return "rb"
	default:
		return style
	}
}

func styleFromTagKey(key string) string {
	const prefix = "/genres/"
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, "/") {
		return ""
	}
	style := strings.TrimSuffix(strings.TrimPrefix(key, prefix), "/")
	if strings.Contains(style, "/") || canonicalStyle(style) != style {
		return ""
	}
	return style
}

func normalizeStyle(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func styleLabel(style string) string {
	parts := strings.Split(style, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

func dedupeCloudcasts(shows []apiCloudcast, limit int) []apiCloudcast {
	out := make([]apiCloudcast, 0, min(len(shows), limit))
	seen := make(map[string]bool)
	for _, show := range shows {
		key := show.Key
		if key == "" {
			key = show.URL
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, show)
		if len(out) == limit {
			break
		}
	}
	return out
}

func dedupeArtists(artists []provider.ArtistInfo) []provider.ArtistInfo {
	out := make([]provider.ArtistInfo, 0, len(artists))
	seen := make(map[string]bool)
	for _, artist := range artists {
		key := strings.ToLower(artist.ID)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, artist)
	}
	return out
}
