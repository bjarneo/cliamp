package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/audiobookshelf"
	"github.com/bjarneo/cliamp/external/emby"
	"github.com/bjarneo/cliamp/external/jellyfin"
	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/external/lyrion"
	"github.com/bjarneo/cliamp/external/mixcloud"
	"github.com/bjarneo/cliamp/external/navidrome"
	"github.com/bjarneo/cliamp/external/netease"
	"github.com/bjarneo/cliamp/external/plex"
	"github.com/bjarneo/cliamp/external/podcast"
	"github.com/bjarneo/cliamp/external/qobuz"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/external/radiometa"
	"github.com/bjarneo/cliamp/external/soundcloud"
	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/external/tidal"
	"github.com/bjarneo/cliamp/external/yandex"
	"github.com/bjarneo/cliamp/external/ytmusic"
	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/embyapi"
	"github.com/bjarneo/cliamp/internal/resume"
	"github.com/bjarneo/cliamp/internal/ytdlp"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/resolve"
	"github.com/bjarneo/cliamp/ui/model"
)

// providerSet holds the providers of one run, in the order of the provider
// list. The player hooks, the sign-in observers and the shutdown find the
// providers in entries. A provider.CustomStreamer or a provider.Closer needs
// no code here.
type providerSet struct {
	entries []provider.Entry
	// local, favorites and history are nil when the config directory is
	// unavailable. The local provider and the Model share the two stores.
	local          *local.Provider
	favorites      *favorites.Store
	history        *history.Store
	radioFavorites *radio.Favorites
}

// providerKey is one provider that --provider and the provider config key
// accept.
type providerKey struct {
	key   string // the value of --provider and of the provider config key
	alias string // a short form of key that --provider also accepts
	name  string // the display name in the provider list
	// optional marks a provider that registers only when it is configured.
	// The log names each optional provider that is not configured.
	optional bool
}

// providerKeys lists the providers that --provider accepts, in help order.
// The flag help, the flag check, the provider names and the log of skipped
// providers all come from this table. The local provider is not a
// --provider value.
var providerKeys = []providerKey{
	{key: "cliamp", name: "cliamp radio"},
	{key: "radio", name: "Radio"},
	{key: "podcast", name: "Podcasts"},
	{key: "navidrome", name: "Navidrome", optional: true},
	{key: "lyrion", name: "Lyrion", optional: true},
	{key: "plex", name: "Plex", optional: true},
	{key: "jellyfin", name: "Jellyfin", optional: true},
	{key: "emby", name: "Emby", optional: true},
	{key: "spotify", name: "Spotify", optional: true},
	{key: "qobuz", name: "Qobuz", optional: true},
	{key: "tidal", name: "Tidal", optional: true},
	{key: "soundcloud", name: "SoundCloud", optional: true},
	{key: "mixcloud", name: "Mixcloud", optional: true},
	{key: "netease", name: "NetEase", optional: true},
	{key: "yandex", name: "Yandex Music", optional: true},
	{key: "audiobookshelf", alias: "abs", name: "Audiobookshelf", optional: true},
	{key: "yt", name: "YouTube (All)"},
	{key: "youtube", name: "YouTube"},
	{key: "ytmusic", name: "YouTube Music"},
}

// providerName returns the display name of the provider key.
func providerName(key string) string {
	for _, pk := range providerKeys {
		if pk.key == key {
			return pk.name
		}
	}
	return key
}

// providerFlagUsage returns the help text of --provider.
func providerFlagUsage() string {
	var values []string
	for _, pk := range providerKeys {
		values = append(values, pk.key)
		if pk.alias != "" {
			values = append(values, pk.alias)
		}
	}
	return "default provider: " + strings.Join(values, ", ")
}

// parseProviderKey returns the provider key that the --provider value v
// names. It accepts a key or an alias in any case.
func parseProviderKey(v string) (string, error) {
	v = strings.ToLower(v)
	keys := make([]string, len(providerKeys))
	for i, pk := range providerKeys {
		if v == pk.key || (pk.alias != "" && v == pk.alias) {
			return pk.key, nil
		}
		keys[i] = pk.key
	}
	last := len(keys) - 1
	return "", fmt.Errorf("--provider must be %s, or %s (got %q)", strings.Join(keys[:last], ", "), keys[last], v)
}

