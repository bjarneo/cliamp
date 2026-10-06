package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	cli "github.com/urfave/cli/v3"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/cmd"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/qobuz"
	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/external/tidal"
	"github.com/bjarneo/cliamp/external/ytmusic"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/pluginmgr"
	"github.com/bjarneo/cliamp/resolve"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
	"github.com/bjarneo/cliamp/upgrade"
)

func buildApp() *cli.Command {
	rootFlags := []cli.Flag{
		&cli.Float64Flag{Name: "vol", Usage: "startup volume in dB [volume_min, +6]"},
		&cli.BoolWithInverseFlag{Name: "shuffle", Usage: "shuffle playback"},
		&cli.StringFlag{Name: "repeat", Usage: "repeat mode: off, all, one"},
		&cli.BoolWithInverseFlag{Name: "mono", Usage: "mono output"},
		&cli.BoolWithInverseFlag{Name: "auto-play", Usage: "start playback immediately"},
		&cli.BoolWithInverseFlag{Name: "simplified", Usage: "simplified playback view (no visualizer or playlist)"},
		&cli.BoolWithInverseFlag{Name: "help-bar", Usage: "show the key-binding hint bar (? still opens the full keymap)", Value: true},
		&cli.BoolWithInverseFlag{Name: "expanded", Usage: "start with the playlist expanded (the Ctrl+X state)"},
		&cli.StringFlag{Name: "provider", Usage: providerFlagUsage()},
		&cli.StringFlag{Name: "start-theme", Usage: "UI theme name"},
		&cli.StringFlag{Name: "visualizer", Usage: "visualizer mode"},
		&cli.BoolFlag{Name: "visualizer-60fps", Usage: "render visualizer at 60 FPS (higher CPU use)"},
		&cli.StringFlag{Name: "eq-preset", Usage: "EQ preset name"},
		&cli.IntFlag{Name: "sample-rate", Usage: "output sample rate in Hz (0=auto)", HideDefault: true},
		&cli.IntFlag{Name: "buffer-ms", Usage: "speaker buffer in milliseconds (50-5000)", HideDefault: true},
		&cli.IntFlag{Name: "resample-quality", Usage: "resample quality factor (1-4)", HideDefault: true},
		&cli.IntFlag{Name: "bit-depth", Usage: "PCM bit depth: 16 or 32", HideDefault: true},
		&cli.StringFlag{Name: "audio-device", Usage: "audio output device (use 'list' to show)"},
		&cli.StringFlag{Name: "playlist", Usage: "load a local TOML playlist by name and start playing"},
		&cli.StringFlag{Name: "log-level", Usage: "log level: debug, info, warn, error"},
		&cli.BoolWithInverseFlag{Name: "expand-playlist", Usage: "expand YouTube and YouTube Music list= URLs to the full playlist", Value: true},
		&cli.BoolWithInverseFlag{Name: "low-power", Usage: "low-power mode: reduce CPU by lowering UI cadence and disabling visualization"},
		&cli.BoolFlag{Name: "daemon", Aliases: []string{"d"}, Usage: "run headless (no TUI), serving IPC for scripts/Waybar"},
	}

	return &cli.Command{
		Name:                  "cliamp",
		Usage:                 "retro terminal music player",
		Version:               buildVersion(),
		EnableShellCompletion: true,
		Flags:                 rootFlags,
		Action: func(ctx context.Context, c *cli.Command) error {
			if strings.EqualFold(c.String("audio-device"), "list") {
				return listAudioDevices()
			}
			ov, err := overridesFromFlags(c)
			if err != nil {
				return err
			}
			return run(ov, c.Args().Slice(), c.Bool("daemon"), c.Bool("visualizer-60fps"))
		},
		Commands: []*cli.Command{
			upgradeCommand(),
			pluginsCommand(),
			playlistCommand(),
			historyCommand(),
			radioCommand(),
			setupCommand(),
			spotifyCommand(),
			qobuzCommand(),
			tidalCommand(),
			ytmusicCommand(),
			ipcSimpleCommand("play", "resume playback"),
			ipcSimpleCommand("pause", "pause playback"),
			ipcSimpleCommand("toggle", "play/pause toggle"),
			ipcSimpleCommand("next", "next track"),
			ipcSimpleCommand("prev", "previous track"),
			ipcSimpleCommand("stop", "stop playback"),
			statusCommand(),
			volumeCommand(),
			seekCommand(),
			loadCommand(),
			queueCommand(),
			themeCommand(),
			visCommand(),
			visStreamCommand(),
			shuffleCommand(),
			repeatCommand(),
			monoCommand(),
			speedCommand(),
			eqCommand(),
			deviceCommand(),
			remoteCommand(),
			openCommand(),
			protocolCommand(),
		},
	}
}

