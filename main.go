// Package main is the entry point for the CLIAMP terminal music player.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cliamp/config"
	"cliamp/external/local"
	"cliamp/external/navidrome"
	"cliamp/external/plex"
	"cliamp/external/radio"
	"cliamp/external/spotify"
	"cliamp/external/ytmusic"
	"cliamp/internal/resume"
	"cliamp/luaplugin"
	"cliamp/mpris"
	"cliamp/pluginmgr"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/resolve"
	"cliamp/theme"
	"cliamp/ui"
	"cliamp/upgrade"
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
	var providers []ui.ProviderEntry
	providers = append(providers, ui.ProviderEntry{Key: "radio", Name: "Radio", Provider: radioProv})

	var navClient *navidrome.NavidromeClient
	if c := navidrome.NewFromConfig(cfg.Navidrome); c != nil {
		navClient = c
	} else if c := navidrome.NewFromEnv(); c != nil {
		navClient = c
	}
	if navClient != nil {
		providers = append(providers, ui.ProviderEntry{Key: "navidrome", Name: "Navidrome", Provider: navClient})
	}

	if plexProv := plex.NewFromConfig(cfg.Plex); plexProv != nil {
		providers = append(providers, ui.ProviderEntry{Key: "plex", Name: "Plex", Provider: plexProv})
	}

	var spotifyProv *spotify.SpotifyProvider
	if cfg.Spotify.IsSet() {
		spotifyProv = spotify.New(nil, cfg.Spotify.ClientID)
		providers = append(providers, ui.ProviderEntry{Key: "spotify", Name: "Spotify", Provider: spotifyProv})
	}

	var ytProviders ytmusic.Providers
	// Enable YouTube providers if any [yt]/[youtube]/[ytmusic] config exists,
	// or if the --provider flag selects a YouTube provider,
	// or if fallback credentials are available.
	ytWanted := cfg.YouTubeMusic.IsSetOrFallback(ytmusic.FallbackCredentials)
	if !ytWanted {
		// Also enable if --provider flag selects a YouTube provider.
		switch cfg.Provider {
		case "yt", "youtube", "ytmusic":
			ytWanted = true
		}
	}
	if ytWanted {
		ytClientID, ytClientSecret := cfg.YouTubeMusic.ResolveCredentials(ytmusic.FallbackCredentials)
		// Configure yt-dlp cookie source for YouTube Music uploads/private tracks.
		if cfg.YouTubeMusic.CookiesFrom != "" {
			player.SetYTDLCookiesFrom(cfg.YouTubeMusic.CookiesFrom)
		}
		if ytClientID == "" || ytClientSecret == "" {
			fmt.Fprintf(os.Stderr, "YouTube: no credentials available (configure client_id/client_secret in config.toml)\n")
		} else {
			// YouTube playback requires yt-dlp. Check early and offer to install.
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
					ui.ProviderEntry{Key: "yt", Name: "YouTube (All)", Provider: ytProviders.All},
					ui.ProviderEntry{Key: "youtube", Name: "YouTube", Provider: ytProviders.Video},
					ui.ProviderEntry{Key: "ytmusic", Name: "YouTube Music", Provider: ytProviders.Music},
				)
			}
		}
	}

	localProv := local.New()

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

	// Determine default provider key.
	defaultProvider := cfg.Provider
	if defaultProvider == "" {
		defaultProvider = "radio"
	}

	// No args + radio provider: stream the built-in radio directly.
	if len(positional) == 0 && defaultProvider == "radio" {
		resolved.Pending = append(resolved.Pending, "https://radio.cliamp.stream/streams.m3u")
	}

	pl := playlist.New()
	pl.Add(resolved.Tracks...)

	// Resolve sample rate: 0 means auto-detect from the system's default
	// output audio device (e.g. 48 kHz for USB-C headphones). Falls back
	// to 44100 Hz if detection is unavailable or returns an unusable value.
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

	// Register Spotify streamer factory so spotify: URIs are decoded
	// through go-librespot instead of the normal file/HTTP pipeline.
	if spotifyProv != nil {
		p.SetStreamerFactory(spotifyProv.NewStreamer)
	}

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

	m := ui.NewModel(p, pl, providers, defaultProvider, localProv, spotifyProv, themes, cfg.Navidrome, navClient, luaMgr)

	// Wire Lua plugin state provider with read-only access to player/playlist.
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
			Position:    func() float64 { return p.Position().Seconds() },
			Duration:    func() float64 { return p.Duration().Seconds() },
			Volume:      func() float64 { return p.Volume() },
			Speed:       func() float64 { return p.Speed() },
			Mono:        func() bool { return p.Mono() },
			RepeatMode:  func() string { return pl.Repeat().String() },
			Shuffle:     func() bool { return pl.Shuffled() },
			EQBands:     func() [10]float64 { return p.EQBands() },
			TrackTitle:  func() string { t, _ := pl.Current(); return t.Title },
			TrackArtist: func() string { t, _ := pl.Current(); return t.Artist },
			TrackAlbum:  func() string { t, _ := pl.Current(); return t.Album },
			TrackGenre:  func() string { t, _ := pl.Current(); return t.Genre },
			TrackYear:   func() int { t, _ := pl.Current(); return t.Year },
			TrackNumber: func() int { t, _ := pl.Current(); return t.TrackNumber },
			TrackPath:   func() string { t, _ := pl.Current(); return t.Path },
			TrackIsStream: func() bool { t, _ := pl.Current(); return t.Stream },
			TrackDuration: func() int { t, _ := pl.Current(); return t.DurationSecs },
			PlaylistCount: func() int { return pl.Len() },
			CurrentIndex:  func() int { return pl.Index() },
		})
	}

	// Register Lua visualizers into the visualizer cycle.
	if luaMgr != nil {
		if names := luaMgr.Visualizers(); len(names) > 0 {
			m.RegisterLuaVisualizers(names, luaMgr.RenderVis)
		}
	}

	m.SetSeekStepLarge(cfg.SeekStepLargeDuration())
	m.SetPendingURLs(resolved.Pending)
	if len(resolved.Tracks) == 0 && len(resolved.Pending) == 0 {
		m.StartInProvider()
	}
	if cfg.EQPreset != "" && cfg.EQPreset != "Custom" {
		m.SetEQPreset(cfg.EQPreset)
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

	// PositionSec == 0 is indistinguishable from "never played"; skip resume.
	if rs := resume.Load(); rs.Path != "" && rs.PositionSec > 0 {
		m.SetResume(rs.Path, rs.PositionSec)
	}

	prog := tea.NewProgram(m, tea.WithAltScreen())

	// Wire Lua plugin control provider (needs prog.Send for next/prev).
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
			SetEQPreset: func(name string) { prog.Send(ui.SetEQPresetMsg{Name: name}) },
			Next:         func() { prog.Send(mpris.NextMsg{}) },
			Prev:         func() { prog.Send(mpris.PrevMsg{}) },
		})
	}

	if svc, err := mpris.New(func(msg interface{}) { prog.Send(msg) }); err == nil && svc != nil {
		defer svc.Close()
		go prog.Send(mpris.InitMsg{Svc: svc})
	}

	finalModel, err := prog.Run()
	if err != nil {
		return err
	}

	// Persist theme selection and resume state across restarts.
	if fm, ok := finalModel.(ui.Model); ok {
		themeName := fm.ThemeName()
		if themeName == theme.DefaultName {
			themeName = ""
		}
		_ = config.Save("theme", fmt.Sprintf("%q", themeName)) // best-effort — non-critical persistence

		if path, secs := fm.ResumeState(); path != "" && secs > 0 {
			resume.Save(path, secs)
		}
	}

	return nil
}