// buildProviders creates the providers that cfg enables. The public
// providers are always available. The account providers register when they
// are configured. interactive allows the yt-dlp install prompt.
func buildProviders(cfg config.Config, interactive bool) *providerSet {
	s := &providerSet{radioFavorites: radio.LoadFavorites()}
	radioProv := radio.New(radio.Options{
		Favorites:   s.radioFavorites,
		Country:     cfg.Radio.Country,
		SaveCountry: config.SaveRadioCountry,
	})
	s.favorites, s.history = favorites.New(), history.New()
	s.local = local.New(s.favorites, s.history)
	// The virtual Favorites playlist reserves the name of a Favorites.toml
	// playlist. Bookmarks became favorites. Copy the old bookmarks one time.
	if err := s.local.MigrateFavoritesFile(); err != nil {
		applog.Warn("favorites playlist migration: %v", err)
	}
	if added, err := s.local.MigrateBookmarks(); err != nil {
		applog.Warn("bookmark migration: %v", err)
	} else if added > 0 {
		applog.Info("copied %d bookmarks into favorites", added)
	}

	add := func(key string, p playlist.Provider) {
		s.entries = append(s.entries, provider.Entry{Key: key, Name: providerName(key), Provider: p})
	}
	// The cliamp radio channels come first: they are the view cliamp opens on.
	add("cliamp", radio.NewChannels())
	add("radio", radioProv)
	if s.local != nil {
		s.entries = append(s.entries, provider.Entry{Key: "local", Name: "Local", Provider: s.local})
	} else {
		logProviderSkipped("Local", "local", "config directory unavailable")
	}
	add("podcast", podcast.New(cfg.Podcast.Country))

	// A configured server takes precedence over the environment variables.
	nav := navidrome.NewFromConfig(cfg.Navidrome)
	if nav == nil {
		nav = navidrome.NewFromEnv(cfg.Navidrome)
	}
	if nav != nil {
		nav.SaveSort = config.SaveNavidromeSort
		add("navidrome", nav)
	}
	lyr := lyrion.NewFromConfig(cfg.Lyrion)
	if lyr == nil {
		lyr = lyrion.NewFromEnv(cfg.Lyrion)
	}
	if lyr != nil {
		add("lyrion", lyr)
	}
	if p := plex.NewFromConfig(cfg.Plex); p != nil {
		add("plex", p)
	}
	if p := jellyfin.NewFromConfig(cfg.Jellyfin); p != nil {
		add("jellyfin", p)
	}
	if p := emby.NewFromConfig(cfg.Emby); p != nil {
		add("emby", p)
	}
	if p := audiobookshelf.NewFromConfig(cfg.Audiobookshelf); p != nil {
		add("audiobookshelf", p)
	}
	if cfg.Spotify.IsSet() {
		clientID := cfg.Spotify.ResolveClientID(spotify.DefaultClientID)
		add("spotify", spotify.New(nil, clientID, cfg.Spotify.Bitrate))
	}
	if cfg.Qobuz.IsSet() {
		add("qobuz", qobuz.New(cfg.Qobuz.Quality))
	}
	if cfg.Tidal.IsSet() {
		add("tidal", tidal.New(cfg.Tidal.Quality, cfg.Tidal.ClientID, cfg.Tidal.ClientSecret))
	}
	if p := soundcloud.NewFromConfig(soundcloud.Config{
		Enabled:     cfg.SoundCloud.Enabled,
		User:        cfg.SoundCloud.User,
		CookiesFrom: cfg.SoundCloud.CookiesFrom,
	}); p != nil {
		add("soundcloud", p)
	}
	if p := mixcloud.NewFromConfig(mixcloud.Config{
		Enabled:     cfg.Mixcloud.Enabled,
		Username:    cfg.Mixcloud.Username,
		AccessToken: cfg.Mixcloud.AccessToken,
		CookiesFrom: cfg.Mixcloud.CookiesFrom,
		Styles:      cfg.Mixcloud.Styles,
		StylesSet:   cfg.Mixcloud.StylesSet,
		MaxItems:    cfg.Mixcloud.MaxItems,
		SaveStyles:  config.SaveMixcloudStyles,
	}); p != nil {
		add("mixcloud", p)
	}
	if p := netease.NewFromConfig(netease.Config{
		Enabled:     cfg.NetEase.Enabled,
		CookiesFrom: cfg.NetEase.CookiesFrom,
		UserID:      cfg.NetEase.UserID,
	}); p != nil {
		add("netease", p)
	}
	if p := yandex.NewFromConfig(yandex.Config{
		Enabled: cfg.Yandex.Enabled,
		Token:   cfg.Yandex.Token,
	}); p != nil {
		add("yandex", p)
	}
	s.entries = append(s.entries, youTubeEntries(cfg, interactive)...)

	logProviderWiring(s.entries)
	return s
}