func listAudioDevices() error {
	devices, err := player.ListAudioDevices()
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		fmt.Println("No audio output devices found.")
	} else {
		for _, d := range devices {
			marker := "  "
			if d.Active {
				marker = "* "
			}
			fmt.Printf("%s%-50s %s\n", marker, d.Description, d.Name)
		}
	}
	return nil
}

func overridesFromFlags(c *cli.Command) (config.Overrides, error) {
	var ov config.Overrides
	if c.IsSet("vol") {
		v := c.Float64("vol")
		ov.Volume = &v
	}
	if c.IsSet("shuffle") {
		v := c.Bool("shuffle")
		ov.Shuffle = &v
	}
	if c.IsSet("repeat") {
		v := strings.ToLower(c.String("repeat"))
		switch v {
		case "off", "all", "one":
			ov.Repeat = &v
		default:
			return ov, fmt.Errorf("--repeat must be off, all, or one (got %q)", v)
		}
	}
	if c.IsSet("mono") {
		v := c.Bool("mono")
		ov.Mono = &v
	}
	if c.IsSet("auto-play") {
		v := c.Bool("auto-play")
		ov.Play = &v
	}
	if c.IsSet("simplified") {
		v := c.Bool("simplified")
		ov.Simplified = &v
	}
	if c.IsSet("help-bar") {
		v := !c.Bool("help-bar")
		ov.HideHelpBar = &v
	}
	if c.IsSet("expanded") {
		v := c.Bool("expanded")
		ov.Expanded = &v
	}
	if c.IsSet("provider") {
		v, err := parseProviderKey(c.String("provider"))
		if err != nil {
			return ov, err
		}
		ov.Provider = &v
	}
	if c.IsSet("start-theme") {
		v := c.String("start-theme")
		ov.Theme = &v
	}
	if c.IsSet("visualizer") {
		v := c.String("visualizer")
		ov.Visualizer = &v
	}
	if c.IsSet("eq-preset") {
		v := c.String("eq-preset")
		ov.EQPreset = &v
	}
	if c.IsSet("sample-rate") {
		v := int(c.Int("sample-rate"))
		ov.SampleRate = &v
	}
	if c.IsSet("buffer-ms") {
		v := int(c.Int("buffer-ms"))
		ov.BufferMs = &v
	}
	if c.IsSet("resample-quality") {
		v := int(c.Int("resample-quality"))
		ov.ResampleQuality = &v
	}
	if c.IsSet("bit-depth") {
		v := int(c.Int("bit-depth"))
		ov.BitDepth = &v
	}
	if c.IsSet("audio-device") {
		v := c.String("audio-device")
		ov.AudioDevice = &v
	}
	if c.IsSet("playlist") {
		v := c.String("playlist")
		ov.Playlist = &v
	}
	if c.IsSet("log-level") {
		v := c.String("log-level")
		if _, err := applog.ParseLevel(v); err != nil {
			return ov, fmt.Errorf("--log-level: %w", err)
		}
		ov.LogLevel = &v
	}
	if c.IsSet("low-power") {
		v := c.Bool("low-power")
		ov.LowPower = &v
	}
	if c.IsSet("expand-playlist") {
		v := c.Bool("expand-playlist")
		ov.ExpandPlaylist = &v
	}
	return ov, nil
}

func upgradeCommand() *cli.Command {
	return &cli.Command{
		Name:  "upgrade",
		Usage: "upgrade cliamp to the latest stable release",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "prerelease", Usage: "upgrade to the newest release, prereleases included"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return upgrade.Run(version, c.Bool("prerelease"))
		},
	}
}