const helpText = `cliamp — retro terminal music player

Usage: cliamp [flags] <file|folder|url> [...]

Playback:
  --volume <dB>           Volume in dB, range [-30, +6] (e.g. --volume -5)
  --shuffle
  --repeat <off|all|one>
  --mono / --no-mono
  --auto-play             Start playback immediately

Audio engine:
  --sample-rate <Hz>      Output sample rate (0=auto, 22050, 44100, 48000, 96000, 192000)
  --buffer-ms <ms>        Speaker buffer in milliseconds (50–500)
  --resample-quality <n>  Resample quality factor (1–4)
  --bit-depth <n>         PCM bit depth: 16 (default) or 32 (lossless)

Provider:
  --provider <name>       Default provider: radio, navidrome, plex, spotify, yt, youtube, ytmusic (default: radio)

Appearance:
  --compact               Compact mode (cap width at 80 columns)
  --theme <name>          UI theme name
  --visualizer <mode>     Visualizer mode (Bars, Bricks, Columns, Wave, Scatter, Flame, Retro, Pulse, Matrix, Binary, None)
  --eq-preset <name>      EQ preset name (e.g. "Bass Boost")

Plugins:
  cliamp plugins list                List installed plugins
  cliamp plugins install <source>    Install a plugin (URL, user/repo, gitlab:user/repo, codeberg:user/repo)
  cliamp plugins remove <name>       Remove a plugin

General:
  -h, --help              Show this help message
  -v, --version           Show the current version
  --upgrade               Upgrade cliamp to the latest release

Examples:
  cliamp track.mp3 song.flac ~/Music
  cliamp --shuffle --volume -5 track.mp3
  cliamp track.mp3 --repeat all --mono
  cliamp --auto-play --shuffle ~/Music
  cliamp --eq-preset "Bass Boost" ~/Music
  cliamp https://example.com/song.mp3
  cliamp http://radio.example.com/stream.m3u
  cliamp search "rick astley"            # search YouTube
  cliamp search-sc "lofi beats"            # search SoundCloud
  cliamp https://soundcloud.com/user/sets/playlist
  cliamp https://www.youtube.com/watch?v=...

Environment:
  NAVIDROME_URL, NAVIDROME_USER, NAVIDROME_PASS   Navidrome server (env fallback)

Config:    ~/.config/cliamp/config.toml  (see config.toml.example)
Radios:    ~/.config/cliamp/radios.toml
Playlists: ~/.config/cliamp/playlists/*.toml
Formats:   mp3, wav, flac, ogg, m4a, aac, opus, wma (aac/opus/wma need ffmpeg)
SoundCloud/YouTube/Bandcamp require yt-dlp`