// youTubeEntries returns the YouTube (All), YouTube and YouTube Music
// entries, which share one sign-in. It returns none when YouTube is not
// wanted, has no credentials or has no yt-dlp.
func youTubeEntries(cfg config.Config, interactive bool) []provider.Entry {
	wanted := cfg.YouTubeMusic.IsSet()
	if !wanted {
		switch cfg.Provider {
		case "yt", "youtube", "ytmusic":
			wanted = true
		}
	}
	if !wanted {
		logYouTubeSkipped("not configured")
		return nil
	}
	clientID := strings.TrimSpace(cfg.YouTubeMusic.ClientID)
	clientSecret := strings.TrimSpace(cfg.YouTubeMusic.ClientSecret)
	hasOAuth := clientID != "" && clientSecret != ""
	hasCookies := strings.TrimSpace(cfg.YouTubeMusic.CookiesFrom) != ""
	if hasCookies {
		for _, host := range []string{"youtube.com", "youtu.be", "music.youtube.com"} {
			resolve.SetYTDLCookiesForHost(host, cfg.YouTubeMusic.CookiesFrom)
		}
	}
	if !hasOAuth && !hasCookies {
		fmt.Fprintf(os.Stderr, "YouTube: no credentials available (configure client_id/client_secret or cookies_from in config.toml)\n")
		logYouTubeSkipped("no credentials available")
		return nil
	}

	if !player.YTDLPAvailable() {
		fmt.Fprintf(os.Stderr, "\nYouTube requires yt-dlp for audio playback.\n")
		fmt.Fprintf(os.Stderr, "Install command: %s\n\n", ytdlp.InstallHint())
		if offerYTDLPInstall(interactive, os.Stdin, os.Stderr) {
			fmt.Fprintf(os.Stderr, "Installing yt-dlp...\n")
			if err := installYTDLP(); err != nil {
				fmt.Fprintf(os.Stderr, "Installation failed: %v\n", err)
				fmt.Fprintf(os.Stderr, "YouTube providers disabled. Install manually and restart.\n\n")
			} else {
				fmt.Fprintf(os.Stderr, "yt-dlp installed successfully!\n\n")
			}
		}
	}
	if !player.YTDLPAvailable() {
		logYouTubeSkipped("yt-dlp not available")
		return nil
	}

	var all, video, music playlist.Provider
	if hasOAuth {
		p := ytmusic.New(nil, clientID, clientSecret, hasCookies)
		all, video, music = p.All, p.Video, p.Music
	} else {
		p := ytmusic.NewCookieProviders(cfg.YouTubeMusic.CookiesFrom)
		all, video, music = p.All, p.Video, p.Music
	}
	return []provider.Entry{
		{Key: "yt", Name: providerName("yt"), Provider: all},
		{Key: "youtube", Name: providerName("youtube"), Provider: video},
		{Key: "ytmusic", Name: providerName("ytmusic"), Provider: music},
	}
}

// Close releases the providers that implement provider.Closer. The three
// YouTube providers share one base, and its close is safe to repeat.
func (s *providerSet) Close() {
	for _, e := range s.entries {
		if c, ok := e.Provider.(provider.Closer); ok {
			c.Close()
		}
	}
}

// localPlaylists returns the local provider for model.New. With no config
// directory it returns a nil interface. A nil *local.Provider in the
// interface would look set to the Model, which then calls it and panics.
func (s *providerSet) localPlaylists() playlist.Provider {
	if s.local == nil {
		return nil
	}
	return s.local
}

// resumeServer returns the Jellyfin or Emby provider when key names one that
// is configured. When such a server is the default provider, cliamp saves
// the list that a track was played from and restores it at the next start.
// It returns nil for any other key.
func (s *providerSet) resumeServer(key string) *embyapi.Provider {
	for _, e := range s.entries {
		if e.Key != key {
			continue
		}
		switch p := e.Provider.(type) {
		case *jellyfin.Provider:
			return p.Provider
		case *emby.Provider:
			return p.Provider
		}
	}
	return nil
}