func pluginsCommand() *cli.Command {
	return &cli.Command{
		Name:  "plugins",
		Usage: "manage Lua plugins",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "list installed plugins",
				Action: func(ctx context.Context, c *cli.Command) error {
					return pluginmgr.List()
				},
			},
			{
				Name:      "install",
				Usage:     "install a plugin",
				ArgsUsage: "<source>",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "approve plugin trust without prompting"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp plugins install <source>")
					}
					return pluginmgr.Install(c.Args().First(), c.Bool("yes"))
				},
			},
			{
				Name:      "trust",
				Usage:     "approve the current contents of an installed plugin",
				ArgsUsage: "<name>",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "approve plugin trust without prompting"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp plugins trust <name>")
					}
					return pluginmgr.Trust(c.Args().First(), c.Bool("yes"))
				},
			},
			{
				Name:      "remove",
				Usage:     "remove a plugin",
				ArgsUsage: "<name>",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp plugins remove <name>")
					}
					return pluginmgr.Remove(c.Args().First())
				},
			},
			{
				Name:      "call",
				Usage:     "invoke a plugin command in the running cliamp",
				ArgsUsage: "<plugin> <command> [args...]",
				Action: func(ctx context.Context, c *cli.Command) error {
					args := c.Args().Slice()
					if len(args) < 2 {
						return fmt.Errorf("usage: cliamp plugins call <plugin> <command> [args...]")
					}
					resp, err := ipcSendWithin("plugin.call", ipc.Request{
						Name: args[0],
						Sub:  args[1],
						Args: args[2:],
					}, 6*time.Minute)
					if err != nil {
						return err
					}
					if resp.Output != "" {
						fmt.Println(resp.Output)
					}
					return nil
				},
			},
			{
				Name:  "commands",
				Usage: "list plugin commands registered in the running cliamp",
				Action: func(ctx context.Context, c *cli.Command) error {
					resp, err := ipcSend("plugin.commands", ipc.Request{})
					if err != nil {
						return err
					}
					if len(resp.Items) == 0 {
						fmt.Println("No plugin commands registered.")
						return nil
					}
					for _, item := range resp.Items {
						fmt.Println(item)
					}
					return nil
				},
			},
		},
	}
}

func protocolCommand() *cli.Command {
	return &cli.Command{
		Name:  "protocol",
		Usage: "register cliamp:// links with the desktop",
		Description: "Makes cliamp the handler for cliamp:// links, so clicking one plays\n" +
			"or queues its target. install.sh already registers the scheme; use\n" +
			"these commands after a go install build, to point the scheme at a\n" +
			"different binary, or to remove the registration.",
		Commands: []*cli.Command{
			{
				Name:  "register",
				Usage: "make cliamp the handler for cliamp:// links",
				Action: func(ctx context.Context, c *cli.Command) error {
					return cmd.ProtocolRegister(os.Stdout)
				},
			},
			{
				Name:  "unregister",
				Usage: "remove the cliamp:// handler registration",
				Action: func(ctx context.Context, c *cli.Command) error {
					return cmd.ProtocolUnregister(os.Stdout)
				},
			},
			{
				Name:  "status",
				Usage: "report whether cliamp:// is registered",
				Action: func(ctx context.Context, c *cli.Command) error {
					return cmd.ProtocolStatus(os.Stdout)
				},
			},
		},
	}
}

// radioCommand is an easter egg: the "who's listening" globe from
// cliamp.stream, in the terminal. The website's stats card shows the command
// as a prompt; it is left out of the help listing so that stays the only hint.
func radioCommand() *cli.Command {
	return &cli.Command{
		Name:   "radio",
		Usage:  "who is listening to the cliamp radio channels",
		Hidden: true,
		Description: "Shows live listener statistics for the cliamp radio channels from\n" +
			"radio.cliamp.stream: listeners now on the live streams and the channel\n" +
			"playlists, by country and by channel, plus the all-time totals of the\n" +
			"live streams. --globe draws them on a spinning globe, like the one on\n" +
			"cliamp.stream.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "stats", Usage: "print live listener statistics"},
			&cli.BoolFlag{Name: "globe", Usage: "show the statistics on an animated globe (implies --stats)"},
			&cli.BoolFlag{Name: "json", Usage: "print the raw statistics document of the live streams (implies --stats)"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			switch {
			case c.Bool("globe") && c.Bool("json"):
				return fmt.Errorf("--globe and --json cannot be combined")
			case c.Bool("globe"):
				return cmd.RadioGlobe(ctx, c.Root().String("start-theme"))
			case c.Bool("stats") || c.Bool("json"):
				return cmd.RadioStats(ctx, os.Stdout, c.Bool("json"))
			default:
				return cli.ShowSubcommandHelp(c)
			}
		},
	}
}

func setupCommand() *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "interactive wizard to configure remote providers",
		Description: "Walks through configuring Navidrome, Plex, Jellyfin, Spotify,\n" +
			"Qobuz, Tidal, NetEase, Audiobookshelf, and YouTube Music. Validates\n" +
			"connections and writes ~/.config/cliamp/config.toml.",

		Action: func(ctx context.Context, c *cli.Command) error {
			return cmd.Setup()
		},
	}
}

func spotifyCommand() *cli.Command {
	return providerCredsCommand("spotify", "Spotify", spotify.CredsPath, spotify.DeleteCreds)
}

func qobuzCommand() *cli.Command {
	return providerCredsCommand("qobuz", "Qobuz", qobuz.CredsPath, qobuz.DeleteCreds)
}

