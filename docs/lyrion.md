# Lyrion Music Server Integration

Cliamp can connect to a [Lyrion Music Server](https://lyrion.org/) (LMS, formerly Logitech Media Server) and stream music directly from your library. LMS is the long-running self-hosted server behind the Squeezebox ecosystem.

> **Quick start:** run `cliamp setup` for a guided TUI that prompts for the server URL and optional credentials, validates the connection, and writes the `[lyrion]` block for you. Manual setup steps are below.

## Setup

Add a `[lyrion]` block to `~/.config/cliamp/config.toml`:

```toml
[lyrion]
url = "http://nas.local:9000"
```

Port 9000 is the LMS default — the same port as its web interface, not the 9090 CLI port.

If your server has password protection enabled (Settings → Advanced → Security), add credentials:

```toml
[lyrion]
url      = "http://nas.local:9000"
user     = "your-username"
password = "your-password"
```

To keep the password out of the file, use the shared `$VAR` interpolation supported by every string value in the config:

```toml
[lyrion]
url      = "http://nas.local:9000"
user     = "your-username"
password = "${LYRION_PASSWORD}"
```

### Environment variables

If no `[lyrion]` block is present, Cliamp falls back to environment variables. Only the URL is required:

```sh
export LYRION_URL="http://nas.local:9000"
export LYRION_USER="your-username"   # only if password protection is on
export LYRION_PASS="your-password"
```

A configured `[lyrion]` block always takes precedence over these.

## How It Works

Cliamp queries your library over the LMS JSON-RPC endpoint (`POST /jsonrpc.js`) and plays tracks from its HTTP file endpoint (`/music/<track_id>/download`). Playback runs in Cliamp's own audio engine, exactly as it does for every other provider.

Browse playlists with the arrow keys and press Enter to load one.

**Press `N` to open the artist/album browser.** This matters more than it does for other providers: the provider pane lists only your server's *saved playlists*, and on many LMS installs those are dominated by playlists imported by streaming-service plugins — which are not playable (see below). Your actual music files are organised by artist and album, and the browser is how you reach them.

## Limitations

Cliamp is **not** a Squeezebox player. It reads your library from LMS and plays the files itself, rather than registering as an LMS endpoint. That has three consequences worth knowing about:

- **No play history in LMS.** LMS records play counts and "last played" against a player playing a track. Since Cliamp never drives an LMS player, your listening in Cliamp leaves no trace on the server. This is expected behaviour, not a bug.
- **No synchronisation or multi-room.** Cliamp cannot join an LMS sync group, and other LMS controllers cannot see or control Cliamp. If you want that, run a real Squeezebox player such as [squeezelite](https://github.com/ralph-irving/squeezelite).
- **No server-side transcoding.** Cliamp downloads the original file and decodes it locally. Formats Cliamp cannot decode are unplayable even though LMS could have transcoded them. Installing `ffmpeg` widens the range of decodable formats considerably.

### Tracks added by server plugins

LMS plugins that pull in streaming services (Spotty for Spotify, and similar) add their tracks to your library alongside your own files. Those tracks **appear** in Cliamp's browser but are marked unplayable and skipped during playback, because LMS only serves file-backed tracks over the endpoint Cliamp streams from — a download request for a plugin track is accepted but may never deliver any audio, leaving playback to hang.

They are shown rather than hidden so your library does not look mysteriously incomplete. To play those services, use Cliamp's own Spotify, Qobuz, or Tidal providers, which speak each service's protocol directly.

## Security

When credentials are configured, Cliamp authenticates with HTTP Basic authentication — the same scheme the LMS web interface uses. Over plain HTTP that sends your credentials in a reusable form with every request, which is fine on a trusted LAN. If your server is reachable from outside your network, put it behind HTTPS and use an `https://` URL.

## Troubleshooting

**The provider does not appear.** Cliamp only constructs the provider when a URL is configured. Check that your `[lyrion]` block sets `url`, or that `LYRION_URL` is exported in the shell you launch Cliamp from.

**"authentication failed".** The server rejected your credentials. Confirm the username and password against the LMS web interface. If your server has no password protection, remove `user` and `password` entirely rather than leaving them blank-but-present.

**Connection refused or timed out.** Confirm the port — LMS serves both its web UI and this API on 9000 by default. Opening `http://your-server:9000` in a browser is the fastest check.

**Tracks appear but will not play.** The format is probably one that Cliamp cannot decode. Install `ffmpeg` and try again.
