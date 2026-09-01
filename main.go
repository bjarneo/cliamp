package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	"github.com/bjarneo/cliamp/external/qobuz"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/external/radiometa"
	"github.com/bjarneo/cliamp/external/soundcloud"
	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/external/tidal"
	"github.com/bjarneo/cliamp/external/ytmusic"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/internal/resume"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/mediactl"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
	"github.com/bjarneo/cliamp/ui/model"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version string

const (
	defaultUIFPS  = 20
	lowPowerUIFPS = 5
)

// isBufferedProviderURL reports whether u is a provider stream endpoint that
// needs the buffered download pipeline rather than the live-stream one. These
// are finite files with a known length, so buffering gives seeking and gapless
// playback.
func isBufferedProviderURL(u string) bool {
	return navidrome.IsSubsonicStreamURL(u) ||
		jellyfin.IsStreamURL(u) ||
		emby.IsStreamURL(u) ||
		plex.IsStreamURL(u) ||
		qobuz.IsStreamURL(u) ||
		tidal.IsStreamURL(u) ||
		audiobookshelf.IsStreamURL(u) ||
		lyrion.IsStreamURL(u)
}

func run(overrides config.Overrides, positional []string, daemon, visualizer60FPS bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	overrides.Apply(&cfg)

	closeLog, appliedLevel, logErr := initLogging(cfg.LogLevel)
	defer closeLog()
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "logging: %v (continuing without file log)\n", logErr)
		applog.Status("logging: %v", logErr)
	} else {
		applog.Info("cliamp starting (version=%s level=%s)", appmeta.Version(), appliedLevel)
	}

	// Build provider list: Radio is always available, Navidrome and Spotify if configured.
	radioProv := radio.New(radio.Options{
		Country:     cfg.Radio.Country,
		SaveCountry: config.SaveRadioCountry,
	})
	localProv := local.New()

	var providers []model.ProviderEntry
	providers = append(providers, model.ProviderEntry{Key: "radio", Name: "Radio", Provider: radioProv})
	if localProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "local", Name: "Local", Provider: localProv})
	}

	var navClient *navidrome.NavidromeClient
	if c := navidrome.NewFromConfig(cfg.Navidrome); c != nil {
		navClient = c
	} else if c := navidrome.NewFromEnv(); c != nil {
		navClient = c
	}
	if navClient != nil {
		providers = append(providers, model.ProviderEntry{Key: "navidrome", Name: "Navidrome", Provider: navClient})
	}

	var lyrionClient *lyrion.Client
	if c := lyrion.NewFromConfig(cfg.Lyrion); c != nil {
		lyrionClient = c
	} else if c := lyrion.NewFromEnv(); c != nil {
		lyrionClient = c
	}
	if lyrionClient != nil {
		providers = append(providers, model.ProviderEntry{Key: "lyrion", Name: "Lyrion", Provider: lyrionClient})
	}

	if plexProv := plex.NewFromConfig(cfg.Plex); plexProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "plex", Name: "Plex", Provider: plexProv})
	}

	if jellyProv := jellyfin.NewFromConfig(cfg.Jellyfin); jellyProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "jellyfin", Name: "Jellyfin", Provider: jellyProv})
	}

	if embyProv := emby.NewFromConfig(cfg.Emby); embyProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "emby", Name: "Emby", Provider: embyProv})
	}

	if absProv := audiobookshelf.NewFromConfig(cfg.Audiobookshelf); absProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "audiobookshelf", Name: "Audiobookshelf", Provider: absProv})
	}

	var spotifyProv *spotify.SpotifyProvider
	if cfg.Spotify.IsSet() {
		clientID := cfg.Spotify.ResolveClientID(spotify.DefaultClientID)
		spotifyProv = spotify.New(nil, clientID, cfg.Spotify.Bitrate)
		providers = append(providers, model.ProviderEntry{Key: "spotify", Name: "Spotify", Provider: spotifyProv})
	}

	var qobuzProv *qobuz.QobuzProvider
	if cfg.Qobuz.IsSet() {
		qobuzProv = qobuz.New(cfg.Qobuz.Quality)
		providers = append(providers, model.ProviderEntry{Key: "qobuz", Name: "Qobuz", Provider: qobuzProv})
	}

	var tidalProv *tidal.TidalProvider
	if cfg.Tidal.IsSet() {
		tidalProv = tidal.New(cfg.Tidal.Quality, cfg.Tidal.ClientID, cfg.Tidal.ClientSecret)
		providers = append(providers, model.ProviderEntry{Key: "tidal", Name: "Tidal", Provider: tidalProv})
	}

	if scProv := soundcloud.NewFromConfig(soundcloud.Config{
		Enabled:     cfg.SoundCloud.Enabled,
		User:        cfg.SoundCloud.User,
		CookiesFrom: cfg.SoundCloud.CookiesFrom,
	}); scProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "soundcloud", Name: "SoundCloud", Provider: scProv})
	}

	if mcProv := mixcloud.NewFromConfig(mixcloud.Config{
		Enabled:        cfg.Mixcloud.Enabled,
		Username:       cfg.Mixcloud.Username,
		AccessToken:    cfg.Mixcloud.AccessToken,
		CookiesFrom:    cfg.Mixcloud.CookiesFrom,
		Styles:         cfg.Mixcloud.Styles,
		StylesSet:      cfg.Mixcloud.StylesSet,
		MaxItems:       cfg.Mixcloud.MaxItems,
		StreamCreators: cfg.Mixcloud.StreamCreators,
		SaveStyles:     config.SaveMixcloudStyles,
	}); mcProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "mixcloud", Name: "Mixcloud", Provider: mcProv})
	}

	if neProv := netease.NewFromConfig(netease.Config{
		Enabled:     cfg.NetEase.Enabled,
		CookiesFrom: cfg.NetEase.CookiesFrom,
		UserID:      cfg.NetEase.UserID,
	}); neProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "netease", Name: "NetEase", Provider: neProv})
	}

	var closeYouTube func()
	ytWanted := cfg.YouTubeMusic.IsSetOrFallback(ytmusic.FallbackCredentials)
	if !ytWanted {
		switch cfg.Provider {
		case "yt", "youtube", "ytmusic":
			ytWanted = true
		}
	}
	if ytWanted {
		explicitOAuth := strings.TrimSpace(cfg.YouTubeMusic.ClientID) != "" && strings.TrimSpace(cfg.YouTubeMusic.ClientSecret) != ""
		hasCookies := strings.TrimSpace(cfg.YouTubeMusic.CookiesFrom) != ""
		if hasCookies {
			for _, host := range []string{"youtube.com", "youtu.be", "music.youtube.com"} {
				resolve.SetYTDLCookiesForHost(host, cfg.YouTubeMusic.CookiesFrom)
			}
		}

		ytClientID, ytClientSecret := cfg.YouTubeMusic.ResolveCredentials(ytmusic.FallbackCredentials)
		hasFallbackOAuth := !explicitOAuth && ytClientID != "" && ytClientSecret != ""

		if !explicitOAuth && !hasCookies && !hasFallbackOAuth {
			fmt.Fprintf(os.Stderr, "YouTube: no credentials available (configure client_id/client_secret or cookies_from in config.toml)\n")
		} else {
			if !player.YTDLPAvailable() {
				fmt.Fprintf(os.Stderr, "\nYouTube requires yt-dlp for audio playback.\n")
				fmt.Fprintf(os.Stderr, "Install command: %s\n\n", player.YtdlpInstallHint())
				fmt.Fprintf(os.Stderr, "Press Enter to install automatically, or Ctrl+C to skip... ")
				fmt.Scanln()
				fmt.Fprintf(os.Stderr, "Installing yt-dlp...\n")
				if err := player.InstallYTDLP(); err != nil {
					fmt.Fprintf(os.Stderr, "Installation failed: %v\n", err)
					fmt.Fprintf(os.Stderr, "YouTube providers disabled. Install manually and restart.\n\n")
				} else {
					fmt.Fprintf(os.Stderr, "yt-dlp installed successfully!\n\n")
				}
			}
			if player.YTDLPAvailable() {
				var all, video, music playlist.Provider
				if explicitOAuth {
					oauthProviders := ytmusic.New(nil, ytClientID, ytClientSecret, hasCookies)
					all, video, music = oauthProviders.All, oauthProviders.Video, oauthProviders.Music
					closeYouTube = oauthProviders.Music.Close
				} else if hasCookies {
					cookieProviders := ytmusic.NewCookieProviders(cfg.YouTubeMusic.CookiesFrom)
					all, video, music = cookieProviders.All, cookieProviders.Video, cookieProviders.Music
					closeYouTube = cookieProviders.Music.Close
				} else if hasFallbackOAuth {
					oauthProviders := ytmusic.New(nil, ytClientID, ytClientSecret, false)
					all, video, music = oauthProviders.All, oauthProviders.Video, oauthProviders.Music
					closeYouTube = oauthProviders.Music.Close
				}
				if all != nil {
					providers = append(providers,
						model.ProviderEntry{Key: "yt", Name: "YouTube (All)", Provider: all},
						model.ProviderEntry{Key: "youtube", Name: "YouTube", Provider: video},
						model.ProviderEntry{Key: "ytmusic", Name: "YouTube Music", Provider: music},
					)
				}
			}
		}
	}

	if spotifyProv != nil {
		defer spotifyProv.Close()
	}
	if qobuzProv != nil {
		defer qobuzProv.Close()
	}
	if tidalProv != nil {
		defer tidalProv.Close()
	}
	if closeYouTube != nil {
		defer closeYouTube()
	}

	if len(positional) > 0 && (positional[0] == "search" || positional[0] == "search-sc") {
		if len(positional) == 1 {
			return fmt.Errorf("search requires a query string (e.g. cliamp search \"never gonna give you up\")")
		}
		prefix := "ytsearch1:"
		if positional[0] == "search-sc" {
			prefix = "scsearch1:"
		}
		query := strings.Join(positional[1:], " ")
		positional = []string{prefix + query}
	}

	if cfg.YouTubeMusic.ExpandPlaylist != nil {
		resolve.ExpandYTPlaylist = *cfg.YouTubeMusic.ExpandPlaylist
	}

	resolved, err := resolve.Args(positional)
	if err != nil {
		return err
	}

	defaultProvider := cfg.Provider
	if defaultProvider == "" {
		defaultProvider = "radio"
	}
	defaultRadio := len(positional) == 0 && defaultProvider == "radio"

	pl := playlist.New()
	if cfg.Playlist != "" && localProv != nil {
		tracks, err := localProv.Tracks(cfg.Playlist)
		if err != nil {
			return fmt.Errorf("playlist %q: %w", cfg.Playlist, err)
		}
		pl.Add(tracks...)
	} else if defaultRadio {
		pl.Add(
			playlist.Track{Path: "http://radio.cliamp.stream/lofi/stream", Title: "Lofi Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/synthwave/stream", Title: "Synthwave Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/edm/stream", Title: "EDM Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/omarchy/stream", Title: "Omarchy Radio", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/ncs/stream", Title: "NCS Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/ncs-house/stream", Title: "NCS House Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/ncs-dubstep/stream", Title: "NCS Dubstep Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/ncs-dnb/stream", Title: "NCS Drum & Bass Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/ncs-trap/stream", Title: "NCS Trap Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/ncs-phonk/stream", Title: "NCS Phonk Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/ncs-pop/stream", Title: "NCS Pop Stream", Stream: true, Realtime: true},
			playlist.Track{Path: "http://radio.cliamp.stream/ncs-chill/stream", Title: "NCS Chill Stream", Stream: true, Realtime: true},
		)
	}
	pl.Add(resolved.Tracks...)

	// Daemon mode has no UI loop to drain pending URLs (feeds, M3U, yt-dlp),
	// so resolve them synchronously here. The TUI path does this in the
	// background via m.SetPendingURLs.
	if daemon && len(resolved.Pending) > 0 {
		fmt.Fprintf(os.Stderr, "cliamp: resolving %d remote URL(s)...\n", len(resolved.Pending))
		remote, err := resolve.Remote(resolved.Pending)
		if err != nil {
			return fmt.Errorf("resolve remote: %w", err)
		}
		pl.Add(remote...)
	}

	if cfg.AudioDevice != "" {
		cleanup := player.PrepareAudioDevice(cfg.AudioDevice)
		defer cleanup()
	}

	sampleRate := cfg.SampleRate
	if sampleRate == 0 {
		if detected := player.DeviceSampleRate(); detected > 0 {
			sampleRate = detected
		} else {
			sampleRate = 44100
		}
	}

	p, err := player.New(player.Quality{
		SampleRate:      sampleRate,
		BufferMs:        cfg.BufferMs,
		ResampleQuality: cfg.ResampleQuality,
		BitDepth:        cfg.BitDepth,
	})
	if err != nil {
		return fmt.Errorf("player: %w", err)
	}
	defer p.Close()

	if spotifyProv != nil {
		p.RegisterStreamerFactory("spotify:", spotifyProv.NewStreamer)
	}

	if tidalProv != nil {
		// Tidal tracks carry tidal:// URIs; the provider resolves them to a
		// fresh signed URL or DASH segment list when playback starts.
		p.RegisterSourceResolver(tidal.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
			u, segments, err := tidalProv.ResolveSource(uri)
			return player.ResolvedSource{URL: u, Segments: segments}, err
		})
	}

	if lyrionClient != nil {
		p.RegisterSourceResolver(lyrion.TrackURIPrefix, func(uri string) (player.ResolvedSource, error) {
			u, segments, err := lyrionClient.ResolveSource(uri)
			return player.ResolvedSource{URL: u, Segments: segments}, err
		})
	}

	p.RegisterBufferedURLMatcher(isBufferedProviderURL)

	// Pull now-playing for stations that carry no inline ICY metadata (NTS, FIP).
	p.RegisterStreamMetadataResolver(radiometa.Resolver)

	cfg.ApplyPlayer(p)
	cfg.ApplyPlaylist(pl)
	ui.SetPadding(cfg.PaddingH, cfg.PaddingV)

	if daemon {
		if cfg.EQPreset != "" && cfg.EQPreset != "Custom" {
			if preset, ok := model.EQPresetByName(cfg.EQPreset); ok {
				for i, gain := range preset.Bands {
					p.SetEQBand(i, gain)
				}
			}
		}
		return runDaemon(p, pl, localProv, providers, cfg.AutoPlay, cfg.EQPreset)
	}

	themes := theme.LoadAll()

	pluginBroker := ipc.NewBroker()
	defer pluginBroker.Close()

	luaMgr, luaErr := luaplugin.New(cfg.Plugins, pluginBroker)
	if luaErr != nil {
		fmt.Fprintf(os.Stderr, "lua plugins: %v\n", luaErr)
	}
	if luaMgr != nil {
		luaMgr.SetReservedKeys(model.ReservedKeys())
		defer luaMgr.Close()
	}

	m := model.New(p, pl, providers, defaultProvider, localProv, themes, luaMgr, config.SaveFunc{})
	m.SetIPCBroker(pluginBroker)
	m.SetCustomEQBands(cfg.EQ)
	m.SetVisVolumeLinked(cfg.VisVolumeLinked)
	m.SetVisualizer60FPS(visualizer60FPS)

	if luaMgr != nil {
		luaMgr.SetStateProvider(luaplugin.StateProvider{
			PlayerState: func() string {
				if !p.IsPlaying() {
					return "stopped"
				}
				if p.IsPaused() {
					return "paused"
				}
				return "playing"
			},
			Position:      func() float64 { return p.Position().Seconds() },
			Duration:      func() float64 { return p.Duration().Seconds() },
			Volume:        func() float64 { return p.Volume() },
			Speed:         func() float64 { return p.Speed() },
			Mono:          func() bool { return p.Mono() },
			RepeatMode:    func() string { return pl.Repeat().String() },
			Shuffle:       func() bool { return pl.Shuffled() },
			EQBands:       func() [10]float64 { return p.EQBands() },
			TrackTitle:    func() string { t, _ := pl.Current(); return t.Title },
			TrackArtist:   func() string { t, _ := pl.Current(); return t.Artist },
			TrackAlbum:    func() string { t, _ := pl.Current(); return t.Album },
			TrackGenre:    func() string { t, _ := pl.Current(); return t.Genre },
			TrackYear:     func() int { t, _ := pl.Current(); return t.Year },
			TrackNumber:   func() int { t, _ := pl.Current(); return t.TrackNumber },
			TrackPath:     func() string { t, _ := pl.Current(); return t.Path },
			TrackIsStream: func() bool { t, _ := pl.Current(); return t.Stream },
			TrackDuration: func() int { t, _ := pl.Current(); return t.DurationSecs },
			PlaylistCount: func() int { return pl.Len() },
			CurrentIndex:  func() int { return pl.Index() },
			QueueList: func() []luaplugin.QueueEntry {
				tracks := pl.Tracks()
				out := make([]luaplugin.QueueEntry, len(tracks))
				for i, t := range tracks {
					out[i] = luaplugin.QueueEntry{
						Title:  t.Title,
						Artist: t.Artist,
						Album:  t.Album,
						Path:   t.Path,
						Index:  i,
						Queued: pl.QueuePosition(i) >= 0,
					}
				}
				return out
			},
		})
	}

	if luaMgr != nil {
		if names := luaMgr.Visualizers(); len(names) > 0 {
			m.RegisterLuaVisualizers(names, luaMgr.RenderVis)
		}
	}

	m.SetSeekStepLarge(cfg.SeekStepLargeDuration())
	m.SetInitialDirectory(cfg.InitialDirectory)
	m.SetPendingURLs(resolved.Pending)
	if cfg.Playlist != "" && len(resolved.Tracks) == 0 && len(resolved.Pending) == 0 {
		m.SetLoadedPlaylist(cfg.Playlist)
	}
	if len(resolved.Tracks) == 0 && len(resolved.Pending) == 0 && pl.Len() == 0 {
		m.StartInProvider()
	}
	if cfg.EQPreset != "" && cfg.EQPreset != "Custom" {
		m.SetEQPreset(cfg.EQPreset, nil)
	}
	if cfg.Theme != "" {
		m.SetTheme(cfg.Theme)
	}
	if cfg.Visualizer != "" {
		m.SetVisualizer(cfg.Visualizer)
	}
	if cfg.AutoPlay {
		m.SetAutoPlay(true)
	}
	if cfg.LowPower {
		m.SetLowPower(true)
	}
	if cfg.Simplified {
		m.SetSimplified(true)
	}

	if rs := resume.Load(); rs.Path != "" && rs.PositionSec > 0 {
		// Mixcloud is commonly opened from its provider browser rather than a
		// positional URL. Arm only that provider's browser-started resume while
		// preserving cliamp's existing positional-file behavior elsewhere.
		if playlist.IsMixcloudURL(rs.Path) || (!defaultRadio && len(positional) > 0) {
			m.SetResume(rs.Path, rs.PositionSec)
		}
	}

	progOpts := []tea.ProgramOption{tea.WithFPS(defaultUIFPS)}
	if cfg.LowPower {
		progOpts[0] = tea.WithFPS(lowPowerUIFPS)
	}
	prog := tea.NewProgram(m, progOpts...)

	if spotifyProv != nil {
		spotify.SetAuthURLObserver(func(u string) {
			prog.Send(model.ProvAuthURLMsg{ProviderName: spotifyProv.Name(), URL: u})
		})
		defer spotify.SetAuthURLObserver(nil)
	}
	if qobuzProv != nil {
		qobuz.SetAuthURLObserver(func(u string) {
			prog.Send(model.ProvAuthURLMsg{ProviderName: qobuzProv.Name(), URL: u})
		})
		defer qobuz.SetAuthURLObserver(nil)
	}
	if tidalProv != nil {
		tidal.SetAuthURLObserver(func(u string) {
			prog.Send(model.ProvAuthURLMsg{ProviderName: tidalProv.Name(), URL: u})
		})
		defer tidal.SetAuthURLObserver(nil)
	}

	svc, svcErr := wireMediaCtl(prog)
	if svcErr != nil {
		applog.Warn("media control (MPRIS/NowPlaying) unavailable: %v", svcErr)
	} else if svc != nil {
		defer svc.Close()
	}

	if luaMgr != nil {
		luaMgr.SetControlProvider(luaplugin.ControlProvider{
			SetVolume:   func(db float64) { p.SetVolume(db) },
			SetSpeed:    func(ratio float64) { p.SetSpeed(ratio) },
			SetEQBand:   func(band int, db float64) { prog.Send(model.SetEQBandMsg{Band: band, Gain: db}) },
			ToggleMono:  func() { p.ToggleMono() },
			TogglePause: func() { p.TogglePause() },
			Stop:        func() { p.Stop() },
			Seek: func(secs float64) {
				_ = p.Seek(time.Duration(secs * float64(time.Second)))
			},
			SetEQPreset: func(name string, bands *[10]float64) {
				prog.Send(model.SetEQPresetMsg{Name: name, Bands: bands})
			},
			Next: func() { prog.Send(playback.NextMsg{}) },
			Prev: func() { prog.Send(playback.PrevMsg{}) },
			QueueAdd: func(path string) {
				prog.Send(model.PluginQueueMsg{Op: "add", Path: path})
			},
			QueueJump: func(index int) {
				prog.Send(model.PluginQueueMsg{Op: "jump", Index: index})
			},
			QueueRemove: func(index int) {
				prog.Send(model.PluginQueueMsg{Op: "remove", Index: index})
			},
			QueueMove: func(from, to int) {
				prog.Send(model.PluginQueueMsg{Op: "move", Index: from, To: to})
			},
		})
		luaMgr.SetUIProvider(luaplugin.UIProvider{
			ShowMessage: func(text string, duration time.Duration) {
				prog.Send(model.ShowStatusMsg{Text: text, Duration: duration})
			},
		})
	}

	ipcSrv, ipcErr := ipc.NewServerWithBroker(ipc.DefaultSocketPath(), pluginBroker)
	if ipcErr != nil {
		fmt.Fprintf(os.Stderr, "ipc: %v\n", ipcErr)
	} else {
		defer ipcSrv.Close()
		ipcSrv.SetV2Dispatcher(newTUIV2Dispatcher(prog, ipcSrv.JobStore(), luaMgr))
		if luaMgr == nil {
			operations := ipc.DefaultOperationRegistry()
			operations.Unregister("plugin.call", "plugin.commands")
			ipcSrv.SetOperationRegistry(operations)
		}
		go publishV2JobEvents(ipcSrv.Done(), ipcSrv.JobStore(), pluginBroker)
	}

	finalModel, err := mediactl.Run(prog, svc)
	if err != nil {
		return err
	}

	if fm, ok := finalModel.(model.Model); ok {
		themeName := fm.ThemeName()
		if themeName == theme.DefaultName {
			themeName = ""
		}
		_ = config.Save("theme", fmt.Sprintf("%q", themeName))

		if path, secs, pl := fm.ResumeState(); path != "" && secs > 0 {
			resume.Save(path, secs, pl)
		}
	}

	return nil
}