const pluginsHelpText = `cliamp plugins — manage Lua plugins

Usage: cliamp plugins <command> [args]

Commands:
  list                    List installed plugins
  install <source>        Install a plugin
  remove <name>           Remove a plugin

Install sources (repos must be named cliamp-plugin-<name>):
  user/cliamp-plugin-foo            GitHub repository
  user/cliamp-plugin-foo@v1.0       GitHub repository at a specific tag
  gitlab:user/repo        GitLab repository
  codeberg:user/repo      Codeberg repository
  https://example.com/p.lua   Direct URL

Examples:
  cliamp plugins list
  cliamp plugins install bjarneo/cliamp-plugin-lastfm
  cliamp plugins install bjarneo/cliamp-plugin-lastfm@v1.0
  cliamp plugins install gitlab:user/my-visualizer
  cliamp plugins install codeberg:user/my-plugin
  cliamp plugins install https://example.com/my-plugin.lua
  cliamp plugins remove lastfm`

func main() {
	action, overrides, positional, err := config.ParseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	switch action {
	case "help":
		fmt.Println(helpText)
		return
	case "version":
		if version == "" {
			fmt.Println("cliamp (dev build)")
		} else {
			fmt.Printf("cliamp %s\n", version)
		}
		return
	case "upgrade":
		if err := upgrade.Run(version); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case "plugins":
		fmt.Println(pluginsHelpText)
		return
	case "plugins-list":
		if err := pluginmgr.List(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case "plugins-install":
		if len(positional) == 0 {
			fmt.Fprintln(os.Stderr, "usage: cliamp plugins install <source>")
			os.Exit(1)
		}
		if err := pluginmgr.Install(positional[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case "plugins-remove":
		if len(positional) == 0 {
			fmt.Fprintln(os.Stderr, "usage: cliamp plugins remove <name>")
			os.Exit(1)
		}
		if err := pluginmgr.Remove(positional[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := run(overrides, positional); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