func tidalCommand() *cli.Command {
	cmd := providerCredsCommand("tidal", "Tidal", tidal.CredsPath, tidal.DeleteCreds)
	cmd.Commands = append(cmd.Commands, &cli.Command{
		Name:      "probe",
		Usage:     "print sanitized playback diagnostics for each quality tier (no tokens or signed URLs)",
		ArgsUsage: "[search query]",
		Action: func(ctx context.Context, c *cli.Command) error {
			query := strings.Join(c.Args().Slice(), " ")
			if query == "" {
				query = "Random Access Memories Get Lucky"
			}
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			return tidal.Probe(probeCtx, os.Stdout, query, cfg.Tidal.ClientID, cfg.Tidal.ClientSecret)
		},
	})
	return cmd
}

func ytmusicCommand() *cli.Command {
	return providerCredsCommand("ytmusic", "YouTube Music", ytmusic.CredsPath, ytmusic.DeleteCreds)
}

// providerCredsCommand builds the `cliamp <provider> reset` subcommand shared
// by providers that cache OAuth credentials on disk.
func providerCredsCommand(key, display string, credsPath func() (string, error), deleteCreds func() (bool, error)) *cli.Command {
	return &cli.Command{
		Name:  key,
		Usage: "manage " + display + " integration",
		Commands: []*cli.Command{
			{
				Name:  "reset",
				Usage: "clear stored " + display + " credentials and force re-authentication",
				Action: func(ctx context.Context, c *cli.Command) error {
					path, err := credsPath()
					if err != nil {
						return fmt.Errorf("locate credentials: %w", err)
					}
					removed, err := deleteCreds()
					if err != nil {
						return fmt.Errorf("remove credentials: %w", err)
					}
					if !removed {
						fmt.Printf("No stored %s credentials to remove.\n", display)
						return nil
					}
					fmt.Printf("Removed %s\n", path)
					fmt.Printf("Restart cliamp and select %s to sign in again.\n", display)
					return nil
				},
			},
		},
	}
}

func playlistCommand() *cli.Command {
	return &cli.Command{
		Name:  "playlist",
		Usage: "manage local playlists",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "list playlists with track counts",
				Action: func(ctx context.Context, c *cli.Command) error {
					return cmd.PlaylistList()
				},
			},
			{
				Name:      "create",
				Usage:     "create a new playlist, optionally from files/directories or directory sources",
				ArgsUsage: "\"Name\" [file|dir ...]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "ssh", Usage: "SSH host for remote directory walking"},
					&cli.StringSliceFlag{Name: "dir", Usage: "reference a directory as a [[dir]] source (repeatable)"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("playlist name is required")
					}
					name := c.Args().First()
					paths := c.Args().Slice()[1:]
					return cmd.PlaylistCreate(name, paths, c.String("ssh"), c.StringSlice("dir"))
				},
			},
			{
				Name:      "rename",
				Usage:     "rename a playlist",
				ArgsUsage: "\"Old\" \"New\"",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() != 2 {
						return fmt.Errorf("usage: cliamp playlist rename \"Old\" \"New\"")
					}
					args := c.Args().Slice()
					return cmd.PlaylistRename(args[0], args[1])
				},
			},
			{
				Name:      "add",
				Usage:     "append tracks or directory sources to an existing playlist",
				ArgsUsage: "\"Name\" [file|dir ...]",
				Flags: []cli.Flag{
					&cli.StringSliceFlag{Name: "dir", Usage: "add a directory as a [[dir]] source (repeatable)"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist add \"Name\" file1 [file2 ...] [--dir dir]")
					}
					name := c.Args().First()
					paths := c.Args().Slice()[1:]
					return cmd.PlaylistAdd(name, paths, c.StringSlice("dir"))
				},
			},
			{
				Name:      "dirs",
				Usage:     "list directory sources referenced by a playlist",
				ArgsUsage: "\"Name\"",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist dirs \"Name\"")
					}
					return cmd.PlaylistDirs(c.Args().First())
				},
			},
			{
				Name:      "show",
				Usage:     "display tracks in a playlist",
				ArgsUsage: "\"Name\"",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "machine-readable JSON output"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist show \"Name\" [--json]")
					}
					return cmd.PlaylistShow(c.Args().First(), c.Bool("json"))
				},
			},
			{
				Name:      "remove",
				Usage:     "remove a track by index",
				ArgsUsage: "\"Name\"",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "index", Usage: "track index (1-based)", Required: true},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist remove \"Name\" --index N")
					}
					return cmd.PlaylistRemove(c.Args().First(), int(c.Int("index")))
				},
			},
			{
				Name:      "delete",
				Usage:     "delete an entire playlist",
				ArgsUsage: "\"Name\"",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist delete \"Name\"")
					}
					return cmd.PlaylistDelete(c.Args().First())
				},
			},
			{
				Name:      "dedupe",
				Usage:     "remove duplicate tracks by exact path",
				ArgsUsage: "\"Name\"",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist dedupe \"Name\"")
					}
					return cmd.PlaylistDedupe(c.Args().First())
				},
			},
			{
				Name:      "sort",
				Usage:     "sort a playlist in place",
				ArgsUsage: "\"Name\"",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "by", Usage: "sort key: track, title, artist, album, artist+album, path", Value: "title"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist sort \"Name\" --by album")
					}
					return cmd.PlaylistSort(c.Args().First(), c.String("by"))
				},
			},
			{
				Name:      "doctor",
				Usage:     "report missing local files in playlists",
				ArgsUsage: "[Name]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "fix", Usage: "prune missing local files"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					name := ""
					if c.Args().Len() > 0 {
						name = c.Args().First()
					}
					return cmd.PlaylistDoctor(name, c.Bool("fix"))
				},
			},
			{
				Name:      "export",
				Usage:     "export a playlist as M3U or PLS",
				ArgsUsage: "\"Name\"",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "format", Usage: "format: m3u or pls", Value: "m3u"},
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "output file (default stdout)"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist export \"Name\" [--format m3u|pls] [-o file]")
					}
					return cmd.PlaylistExport(c.Args().First(), c.String("format"), c.String("output"))
				},
			},
			{
				Name:      "import",
				Usage:     "import a local M3U or PLS file",
				ArgsUsage: "file.m3u",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Usage: "playlist name (default: file basename)"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist import file.m3u [--name Name]")
					}
					return cmd.PlaylistImport(c.Args().First(), c.String("name"))
				},
			},
			{
				Name:      "favorite",
				Aliases:   []string{"bookmark"},
				Usage:     "toggle the favorite of a track by index",
				ArgsUsage: "\"Name\"",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "index", Usage: "track index (1-based)", Required: true},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist favorite \"Name\" --index N")
					}
					return cmd.PlaylistFavorite(c.Args().First(), int(c.Int("index")))
				},
			},
			{
				Name:    "favorites",
				Aliases: []string{"bookmarks"},
				Usage:   "list all favorite tracks",
				Action: func(ctx context.Context, c *cli.Command) error {
					return cmd.PlaylistFavorites()
				},
			},
			{
				Name:      "enrich",
				Usage:     "probe duration and album metadata for SSH tracks",
				ArgsUsage: "\"Name\"",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "source", Usage: "source: metadata, path", Value: "path"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist enrich \"Name\" --source metadata")
					}
					return cmd.PlaylistEnrich(c.Args().First(), c.String("source"))
				},
			},
		},
	}
}