func newTUIV2Dispatcher(prog *tea.Program, jobs *ipc.JobStore, plugins *luaplugin.Manager) ipc.V2Dispatcher {
	return ipc.V2DispatcherFunc(func(ctx context.Context, request ipc.V2Request) (ipc.V2Result, *ipc.V2Error) {
		if request.Operation == "runtime.snapshot" || request.Operation == "runtime.status" {
			request.Method = "state.get"
			request.Operation = ""
		}
		switch request.Method {
		case "state.get", "spectrum.get":
			reply := make(chan model.V2RequestResult, 1)
			go prog.Send(model.V2RequestMsg{Request: request, Reply: reply})
			select {
			case result := <-reply:
				return result.Result, result.Error
			case <-ctx.Done():
				return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeCanceled, Message: ipc.V2MessageCanceled}
			case <-time.After(3 * time.Second):
				return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeUnavailable, Message: ipc.V2MessageUnavailable}
			}
		}

		job, err := jobs.CreateWithContext(ctx, request.Operation)
		if err != nil {
			return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeConflict, Message: ipc.V2MessageConflict}
		}
		if request.Operation == "plugin.call" || request.Operation == "plugin.commands" {
			go runV2PluginJob(jobs, job.ID, request, plugins)
			return ipc.V2Result{Job: &job}, nil
		}
		// Program.Send may wait for the TUI update loop. Job submission itself
		// stays non-blocking so the IPC response can always acknowledge the job.
		go prog.Send(model.V2RequestMsg{Request: request, Jobs: jobs, JobID: job.ID})
		return ipc.V2Result{Job: &job}, nil
	})
}

