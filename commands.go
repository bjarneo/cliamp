package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	cli "github.com/urfave/cli/v3"

	"cliamp/cmd"
	"cliamp/config"
	"cliamp/ipc"
	"cliamp/player"
	"cliamp/pluginmgr"
	"cliamp/theme"
	"cliamp/ui"
	"cliamp/upgrade"
)

func buildApp() *cli.Command {
	rootFlags := []cli.Flag{
		&cli.Float64Flag{Name: "vol", Usage: "startup volume in dB [-30, +6]"},
		&cli.BoolFlag{Name: "shuffle", Usage: "shuffle playback"},
		&cli.StringFlag{Name: "repeat", Usage: "repeat mode: off, all, one"},
		&cli.BoolFlag{Name: "mono", Usage: "mono output"},
		&cli.BoolFlag{Name: "no-mono", Usage: "disable mono output"},
		&cli.BoolFlag{Name: "auto-play", Usage: "start playback immediately"},
		&cli.BoolFlag{Name: "compact", Usage: "compact mode (80 columns)"},
		&cli.StringFlag{Name: "provider", Usage: "default provider: radio, navidrome, plex, jellyfin, spotify, yt, youtube, ytmusic"},
		&cli.StringFlag{Name: "start-theme", Usage: "UI theme name"},
		&cli.StringFlag{Name: "visualizer", Usage: "visualizer mode"},
		&cli.StringFlag{Name: "eq-preset", Usage: "EQ preset name"},
		&cli.IntFlag{Name: "sample-rate", Usage: "output sample rate in Hz (0=auto)", HideDefault: true},
		&cli.IntFlag{Name: "buffer-ms", Usage: "speaker buffer in milliseconds (50-500)", HideDefault: true},
		&cli.IntFlag{Name: "resample-quality", Usage: "resample quality factor (1-4)", HideDefault: true},
		&cli.IntFlag{Name: "bit-depth", Usage: "PCM bit depth: 16 or 32", HideDefault: true},
		&cli.StringFlag{Name: "audio-device", Usage: "audio output device (use 'list' to show)"},
	}

	return &cli.Command{
		Name:    "cliamp",
		Usage:   "retro terminal music player",
		Version: version,
		Flags:   rootFlags,
		Action: func(ctx context.Context, c *cli.Command) error {
			if strings.EqualFold(c.String("audio-device"), "list") {
				return listAudioDevices()
			}
			ov, err := overridesFromFlags(c)
			if err != nil {
				return err
			}
			return run(ov, c.Args().Slice())
		},
		Commands: []*cli.Command{
			upgradeCommand(),
			pluginsCommand(),
			playlistCommand(),
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
		v := true
		ov.Mono = &v
	}
	if c.IsSet("no-mono") {
		v := false
		ov.Mono = &v
	}
	if c.IsSet("auto-play") {
		v := true
		ov.Play = &v
	}
	if c.IsSet("compact") {
		v := true
		ov.Compact = &v
	}
	if c.IsSet("provider") {
		v := strings.ToLower(c.String("provider"))
		switch v {
		case "radio", "navidrome", "spotify", "plex", "jellyfin", "yt", "youtube", "ytmusic":
			ov.Provider = &v
		default:
			return ov, fmt.Errorf("--provider must be radio, navidrome, spotify, plex, jellyfin, yt, youtube, or ytmusic (got %q)", v)
		}
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
	return ov, nil
}

func upgradeCommand() *cli.Command {
	return &cli.Command{
		Name:  "upgrade",
		Usage: "upgrade cliamp to the latest release",
		Action: func(ctx context.Context, c *cli.Command) error {
			return upgrade.Run(version)
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
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp plugins install <source>")
					}
					return pluginmgr.Install(c.Args().First())
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
				Usage:     "create a new playlist from files/directories",
				ArgsUsage: "\"Name\" <file|dir> [...]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "ssh", Usage: "SSH host for remote directory walking"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("playlist name is required")
					}
					if c.Args().Len() < 2 && c.String("ssh") == "" {
						return fmt.Errorf("at least one file or directory is required (or use --ssh)")
					}
					name := c.Args().First()
					paths := c.Args().Slice()[1:]
					return cmd.PlaylistCreate(name, paths, c.String("ssh"))
				},
			},
			{
				Name:      "add",
				Usage:     "append tracks to an existing playlist",
				ArgsUsage: "\"Name\" <file|dir> [...]",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() < 2 {
						return fmt.Errorf("usage: cliamp playlist add \"Name\" file1 [file2 ...]")
					}
					return cmd.PlaylistAdd(c.Args().First(), c.Args().Slice()[1:])
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
				Name:      "favorite",
				Usage:     "toggle favorite on a track by index",
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
				Name:  "favorites",
				Usage: "list all favorited tracks across playlists",
				Action: func(ctx context.Context, c *cli.Command) error {
					return cmd.PlaylistFavorites()
				},
			},
			{
				Name:      "enrich",
				Usage:     "probe duration and album metadata for SSH tracks",
				ArgsUsage: "\"Name\"",
				Action: func(ctx context.Context, c *cli.Command) error {
					if c.Args().Len() == 0 {
						return fmt.Errorf("usage: cliamp playlist enrich \"Name\"")
					}
					return cmd.PlaylistEnrich(c.Args().First())
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
			_, err := ipcSend(ipc.Request{Cmd: name})
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
			resp, err := ipcSend(ipc.Request{Cmd: "status"})
			if err != nil {
				return err
			}
			if c.Bool("json") {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(resp)
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
			return nil
		},
	}
}

func volumeCommand() *cli.Command {
	return &cli.Command{
		Name:      "volume",
		Usage:     "adjust volume in dB",
		ArgsUsage: "<dB>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp volume <dB>")
			}
			db, err := strconv.ParseFloat(c.Args().First(), 64)
			if err != nil {
				return fmt.Errorf("invalid volume value %q", c.Args().First())
			}
			_, err = ipcSend(ipc.Request{Cmd: "volume", Value: db})
			return err
		},
	}
}