func historyCommand() *cli.Command {
	return &cli.Command{
		Name:  "history",
		Usage: "show recently played tracks",
		Description: "Lists recently played tracks. A track is recorded when it starts to play.\n" +
			"Browse the same data inside the TUI under Local Playlists →\n" +
			"\"Recently Played\".",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "limit", Usage: "max entries to show (0 = all)", Value: 50},
			&cli.BoolFlag{Name: "json", Usage: "machine-readable JSON output"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return cmd.HistoryShow(int(c.Int("limit")), c.Bool("json"))
		},
		Commands: []*cli.Command{
			{
				Name:  "clear",
				Usage: "delete the history file",
				Action: func(ctx context.Context, c *cli.Command) error {
					return cmd.HistoryClear()
				},
			},
		},
	}
}

// ipcSimpleCommand creates a fire-and-forget IPC command (play, pause, etc.).
func ipcSimpleCommand(name, usage string) *cli.Command {
	return &cli.Command{
		Name:  name,
		Usage: usage,
		Action: func(ctx context.Context, c *cli.Command) error {
			_, err := ipcSend(name, ipc.Request{})
			return err
		},
	}
}

func statusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "show current playback state",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "machine-readable JSON output"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			snapshot, err := ipcState()
			if err != nil {
				return err
			}
			resp := stateResult(snapshot)
			if c.Bool("json") {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(newStatusJSON(resp))
			}
			state := resp.State
			if state == "" {
				state = "stopped"
			}
			fmt.Printf("State: %s\n", state)
			if resp.Track != nil {
				fmt.Printf("Track: %s\n", resp.Track.Title)
				if resp.Track.Artist != "" {
					fmt.Printf("Artist: %s\n", resp.Track.Artist)
				}
			}
			if resp.Duration > 0 {
				fmt.Printf("Position: %.0f / %.0f sec\n", resp.Position, resp.Duration)
			}
			fmt.Printf("Volume: %.0f dB\n", resp.Volume)
			if resp.Shuffle != nil {
				if *resp.Shuffle {
					fmt.Println("Shuffle: on")
				} else {
					fmt.Println("Shuffle: off")
				}
			}
			if resp.Repeat != "" {
				fmt.Printf("Repeat: %s\n", resp.Repeat)
			}
			if resp.Mono != nil {
				if *resp.Mono {
					fmt.Println("Mono: on")
				} else {
					fmt.Println("Mono: off")
				}
			}
			if resp.Speed > 0 {
				fmt.Printf("Speed: %.2fx\n", resp.Speed)
			}
			if resp.EQPreset != "" {
				fmt.Printf("EQ: %s\n", resp.EQPreset)
			}
			return nil
		},
	}
}

