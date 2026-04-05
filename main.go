package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cliamp/config"
	"cliamp/external/jellyfin"
	"cliamp/external/local"
	"cliamp/external/navidrome"
	"cliamp/external/plex"
	"cliamp/external/radio"
	"cliamp/external/spotify"
	"cliamp/external/ytmusic"
	"cliamp/internal/appmeta"
	"cliamp/internal/resume"
	"cliamp/ipc"
	"cliamp/luaplugin"
	"cliamp/mpris"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/resolve"
	"cliamp/theme"
	"cliamp/ui"
	"cliamp/ui/model"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version string

func run(overrides config.Overrides, positional []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	overrides.Apply(&cfg)

	// Build provider list: Radio is always available, Navidrome and Spotify if configured.
	radioProv := radio.New()
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

	if plexProv := plex.NewFromConfig(cfg.Plex); plexProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "plex", Name: "Plex", Provider: plexProv})
	}

	if jellyProv := jellyfin.NewFromConfig(cfg.Jellyfin); jellyProv != nil {
		providers = append(providers, model.ProviderEntry{Key: "jellyfin", Name: "Jellyfin", Provider: jellyProv})
	}

	var spotifyProv *spotify.SpotifyProvider
	if cfg.Spotify.IsSet() {
		spotifyProv = spotify.New(nil, cfg.Spotify.ClientID)
		providers = append(providers, model.ProviderEntry{Key: "spotify", Name: "Spotify", Provider: spotifyProv})
	}

	var ytProviders ytmusic.Providers
	ytWanted := cfg.YouTubeMusic.IsSetOrFallback(ytmusic.FallbackCredentials)
	if !ytWanted {
		switch cfg.Provider {
		case "yt", "youtube", "ytmusic":
			ytWanted = true
		}
	}
	if ytWanted {
		ytClientID, ytClientSecret := cfg.YouTubeMusic.ResolveCredentials(ytmusic.FallbackCredentials)
		if cfg.YouTubeMusic.CookiesFrom != "" {
			player.SetYTDLCookiesFrom(cfg.YouTubeMusic.CookiesFrom)
		}
		if ytClientID == "" || ytClientSecret == "" {
			fmt.Fprintf(os.Stderr, "YouTube: no credentials available (configure client_id/client_secret in config.toml)\n")
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
				ytProviders = ytmusic.New(nil, ytClientID, ytClientSecret, cfg.YouTubeMusic.CookiesFrom != "")
				providers = append(providers,
					model.ProviderEntry{Key: "yt", Name: "YouTube (All)", Provider: ytProviders.All},
					model.ProviderEntry{Key: "youtube", Name: "YouTube", Provider: ytProviders.Video},
					model.ProviderEntry{Key: "ytmusic", Name: "YouTube Music", Provider: ytProviders.Music},
				)
			}
		}
	}

	if spotifyProv != nil {
		defer spotifyProv.Close()
	}
	if ytProviders.Music != nil {
		defer ytProviders.Music.Close()
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
		cfg.AutoPlay = true
	} else if defaultRadio {
		pl.Add(
			playlist.Track{Path: "http://radio.cliamp.stream/lofi/stream", Title: "Lofi Stream", Stream: true},
			playlist.Track{Path: "http://radio.cliamp.stream/synthwave/stream", Title: "Synthwave Stream", Stream: true},
			playlist.Track{Path: "http://radio.cliamp.stream/edm/stream", Title: "EDM Stream", Stream: true},
		)
	}
	pl.Add(resolved.Tracks...)

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

	p.RegisterBufferedURLMatcher(func(u string) bool {
		return navidrome.IsSubsonicStreamURL(u) || jellyfin.IsStreamURL(u)
	})

	cfg.ApplyPlayer(p)
	cfg.ApplyPlaylist(pl)
	ui.SetPadding(cfg.PaddingH, cfg.PaddingV)

	themes := theme.LoadAll()

	luaMgr, luaErr := luaplugin.New(cfg.Plugins)
	if luaErr != nil {
		fmt.Fprintf(os.Stderr, "lua plugins: %v\n", luaErr)
	}
	if luaMgr != nil {
		defer luaMgr.Close()
	}

	m := model.New(p, pl, providers, defaultProvider, localProv, themes, luaMgr, config.SaveFunc{})

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
		})
	}

	if luaMgr != nil {
		if names := luaMgr.Visualizers(); len(names) > 0 {
			m.RegisterLuaVisualizers(names, luaMgr.RenderVis)
		}
	}

	m.SetSeekStepLarge(cfg.SeekStepLargeDuration())
	m.SetPendingURLs(resolved.Pending)
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
	if cfg.Compact {
		m.SetCompact(true)
	}

	if !defaultRadio && len(positional) > 0 {
		if rs := resume.Load(); rs.Path != "" && rs.PositionSec > 0 {
			m.SetResume(rs.Path, rs.PositionSec)
		}
	}

	prog := tea.NewProgram(m)

	if luaMgr != nil {
		luaMgr.SetControlProvider(luaplugin.ControlProvider{
			SetVolume:   func(db float64) { p.SetVolume(db) },
			SetSpeed:    func(ratio float64) { p.SetSpeed(ratio) },
			SetEQBand:   func(band int, db float64) { p.SetEQBand(band, db) },
			ToggleMono:  func() { p.ToggleMono() },
			TogglePause: func() { p.TogglePause() },
			Stop:        func() { p.Stop() },
			Seek: func(secs float64) {
				_ = p.Seek(time.Duration(secs * float64(time.Second)))
			},
			SetEQPreset: func(name string, bands *[10]float64) {
				prog.Send(model.SetEQPresetMsg{Name: name, Bands: bands})
			},
			Next: func() { prog.Send(mpris.NextMsg{}) },
			Prev: func() { prog.Send(mpris.PrevMsg{}) },
		})
	}

	if svc, err := mpris.New(func(msg interface{}) { prog.Send(msg) }); err == nil && svc != nil {
		defer svc.Close()
		go prog.Send(mpris.InitMsg{Svc: svc})
	}

	ipcSrv, ipcErr := ipc.NewServer(ipc.DefaultSocketPath(), ipc.DispatcherFunc(func(msg interface{}) { prog.Send(msg) }))
	if ipcErr != nil {
		fmt.Fprintf(os.Stderr, "ipc: %v\n", ipcErr)
	} else {
		defer ipcSrv.Close()
	}

	finalModel, err := prog.Run()
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

func ipcSend(req ipc.Request) (ipc.Response, error) {
	resp, err := ipc.Send(ipc.DefaultSocketPath(), req)
	if err != nil {
		return resp, err
	}
	if !resp.OK {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

func main() {
	appmeta.SetVersion(version)
	app := buildApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
