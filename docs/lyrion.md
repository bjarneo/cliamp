# Lyrion Music Server Integration

Use Cliamp to connect to a [Lyrion Music Server](https://lyrion.org/) (LMS, formerly Logitech Media Server) and stream music from your library. LMS is a self-hosted server for the Squeezebox ecosystem.

> **Quick start:** Run `cliamp setup`. Enter the server URL and optional credentials. The TUI validates the connection and writes the `[lyrion]` block. Manual steps follow.

## Setup

Add a `[lyrion]` block to `~/.config/cliamp/config.toml`:

```toml
[lyrion]
url = "http://nas.local:9000"
```

Port 9000 is the LMS default. It is the web interface port, not the 9090 CLI port.

If the server has password protection enabled (Settings → Advanced → Security), add credentials:

```toml
[lyrion]
url      = "http://nas.local:9000"
user     = "your-username"
password = "your-password"
```

To keep the password out of the file, use `$VAR` interpolation. The configuration supports it for every string value:

```toml
[lyrion]
url      = "http://nas.local:9000"
user     = "your-username"
password = "${LYRION_PASSWORD}"
```

### Environment variables

If there is no `[lyrion]` block, Cliamp uses environment variables. Only the URL is required:

```sh
export LYRION_URL="http://nas.local:9000"
export LYRION_USER="your-username"   # only if password protection is on
export LYRION_PASS="your-password"
```

A configured `[lyrion]` block takes precedence over these variables.

## How It Works

Cliamp queries the library through the LMS JSON-RPC endpoint (`POST /jsonrpc.js`). It plays tracks from the HTTP file endpoint (`/music/<track_id>/download`). Playback uses Cliamp's audio engine.

Lyrion has no dedicated shortcut key. Press `Esc` to open the provider list and select it. Or set it as the startup provider with `cliamp --provider lyrion` or `provider = "lyrion"` in `config.toml`.

Use the arrow keys to browse playlists. Press Enter to load one.

**Press `N` to open the artist/album browser.** The provider pane lists only server *saved playlists*. The browser lists music files by artist and album.

Press `Ctrl+F` to search the library through the server. Use it from the playlist view or browser.

## Limitations

Cliamp is **not** a Squeezebox player. It reads the library from LMS and plays files itself. It does not register as an LMS endpoint. This has three effects:

- **No play history in LMS.** LMS records play counts and "last played" for a player that plays a track. Cliamp does not drive an LMS player, so listening with Cliamp leaves no record on the server. This is expected behavior.
- **No synchronization or multi-room.** Cliamp cannot join an LMS sync group. Other LMS controllers cannot see or control Cliamp. For this feature, run a Squeezebox player such as [squeezelite](https://github.com/ralph-irving/squeezelite).
- **No server-side transcoding.** Cliamp downloads the original file and decodes it locally. A format that Cliamp cannot decode cannot play, even if LMS could transcode it. Install `ffmpeg` to decode more formats.

### Tracks added by server plugins

LMS plugins that add streaming services, such as Spotty for Spotify, add their tracks to the library with local files. LMS serves only *file-backed* tracks through the endpoint that Cliamp uses. LMS accepts a download request for a plugin track but might not send audio. Playback then hangs.

**Cliamp hides these by default.** It also hides a saved playlist that a plugin imported. Such a playlist contains only plugin tracks and would otherwise open empty. This avoids unusable entries on a server with a large Spotty library.

To show them as unplayable entries that cliamp skips during playback:

```toml
[lyrion]
url             = "http://nas.local:9000"
show_unplayable = true
```

Set `LYRION_SHOW_UNPLAYABLE=true` for the same behavior with environment configuration.

An *album* that contains only plugin tracks still appears in the browser and opens empty. Cliamp filters playlists because the server reports their origin in the same response. Classifying albums would need one extra request per album.

To play these services, use the Cliamp Spotify, Qobuz, or Tidal provider. Each connects directly to its service protocol.

## Security

With credentials, Cliamp uses HTTP Basic authentication. The LMS web interface uses the same scheme. Plain HTTP sends reusable credentials with every request. Use it only on a trusted LAN. If the server is available outside the network, put it behind HTTPS and use an `https://` URL.

## Troubleshooting

**The provider does not appear.** Cliamp creates the provider only when a URL is configured. Check that `[lyrion]` sets `url`, or that the shell that starts Cliamp exports `LYRION_URL`.

**"authentication failed".** The server rejected the credentials. Check the username and password in the LMS web interface. If the server has no password protection, remove `user` and `password`. Do not leave them blank.

**Connection refused or timed out.** Check the port. LMS serves its web UI and this API on 9000 by default. Open `http://your-server:9000` in a browser to test it.

**Tracks appear but will not play.** Cliamp might not decode the file format. Install `ffmpeg` and try again.

**A playlist or artist is missing.** If it comes from a server plugin instead of local files, cliamp hides it by default. Set `show_unplayable = true` to show it. Showing it does not make it playable.