func volumeCommand() *cli.Command {
	return &cli.Command{
		Name:      "volume",
		Usage:     "set the volume to an absolute level in dB",
		ArgsUsage: "<dB>",
		Description: "Sets the volume to <dB>. A sign does not make the value relative:\n" +
			"cliamp volume -3 sets the volume to -3 dB. cliamp clamps the value to\n" +
			"the range volume_min to +6 dB. To step the volume, run\n" +
			"cliamp remote call volume.adjust --params '{\"value\":3}'.",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp volume <dB>")
			}
			db, err := strconv.ParseFloat(c.Args().First(), 64)
			if err != nil {
				return fmt.Errorf("invalid volume value %q", c.Args().First())
			}
			_, err = ipcSend("volume", ipc.Request{Value: db})
			return err
		},
	}
}

func seekCommand() *cli.Command {
	return &cli.Command{
		Name:      "seek",
		Usage:     "seek by a relative offset in seconds",
		ArgsUsage: "<seconds>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp seek <seconds>")
			}
			secs, err := strconv.ParseFloat(c.Args().First(), 64)
			if err != nil {
				return fmt.Errorf("invalid seek value %q", c.Args().First())
			}
			_, err = ipcSend("seek", ipc.Request{Value: secs})
			return err
		},
	}
}

func loadCommand() *cli.Command {
	return &cli.Command{
		Name:      "load",
		Usage:     "load a playlist into the player",
		ArgsUsage: "\"Playlist Name\"",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp load \"Playlist Name\"")
			}
			_, err := ipcSendWithin("load", ipc.Request{Playlist: c.Args().First()}, ipcLoadWait)
			return err
		},
	}
}

func queueCommand() *cli.Command {
	return &cli.Command{
		Name:      "queue",
		Usage:     "queue a track for playback",
		ArgsUsage: "</path/to/file.mp3>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp queue /path/to/file.mp3")
			}
			path, err := queuePath(c.Args().First())
			if err != nil {
				return err
			}
			_, err = ipcSend("queue", ipc.Request{Path: path})
			return err
		},
	}
}

// queuePath returns the path that cliamp queue sends for arg. A local file
// gets an absolute path, because the running cliamp has its own working
// directory. A URL or another URI, such as spotify:track:..., goes as it is.
// A missing file or a directory is an error.
func queuePath(arg string) (string, error) {
	info, err := os.Stat(arg)
	switch {
	case err == nil && info.IsDir():
		return "", fmt.Errorf("queue: %s is a directory", arg)
	case err == nil:
		return filepath.Abs(arg)
	case playlist.IsURL(arg) || resolve.URIScheme(arg) != "":
		return arg, nil
	}
	return "", fmt.Errorf("queue: %w", err)
}

func themeCommand() *cli.Command {
	return &cli.Command{
		Name:      "theme",
		Usage:     "set or list UI themes",
		ArgsUsage: "<name|list>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp theme <name|list>")
			}
			if strings.EqualFold(c.Args().First(), "list") {
				// The list is read here, so it works when cliamp is not
				// running. It starts with the terminal colors, as the IPC
				// theme list does. No log file is open yet, so the skipped
				// theme files go to stderr.
				themes, errs := theme.LoadAllWithErrors()
				for _, err := range errs {
					fmt.Fprintln(os.Stderr, err)
				}
				fmt.Printf("  %s\n", theme.DefaultName)
				for _, t := range themes {
					fmt.Printf("  %s\n", t.Name)
				}
				return nil
			}
			_, err := ipcSend("theme", ipc.Request{Name: c.Args().First()})
			if err != nil {
				return err
			}
			fmt.Printf("Theme: %s\n", c.Args().First())
			return nil
		},
	}
}

func visStreamCommand() *cli.Command {
	return &cli.Command{
		Name:  "visstream",
		Usage: "stream visualizer bands as NDJSON (one frame per line)",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "fps", Value: 30, Usage: "frames per second (1-60)"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			fps := c.Int("fps")
			if fps < 1 {
				fps = 1
			}
			if fps > 60 {
				fps = 60
			}
			return userIPCError(ipc.StreamBands(ctx, ipc.DefaultSocketPath(), time.Second/time.Duration(fps), os.Stdout))
		},
	}
}