// registerPlayerHooks registers with p the stream factories and source
// resolvers of the providers, and the rules that pick the pipeline of a URL.
func (s *providerSet) registerPlayerHooks(p *player.Player) {
	var servers []*embyapi.Provider
	for _, e := range s.entries {
		if cs, ok := e.Provider.(provider.CustomStreamer); ok {
			for _, scheme := range cs.URISchemes() {
				p.RegisterStreamerFactory(scheme, cs.NewStreamer)
			}
		}
		switch prov := e.Provider.(type) {
		case *yandex.Provider:
			// Yandex tracks carry yandex:track: URIs; the provider resolves them
			// to a fresh signed stream URL when playback starts.
			p.RegisterSourceResolver(yandex.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
				u, err := prov.ResolveSource(uri)
				if err != nil {
					return player.ResolvedSource{}, fmt.Errorf("resolve Yandex source: %w", err)
				}
				return player.ResolvedSource{URL: u}, nil
			})
		case *qobuz.QobuzProvider:
			// Qobuz tracks carry qobuz:// URIs. The provider resolves them to a
			// fresh signed URL when playback starts. The URL is a finite file
			// that buffers for seeking.
			p.RegisterSourceResolver(qobuz.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
				u, err := prov.ResolveSource(uri)
				return player.ResolvedSource{URL: u, Buffered: true}, err
			})
		case *tidal.TidalProvider:
			// Tidal tracks carry tidal:// URIs; the provider resolves them to a
			// fresh signed URL or DASH segment list when playback starts. The
			// URL is a finite file that buffers for seeking.
			p.RegisterSourceResolver(tidal.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
				u, segments, err := prov.ResolveSource(uri)
				return player.ResolvedSource{URL: u, Segments: segments, Buffered: true}, err
			})
		case *lyrion.Client:
			p.RegisterSourceResolver(lyrion.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
				u, segments, err := prov.ResolveSource(uri)
				return player.ResolvedSource{URL: u, Segments: segments}, err
			})
		case *jellyfin.Provider:
			servers = append(servers, prov.Provider)
		case *emby.Provider:
			servers = append(servers, prov.Provider)
		}
	}
	if len(servers) > 0 {
		// Refresh saved Jellyfin and Emby URLs without changing logical
		// playlist paths. A server returns the URL of another server as is.
		refresh := func(rawURL string) (player.ResolvedSource, error) {
			for _, server := range servers {
				u, err := server.ResolveSource(rawURL)
				if err != nil || u != rawURL {
					return player.ResolvedSource{URL: u}, err
				}
			}
			return player.ResolvedSource{URL: rawURL}, nil
		}
		for _, scheme := range []string{"http://", "https://"} {
			p.RegisterSourceResolver(scheme, refresh)
		}
	}

	p.RegisterBufferedURLMatcher(isBufferedProviderURL)
	p.RegisterYTDLMatcher(playlist.IsYTDL)

	// Pull now-playing for stations that carry no inline ICY metadata (NTS, FIP).
	p.RegisterStreamMetadataResolver(radiometa.Resolver)
}

// isBufferedProviderURL reports whether u is a provider stream endpoint that
// needs the buffered download pipeline rather than the live-stream one. These
// are finite files with a known length, so buffering gives seeking and gapless
// playback. It matches every provider, configured or not, because history,
// favorites and saved playlists keep these URLs.
func isBufferedProviderURL(u string) bool {
	return navidrome.IsSubsonicStreamURL(u) ||
		jellyfin.IsStreamURL(u) ||
		emby.IsStreamURL(u) ||
		plex.IsStreamURL(u) ||
		audiobookshelf.IsStreamURL(u) ||
		lyrion.IsStreamURL(u) ||
		yandex.IsStreamURL(u)
}

// observeAuthURLs sends the sign-in URL of a provider to the Model, so the
// provider pane can show it when no browser opens. restore removes the
// observers.
func (s *providerSet) observeAuthURLs(send func(tea.Msg)) (restore func()) {
	var restores []func()
	observe := func(set func(func(string)), names ...string) {
		set(func(u string) {
			for _, name := range names {
				send(model.ProvAuthURLMsg{ProviderName: name, URL: u})
			}
		})
		restores = append(restores, func() { set(nil) })
	}
	// The three YouTube providers share one sign-in. The model shows the URL
	// only for the provider that is active.
	var youTube []string
	for _, e := range s.entries {
		switch p := e.Provider.(type) {
		case *spotify.SpotifyProvider:
			observe(spotify.SetAuthURLObserver, p.Name())
		case *qobuz.QobuzProvider:
			observe(qobuz.SetAuthURLObserver, p.Name())
		case *tidal.TidalProvider:
			observe(tidal.SetAuthURLObserver, p.Name())
		case *ytmusic.YouTubeAllProvider, *ytmusic.YouTubeProvider, *ytmusic.YouTubeMusicProvider:
			youTube = append(youTube, p.Name())
		}
	}
	if len(youTube) > 0 {
		observe(ytmusic.SetAuthURLObserver, youTube...)
	}
	return func() {
		for _, restore := range restores {
			restore()
		}
	}
}