func runV2PluginJob(jobs *ipc.JobStore, jobID string, request ipc.V2Request, plugins *luaplugin.Manager) {
	ctx, err := jobs.Start(jobID)
	if err != nil || ctx.Err() != nil {
		return
	}
	if plugins == nil {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeUnavailable, Message: ipc.V2MessageUnavailable})
		return
	}
	if request.Operation == "plugin.commands" {
		data, err := json.Marshal(ipc.Response{OK: true, Items: plugins.CommandList()})
		if err != nil {
			_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal})
			return
		}
		_ = jobs.Succeed(jobID, data)
		return
	}

	var params ipc.Request
	if err := json.Unmarshal(request.Params, &params); err != nil || params.Name == "" || params.Sub == "" {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInvalidParams, Message: ipc.V2MessageInvalidParams})
		return
	}
	output, err := plugins.EmitCommand(params.Name, params.Sub, params.Args)
	if err != nil {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal, Detail: err.Error()})
		return
	}
	data, err := json.Marshal(ipc.Response{OK: true, Output: output})
	if err != nil {
		_ = jobs.Fail(jobID, ipc.V2Error{Code: ipc.V2ErrorCodeInternal, Message: ipc.V2MessageInternal})
		return
	}
	_ = jobs.Succeed(jobID, data)
}

