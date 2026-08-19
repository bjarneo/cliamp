# Plex Media Server

cliamp can stream music directly from your Plex Media Server, giving you access to your full Plex music library, including any library served by PlexAmp. Streaming uses the same Plex HTTP API that official Plex clients use; no extra software is required.

> **Quick start:** run `cliamp setup` for a guided TUI that prompts for the server URL and `X-Plex-Token`, pings the server to verify the token, and writes the `[plex]` block for you. Manual setup steps are below.

## Prerequisites

- Plex Media Server running and reachable on your network (or remotely)
- At least one music library configured in Plex
- Your `X-Plex-Token` (see below)

## Finding your X-Plex-Token

1. Open Plex Web in a browser and sign in
2. Browse to any item in your music library
3. Click the **···** menu → **Get Info** → **View XML**
4. In the URL of the XML page, copy the value of the `X-Plex-Token` query parameter

Alternatively, follow the [official Plex guide](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/).

## Configuration

Add a `[plex]` section to `~/.config/cliamp/config.toml`:

```toml
[plex]
url   = "http://192.168.1.10:32400"
token = "xxxxxxxxxxxxxxxxxxxx"
libraries = ["Music", "Jazz"]
```

| Key | Description |
|-----|-------------|
| `url` | Base URL of your Plex Media Server, including port (default `32400`) |
| `token` | Your `X-Plex-Token` for authentication |
| `libraries` | Optional comma-separated list of music library names to load. When omitted, all music libraries are loaded. Names are matched case-insensitively. |

If you access Plex remotely via `app.plex.tv`, you can still use a direct server URL if your server has remote access enabled, or use your server's `plex.direct` URL from the Plex Web address bar.

## Usage

Once configured, **Plex** appears as a provider in the cliamp TUI alongside Radio, Navidrome, Spotify, etc.

The provider exposes your server's audio playlists and your music library, grouped under two headers:

```text
── Playlists ──
GOOD
All Music
drums

── Albums ──
Artist - Album Title (Year)
```

Select a playlist or album to load its tracks, then play as normal. Smart playlists are included and resolve to their current contents, so they always reflect the server.

To start cliamp with Plex as the default provider:

```bash
cliamp --provider plex
```

Or set it persistently in config:

```toml
provider = "plex"
```

## How it works

cliamp calls the Plex HTTP API to enumerate your server's audio playlists (`/playlists`) and music library albums. When you select an entry, it fetches the track list (from the playlists items endpoint or the album children endpoint) and constructs authenticated streaming URLs of the form:

```
http://<server>:32400/library/parts/<partID>/<timestamp>/file.<ext>?X-Plex-Token=<token>
```

These are direct file-serve URLs. Plex serves the original file without transcoding, and cliamp's existing HTTP streaming pipeline handles playback. All formats supported by cliamp (MP3, FLAC, AAC, OGG, OPUS, WAV, etc.) work as long as the original file format is one of them.

## Troubleshooting

### macOS: `dial tcp ... connect: no route to host`

If cliamp reports `no route to host` for a Plex server on your LAN while `curl` to the same URL works, macOS is most likely denying the app Local Network access. macOS blocks these connections before any packet is sent and reports the block as a routing error.

Fix: open **System Settings > Privacy & Security > Local Network** and enable access for your terminal app (Terminal, iTerm2, Ghostty, etc.), then restart cliamp. If the terminal is not listed, toggle any entry off and on, or reset the permission database with `tccutil reset All` and relaunch the terminal so macOS prompts again.

## Known limitations

- **No scrobbling**: play counts are not reported back to Plex
- **No playlist write-back**: cliamp cannot create or modify Plex playlists
- **Token is long-lived**: store it carefully; it grants full access to your Plex account
- **Album list is flat**: no artist drill-down; search by scrolling or using cliamp's search