// visModes returns the modes that cliamp vis list prints and the row of the
// active mode, or -1. A running cliamp lists its Lua visualizers too and
// gives the active row, because a Lua mode can have the name of a built-in
// mode. A cliamp that gives no row marks the first row with the active
// name. With no running cliamp, or in headless mode, it lists the built-in
// modes.
func visModes() (names []string, active int, running bool) {
	names = ui.VisModeNames()
	snapshot, err := ipcState()
	if err != nil {
		return names, -1, false
	}
	if resp, err := ipcSend("vis", ipc.Request{Name: "list"}); err == nil && len(resp.Items) > 0 {
		names = resp.Items
		if resp.Visualizer != "" && resp.Index >= 0 && resp.Index < len(names) {
			return names, resp.Index, true
		}
	}
	active = slices.IndexFunc(names, func(name string) bool { return strings.EqualFold(name, snapshot.Visualizer) })
	return names, active, true
}

func visCommand() *cli.Command {
	return &cli.Command{
		Name:      "vis",
		Usage:     "set or list visualizer modes",
		ArgsUsage: "<name|next|list>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp vis <name|next|list>")
			}
			if strings.EqualFold(c.Args().First(), "list") {
				names, active, running := visModes()
				if !running {
					fmt.Fprintln(os.Stderr, "(cliamp not running — active marker unavailable)")
				}
				for i, name := range names {
					marker := "  "
					if i == active {
						marker = "* "
					}
					fmt.Printf("%s%s\n", marker, name)
				}
				return nil
			}
			resp, err := ipcSend("vis", ipc.Request{Name: c.Args().First()})
			if err != nil {
				return err
			}
			fmt.Printf("Visualizer: %s\n", resp.Visualizer)
			return nil
		},
	}
}

func shuffleCommand() *cli.Command {
	return switchCommand("shuffle", "toggle or set shuffle mode", "Shuffle", func(r ipc.Response) *bool { return r.Shuffle })
}

func repeatCommand() *cli.Command {
	return &cli.Command{
		Name:      "repeat",
		Usage:     "set or cycle repeat mode",
		ArgsUsage: "[off|all|one|cycle]",
		Action: func(ctx context.Context, c *cli.Command) error {
			name := "cycle"
			if c.Args().Len() > 0 {
				name = strings.ToLower(c.Args().First())
			}
			resp, err := ipcSend("repeat", ipc.Request{Name: name})
			if err != nil {
				return err
			}
			fmt.Printf("Repeat: %s\n", resp.Repeat)
			return nil
		},
	}
}

func monoCommand() *cli.Command {
	return switchCommand("mono", "toggle or set mono output", "Mono", func(r ipc.Response) *bool { return r.Mono })
}

// switchCommand builds a command that toggles an on and off setting, or sets
// it to on or off. It prints the new state from the result.
func switchCommand(name, usage, label string, state func(ipc.Response) *bool) *cli.Command {
	return &cli.Command{
		Name:      name,
		Usage:     usage,
		ArgsUsage: "[on|off|toggle]",
		Action: func(ctx context.Context, c *cli.Command) error {
			value := "toggle"
			if c.Args().Len() > 0 {
				value = strings.ToLower(c.Args().First())
			}
			resp, err := ipcSend(name, ipc.Request{Name: value})
			if err != nil {
				return err
			}
			if on := state(resp); on != nil && *on {
				fmt.Printf("%s: on\n", label)
			} else {
				fmt.Printf("%s: off\n", label)
			}
			return nil
		},
	}
}

func speedCommand() *cli.Command {
	return &cli.Command{
		Name:      "speed",
		Usage:     "set playback speed (0.25-2.0)",
		ArgsUsage: "<ratio>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp speed <ratio>  (e.g. 1.0, 1.5, 0.75)")
			}
			ratio, err := strconv.ParseFloat(c.Args().First(), 64)
			if err != nil {
				return fmt.Errorf("invalid speed %q", c.Args().First())
			}
			resp, err := ipcSend("speed", ipc.Request{Value: ratio})
			if err != nil {
				return err
			}
			fmt.Printf("Speed: %.2fx\n", resp.Speed)
			return nil
		},
	}
}