func publishV2JobEvents(done <-chan struct{}, jobs *ipc.JobStore, broker *ipc.Broker) {
	for {
		select {
		case <-done:
			return
		case event := <-jobs.Events():
			data, err := json.Marshal(event)
			if err == nil {
				_ = broker.Publish("runtime.job", data, false)
			}
		}
	}
}

// initLogging always returns a non-nil close func so the caller can defer
// it unconditionally, plus the applied level as a string for diagnostics.
// Errors come back as the third return value; the close func is a no-op
// and the level string is empty in that case.
func initLogging(levelStr string) (func() error, string, error) {
	noop := func() error { return nil }
	level, err := applog.ParseLevel(levelStr)
	if err != nil {
		return noop, "", err
	}
	dir, err := appdir.Dir()
	if err != nil {
		return noop, "", fmt.Errorf("resolve config dir: %w", err)
	}
	closeFn, err := applog.Init(filepath.Join(dir, "cliamp.log"), level)
	if err != nil {
		return noop, "", err
	}
	return closeFn, level.String(), nil
}

func wireMediaCtl(prog *tea.Program) (*mediactl.Service, error) {
	svc, err := mediactl.New(prog.Send)
	if err != nil || svc == nil {
		return svc, err
	}
	go prog.Send(model.AttachNotifier(svc))
	return svc, nil
}