func seekCommand() *cli.Command {
	return &cli.Command{
		Name:      "seek",
		Usage:     "seek to position in seconds",
		ArgsUsage: "<seconds>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp seek <seconds>")
			}
			secs, err := strconv.ParseFloat(c.Args().First(), 64)
			if err != nil {
				return fmt.Errorf("invalid seek value %q", c.Args().First())
			}
			_, err = ipcSend(ipc.Request{Cmd: "seek", Value: secs})
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
			_, err := ipcSend(ipc.Request{Cmd: "load", Playlist: c.Args().First()})
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
			_, err := ipcSend(ipc.Request{Cmd: "queue", Path: c.Args().First()})
			return err
		},
	}
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
				themes := theme.LoadAll()
				for _, t := range themes {
					fmt.Printf("  %s\n", t.Name)
				}
				return nil
			}
			_, err := ipcSend(ipc.Request{Cmd: "theme", Name: c.Args().First()})
			if err != nil {
				return err
			}
			fmt.Printf("Theme: %s\n", c.Args().First())
			return nil
		},
	}
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
				var active string
				sockPath := ipc.DefaultSocketPath()
				if resp, err := ipc.Send(sockPath, ipc.Request{Cmd: "status"}); err == nil {
					active = resp.Visualizer
				} else {
					fmt.Fprintln(os.Stderr, "(cliamp not running — active marker unavailable)")
				}
				for _, name := range ui.VisModeNames() {
					marker := "  "
					if strings.EqualFold(name, active) {
						marker = "* "
					}
					fmt.Printf("%s%s\n", marker, name)
				}
				return nil
			}
			resp, err := ipcSend(ipc.Request{Cmd: "vis", Name: c.Args().First()})
			if err != nil {
				return err
			}
			fmt.Printf("Visualizer: %s\n", resp.Visualizer)
			return nil
		},
	}
}
