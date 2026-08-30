package main

// open.go implements the cliamp:// protocol handler.
//
// `cliamp open <uri>` is what a desktop environment runs when someone clicks
// a cliamp:// link. It is the only entry point that acts on a string chosen
// by a web page, so it does two things carefully:
//
//   - Every URI goes through deeplink.Parse first, which validates the whole
//     grammar and returns a closed struct. Nothing from the URI is ever used
//     as an IPC operation name.
//   - dispatch maps the parsed action onto a fixed set of operations written
//     out longhand below. Adding a reachable operation requires editing this
//     file, which is the point.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/deeplink"
	"github.com/bjarneo/cliamp/ipc"
)

// deepLinkStartupTimeout bounds how long a cold start waits for its own IPC
// server to come up before giving up on the requested action. The player is
// already running by then, so exceeding it costs the action, not the session.
const deepLinkStartupTimeout = 15 * time.Second

func openCommand() *cli.Command {
	return &cli.Command{
		Name:      "open",
		Usage:     "handle a cliamp:// link",
		ArgsUsage: "<cliamp://...>",
		Description: "Plays or queues the target of a cliamp:// URI. Registered as the\n" +
			"system handler for the scheme by `cliamp protocol register`.\n\n" +
			"  cliamp open 'cliamp://play?url=https://example.com/stream.mp3'\n" +
			"  cliamp open 'cliamp://play?provider=navidrome&album=a1b2c3'\n" +
			"  cliamp open 'cliamp://queue?provider=ytmusic&q=aphex+twin'\n\n" +
			"When cliamp is already running the action is sent over IPC and the\n" +
			"command exits. Otherwise it starts the player in this terminal and\n" +
			"performs the action once it is up.",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() == 0 {
				return fmt.Errorf("usage: cliamp open 'cliamp://play?url=https://example.com/stream.mp3'")
			}
			return openDeepLink(ctx, c.Args().First())
		},
	}
}

// openDeepLink parses uri and performs it, starting the player when nothing
// is listening on the IPC socket.
func openDeepLink(ctx context.Context, uri string) error {
	action, err := deeplink.Parse(uri)
	if err != nil {
		return err
	}
	if ipcRunning() {
		return dispatchDeepLink(action)
	}
	return coldStartDeepLink(action)
}

// ipcRunning reports whether an instance is listening. It probes rather than
// inferring from a failed dispatch, so a genuine operation error is never
// mistaken for "not running" and answered by starting a second player.
func ipcRunning() bool {
	_, err := ipc.SendV2(ipc.DefaultSocketPath(), ipc.V2Request{
		ID:     json.RawMessage(`"cliamp"`),
		Method: "capabilities",
	})
	return !errors.Is(err, ipc.ErrNotRunning)
}

// coldStartDeepLink runs the player in this process and performs the action
// once its IPC server is accepting connections.
//
// Dispatching to our own socket rather than translating the action into
// startup arguments keeps one code path for every target. Provider albums and
// playlists have no command-line spelling at all, so the alternative would be
// a second, less capable implementation reachable only on a cold start.
func coldStartDeepLink(action deeplink.Action) error {
	// Failures go to applog rather than stderr: the TUI owns the terminal by
	// the time this runs, and writing to stderr would scribble over it.
	// UserError both records the failure and surfaces it in the footer.
	go func() {
		if !waitForIPC(deepLinkStartupTimeout) {
			applog.UserError("cliamp:// %s timed out waiting for the player to start", action.Verb)
			return
		}
		if err := dispatchDeepLink(action); err != nil {
			applog.UserError("cliamp:// %s failed: %v", action.Verb, err)
		}
	}()
	return run(config.Overrides{}, nil, false, false)
}

// waitForIPC polls until the socket accepts a request or timeout elapses.
func waitForIPC(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ipcRunning() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// dispatchDeepLink maps a validated action onto IPC operations. The operation
// names are literals here and never come from the URI.
func dispatchDeepLink(action deeplink.Action) error {
	play := action.Verb == deeplink.Play

	switch action.Target {
	case deeplink.TargetURL:
		// url.load routes through resolve, which admits only http and https
		// and drops non-http entries from remote playlists. That is the same
		// path `cliamp queue` uses, so a link cannot reach anything typing a
		// URL could not.
		_, err := ipcSend("url.load", ipc.Request{Path: action.URL, Play: play})
		return err

	case deeplink.TargetAlbum:
		if !play {
			return errors.New("queueing a provider album is not supported; use cliamp://play")
		}
		_, err := ipcSend("provider.load_album", ipc.Request{
			Provider: action.Provider,
			Album:    action.Album,
		})
		return err

	case deeplink.TargetPlaylist:
		if !play {
			return errors.New("queueing a provider playlist is not supported; use cliamp://play")
		}
		_, err := ipcSend("provider.load", ipc.Request{
			Provider: action.Provider,
			Playlist: action.Playlist,
		})
		return err

	case deeplink.TargetSearch:
		return dispatchDeepLinkSearch(action, play)
	}
	return fmt.Errorf("unsupported cliamp:// target")
}

// dispatchDeepLinkSearch plays or queues a provider's top match for the query.
//
// The track handed to track.play comes from the provider's own search
// response, not from the URI. Providers are services the user configured and
// authenticated against, so their results carry the same trust as browsing
// them in the TUI; only the query text originates with the link, and it is
// used solely as a search term.
func dispatchDeepLinkSearch(action deeplink.Action, play bool) error {
	response, err := ipcSendLong("provider.search", ipc.Request{
		Provider: action.Provider,
		Query:    action.Query,
		Limit:    1,
	}, 60*time.Second)
	if err != nil {
		return err
	}
	if len(response.Tracks) == 0 {
		return fmt.Errorf("%s has no match for %q", action.Provider, action.Query)
	}
	// track.play hands the path straight to playback, bypassing resolve, so
	// the allowlist is applied here instead. The radio directory is public and
	// its entries are filtered at ingest; this keeps the guarantee even for a
	// provider added later that forwards a URL it did not construct.
	if path := response.Tracks[0].Path; !deeplink.AllowsTrackPath(path) {
		return fmt.Errorf("%s returned a result a cliamp:// link may not play: %q", action.Provider, path)
	}

	operation := "track.queue"
	if play {
		operation = "track.play"
	}
	_, err = ipcSend(operation, ipc.Request{Track: &response.Tracks[0]})
	return err
}