// userIPCError renders ipc.ErrNotRunning as the wording users see. The ipc
// package returns a bare sentinel, so all CLI copy stays in the command layer.
func userIPCError(err error) error {
	if errors.Is(err, ipc.ErrNotRunning) {
		return fmt.Errorf("cliamp is not running (no socket at %s)", ipc.DefaultSocketPath())
	}
	return err
}

func ipcSend(operation string, params ipc.Request) (ipc.Response, error) {
	return ipcSendWithContext(context.Background(), operation, params)
}

// ipcSendLong waits for a V2 job under the supplied deadline. Plugin commands
// can legitimately run for minutes (for example, yt-dlp downloads).
func ipcSendLong(operation string, params ipc.Request, deadline time.Duration) (ipc.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	return ipcSendWithContext(ctx, operation, params)
}

func ipcSendWithContext(ctx context.Context, operation string, params ipc.Request) (ipc.Response, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return ipc.Response{}, fmt.Errorf("marshal %s parameters: %w", operation, err)
	}
	response, err := ipc.SendV2(ipc.DefaultSocketPath(), ipc.V2Request{
		ID:        json.RawMessage(`"cliamp"`),
		Method:    "operation.submit",
		Operation: operation,
		Params:    raw,
	})
	if err != nil {
		return ipc.Response{}, userIPCError(err)
	}
	if err := v2ResponseError(response); err != nil {
		return ipc.Response{}, err
	}
	if response.Job == nil {
		return ipc.Response{}, fmt.Errorf("%s returned no job", operation)
	}
	response, err = waitForV2Job(ctx, response.Job.ID)
	if err != nil {
		return ipc.Response{}, err
	}
	if response.Job == nil {
		return ipc.Response{}, fmt.Errorf("%s completed without a job", operation)
	}
	var result ipc.Response
	if err := json.Unmarshal(response.Job.Result, &result); err != nil {
		return ipc.Response{}, fmt.Errorf("decode %s result: %w", operation, err)
	}
	if !result.OK {
		return result, fmt.Errorf("%s", result.Error)
	}
	return result, nil
}

