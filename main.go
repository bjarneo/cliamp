// Package main is the entry point for the CLIAMP terminal music player.
package main

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gopxl/beep/v2"

	"cliamp/config"
	"cliamp/external/navidrome"
	"cliamp/mpris"
	"cliamp/player"
	"cliamp/playlist"
	"cliamp/resolve"
	"cliamp/theme"
	"cliamp/ui"
)

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	var provider playlist.Provider
	if c := navidrome.NewFromEnv(); c != nil {
		provider = c
	}

	defer resolve.CleanupYTDL()

	resolved, err := resolve.Args(os.Args[1:])
	if err != nil {
		return err
	}

	if len(resolved.Tracks) == 0 && len(resolved.Pending) == 0 && provider == nil {
		return errors.New(`usage: cliamp <file|folder|url> [...]

  Local files     cliamp track.mp3 song.flac ~/Music
  HTTP stream     cliamp https://example.com/song.mp3
  Radio / M3U     cliamp http://radio.example.com/stream.m3u
  Podcast feed    cliamp https://example.com/podcast/feed.xml
  SoundCloud      cliamp https://soundcloud.com/user/sets/playlist
  YouTube         cliamp https://www.youtube.com/watch?v=...
  Bandcamp        cliamp https://artist.bandcamp.com/album/...

  Navidrome       Set NAVIDROME_URL, NAVIDROME_USER, NAVIDROME_PASS

Formats: mp3, wav, flac, ogg, m4a, aac, opus, wma (aac/opus/wma need ffmpeg)
SoundCloud/YouTube/Bandcamp require yt-dlp (brew install yt-dlp)`)
	}

	pl := playlist.New()
	pl.Add(resolved.Tracks...)

	p := player.New(beep.SampleRate(player.DefaultSampleRate))
	defer p.Close()

	cfg.ApplyPlayer(p)
	cfg.ApplyPlaylist(pl)

	themes := theme.LoadAll()

	m := ui.NewModel(p, pl, provider, themes)
	m.SetPendingURLs(resolved.Pending)
	if cfg.EQPreset != "" && cfg.EQPreset != "Custom" {
		m.SetEQPreset(cfg.EQPreset)
	}
	if cfg.Theme != "" {
		m.SetTheme(cfg.Theme)
	}

	prog := tea.NewProgram(m, tea.WithAltScreen())

	if svc, err := mpris.New(func(msg interface{}) { prog.Send(msg) }); err == nil && svc != nil {
		defer svc.Close()
		go prog.Send(mpris.InitMsg{Svc: svc})
	}

	finalModel, err := prog.Run()
	if err != nil {
		return err
	}

	// Persist theme selection across restarts.
	if fm, ok := finalModel.(ui.Model); ok {
		cfg.Theme = fm.ThemeName()
		if cfg.Theme == theme.DefaultName {
			cfg.Theme = ""
		}
		_ = config.Save(cfg)
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