// serverResumeSaver saves the play context of each track that server owns,
// so the next start can restore it.
func serverResumeSaver(server *embyapi.Provider) model.ResumeSaver {
	return func(track playlist.Track, positionSec int, context []playlist.Track, contextIndex int) {
		if _, ok := server.RestoreTrack(track); !ok {
			return
		}
		resume.SaveState(resume.State{
			Path: track.Path, PositionSec: positionSec,
			Context: context, ContextIndex: contextIndex,
		})
	}
}

// restoreServerContext rebuilds the saved play context of a Jellyfin or
// Emby server. It returns the tracks, the index and the path of the track
// that played. It fails when that track is not from this server.
func restoreServerContext(state resume.State, prov *embyapi.Provider) ([]playlist.Track, int, string, bool) {
	if prov == nil || len(state.Context) == 0 {
		return nil, 0, "", false
	}
	index := state.ContextIndex
	if index < 0 || index >= len(state.Context) || state.Context[index].Path != state.Path {
		index = -1
		for i, track := range state.Context {
			if track.Path == state.Path {
				index = i
				break
			}
		}
	}
	if index < 0 {
		return nil, 0, "", false
	}
	if _, ok := prov.RestoreTrack(state.Context[index]); !ok {
		return nil, 0, "", false
	}

	tracks := append([]playlist.Track(nil), state.Context...)
	for i, track := range tracks {
		if restored, ok := prov.RestoreTrack(track); ok {
			tracks[i] = restored
		}
	}
	return tracks, index, tracks[index].Path, true
}

// logProviderRegistered records that a provider joined the active set. It
// writes to the log file only, so it never disturbs the TUI. See issue #406.
func logProviderRegistered(name, key string) {
	applog.Info("provider registered: name=%s key=%s", name, key)
}

// logProviderSkipped records why a provider did not register. It writes to
// the log file only, so it never disturbs the TUI. See issue #406.
func logProviderSkipped(name, key, reason string) {
	applog.Info("provider skipped: name=%s key=%s reason=%s", name, key, reason)
}

// logYouTubeSkipped records the skip reason for all three YouTube providers
// (All, video, music), since they register or skip as one group.
func logYouTubeSkipped(reason string) {
	for _, key := range []string{"yt", "youtube", "ytmusic"} {
		logProviderSkipped(providerName(key), key, reason)
	}
}

// offerYTDLPInstall asks on in whether to install yt-dlp now. It asks only
// when interactive is true. A bare Enter, y or yes in any case confirms. Any
// other answer, EOF or a read error skips the install. So a start with stdin
// at /dev/null, as under systemd, never installs a package.
func offerYTDLPInstall(interactive bool, in io.Reader, out io.Writer) bool {
	if !interactive {
		return false
	}
	fmt.Fprint(out, "Press Enter to install it now, or type n and press Enter to skip... ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err == nil {
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			return true
		}
	} else {
		// EOF leaves the cursor on the prompt line.
		fmt.Fprintln(out)
	}
	fmt.Fprint(out, "Skipped. YouTube providers are disabled.\n\n")
	return false
}

// installYTDLP attempts to install yt-dlp using the system package manager.
// Returns nil on success. The caller should re-check player.YTDLPAvailable()
// after.
func installYTDLP() error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			cmd := exec.Command("brew", "install", "yt-dlp")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
		// Fall through to pip
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			cmd := exec.Command("sudo", "apt-get", "install", "-y", "yt-dlp")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			cmd := exec.Command("sudo", "pacman", "-S", "--noconfirm", "yt-dlp")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			return cmd.Run()
		}
	}
	// Fallback: pip/pipx
	if path, err := exec.LookPath("pipx"); err == nil {
		cmd := exec.Command(path, "install", "yt-dlp")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if path, err := exec.LookPath("pip3"); err == nil {
		cmd := exec.Command(path, "install", "yt-dlp")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return fmt.Errorf("no supported package manager found — install manually: https://github.com/yt-dlp/yt-dlp#installation")
}

// isCharDevice reports whether f is a character device, such as a terminal.
// A pipe or a regular file is not. The null device is also a character
// device, so a start with stdin at /dev/null shows the install prompt. The
// EOF that follows skips the install.
func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// logProviderWiring logs the final provider registry: one line per
// registered provider, plus a skip line for each optional provider absent
// from it. YouTube and Local are not optional: they log their own specific
// skip reasons. See issue #406.
func logProviderWiring(providers []provider.Entry) {
	registered := make(map[string]bool, len(providers))
	for _, p := range providers {
		logProviderRegistered(p.Name, p.Key)
		registered[p.Key] = true
	}
	for _, pk := range providerKeys {
		if pk.optional && !registered[pk.key] {
			logProviderSkipped(pk.name, pk.key, "not configured")
		}
	}
}