func ipcState() (ipc.RuntimeSnapshot, error) {
	response, err := ipc.SendV2(ipc.DefaultSocketPath(), ipc.V2Request{ID: json.RawMessage(`"cliamp"`), Method: "state.get"})
	if err != nil {
		return ipc.RuntimeSnapshot{}, userIPCError(err)
	}
	if err := v2ResponseError(response); err != nil {
		return ipc.RuntimeSnapshot{}, err
	}
	if response.Snapshot == nil {
		return ipc.RuntimeSnapshot{}, fmt.Errorf("state response has no snapshot")
	}
	return *response.Snapshot, nil
}

func stateResult(snapshot ipc.RuntimeSnapshot) ipc.Response {
	return ipc.Response{
		OK:         true,
		State:      snapshot.State,
		Track:      snapshot.Track,
		Position:   snapshot.Position,
		Duration:   snapshot.Duration,
		Volume:     snapshot.Volume,
		Playlist:   snapshot.Playlist,
		Index:      snapshot.Index,
		Total:      snapshot.Total,
		Visualizer: snapshot.Visualizer,
		Shuffle:    snapshot.Shuffle,
		Repeat:     snapshot.Repeat,
		Mono:       snapshot.Mono,
		Speed:      snapshot.Speed,
		EQPreset:   snapshot.EQPreset,
		Theme:      snapshot.Theme,
		EQBands:    snapshot.EQBands,
	}
}

func main() {
	appmeta.SetVersion(version)
	app := buildApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