func eqCommand() *cli.Command {
	return &cli.Command{
		Name:      "eq",
		Usage:     "set EQ preset or individual band",
		ArgsUsage: "<preset|band> [dB]",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "band", Usage: "EQ band index (0-9)", Value: -1, HideDefault: true},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			band := int(c.Int("band"))
			if band >= 0 {
				// Set a specific band.
				if c.Args().Len() == 0 {
					return fmt.Errorf("usage: cliamp eq --band N <dB>")
				}
				db, err := strconv.ParseFloat(c.Args().First(), 64)
				if err != nil {
					return fmt.Errorf("invalid dB value %q", c.Args().First())
				}
				resp, err := ipcSend("eq", ipc.Request{Band: band, Value: db})
				if err != nil {
					return err
				}
				fmt.Printf("EQ band %d: %.1f dB (preset: %s)\n", band, db, resp.EQPreset)
				return nil
			}
			// Apply a preset by name.
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp eq <preset>  (e.g. Flat, Rock, Pop, Jazz)")
			}
			resp, err := ipcSend("eq", ipc.Request{Name: c.Args().First()})
			if err != nil {
				return err
			}
			fmt.Printf("EQ: %s\n", resp.EQPreset)
			return nil
		},
	}
}

func deviceCommand() *cli.Command {
	return &cli.Command{
		Name:      "device",
		Usage:     "switch audio output device",
		ArgsUsage: "<name|list>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp device <name|list>")
			}
			if strings.EqualFold(c.Args().First(), "list") {
				resp, err := ipcSend("device", ipc.Request{Name: "list"})
				if err != nil {
					return err
				}
				fmt.Println(resp.Device)
				return nil
			}
			resp, err := ipcSend("device", ipc.Request{Name: c.Args().First()})
			if err != nil {
				return err
			}
			fmt.Printf("Audio device: %s\n", resp.Device)
			return nil
		},
	}
}

func remoteCommand() *cli.Command {
	return &cli.Command{
		Name:  "remote",
		Usage: "use the version 2 IPC API",
		Commands: []*cli.Command{
			{
				Name:  "state",
				Usage: "print the complete runtime snapshot as JSON",
				Action: func(ctx context.Context, c *cli.Command) error {
					response, err := sendV2(ipc.V2Request{Method: "state.get"})
					if err != nil {
						return err
					}
					return printV2Response(response)
				},
			},
			{
				Name:  "capabilities",
				Usage: "print available v2 operations as JSON",
				Action: func(ctx context.Context, c *cli.Command) error {
					response, err := sendV2(ipc.V2Request{Method: "capabilities"})
					if err != nil {
						return err
					}
					return printV2Response(response)
				},
			},
			{
				Name:      "call",
				Usage:     "submit a v2 operation",
				ArgsUsage: "<operation>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "params", Usage: "JSON object passed as operation parameters", Value: "{}"},
					&cli.BoolFlag{Name: "wait", Usage: "wait for a submitted job to finish"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp remote call <operation> [--params '{}']")
					}
					params := json.RawMessage(c.String("params"))
					if !json.Valid(params) {
						return fmt.Errorf("--params must be a JSON value")
					}
					response, err := sendV2(ipc.V2Request{
						Method:    "operation.submit",
						Operation: c.Args().First(),
						Params:    params,
					})
					if err != nil {
						return err
					}
					if err := v2ResponseError(response); err != nil {
						return err
					}
					if c.Bool("wait") && response.Job != nil {
						response, err = waitForV2Job(ctx, response.Job.ID)
						if err != nil {
							return err
						}
					}
					return printV2Response(response)
				},
			},
			{
				Name:      "job",
				Usage:     "print a v2 job",
				ArgsUsage: "<job-id>",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp remote job <job-id>")
					}
					response, err := sendV2(ipc.V2Request{Method: "job.get", JobID: c.Args().First()})
					if err != nil {
						return err
					}
					return printV2Response(response)
				},
			},
			{
				Name:      "cancel",
				Usage:     "cancel a v2 job",
				ArgsUsage: "<job-id>",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp remote cancel <job-id>")
					}
					response, err := sendV2(ipc.V2Request{Method: "job.cancel", JobID: c.Args().First()})
					if err != nil {
						return err
					}
					return printV2Response(response)
				},
			},
			{
				Name:      "events",
				Usage:     "stream v2 runtime events as NDJSON",
				ArgsUsage: "<topic> [topic...]",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp remote events runtime.state [runtime.job]")
					}
					stream, err := ipc.SubscribeV2(ipc.DefaultSocketPath(), json.RawMessage(cliRequestID), c.Args().Slice())
					if err != nil {
						return userIPCError(err)
					}
					defer stream.Close()
					encoder := json.NewEncoder(os.Stdout)
					for {
						select {
						case <-ctx.Done():
							return nil
						default:
						}
						event, err := stream.Next()
						if err != nil {
							return err
						}
						if err := encoder.Encode(event); err != nil {
							return err
						}
					}
				},
			},
		},
	}
}
