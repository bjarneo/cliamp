# Plex Media Server

Use cliamp to stream music from Plex Media Server, including any library served by PlexAmp. Streaming uses the Plex HTTP API. You do not need extra software.

> **Quick start:** Run `cliamp setup`. Enter the server URL and `X-Plex-Token`. The TUI checks the token and writes the `[plex]` block. Manual steps follow.

## Prerequisites

- Plex Media Server running and reachable on your network (or remotely)
- At least one music library configured in Plex
- Your `X-Plex-Token` (see below)

## Finding your X-Plex-Token

1. Open Plex Web in a browser and sign in.
2. Browse to an item in the music library.
3. Click the **···** menu → **Get Info** → **View XML**.
4. Copy the `X-Plex-Token` query parameter value from the XML page URL.

You can also follow the [official Plex guide](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/).

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
| `url` | Plex Media Server base URL, including port (default `32400`) |
| `token` | `X-Plex-Token` for authentication |
| `libraries` | Optional comma-separated music library names to load. If omitted, cliamp loads all music libraries. Name matching is case-insensitive. |

If you access Plex remotely through `app.plex.tv`, use a direct server URL when remote access is enabled. You can also use the server `plex.direct` URL from the Plex Web address bar.

## Usage

After configuration, **Plex** appears in the cliamp provider list.

The provider shows audio playlists and the music library under two headers:

```text
── Playlists ──
GOOD
All Music
drums

── Albums ──
Artist - Album Title (Year)
```

Select a playlist or album to load its tracks. Smart playlists are included. They resolve to current server content.

Start cliamp with Plex as the default provider:

```bash
cliamp --provider plex
```

Or set it in configuration:

```toml
provider = "plex"
```

## How it works

cliamp calls the Plex HTTP API to list audio playlists (`/playlists`) and music library albums. When you select an entry, cliamp gets its track list from the playlist-items or album-children endpoint. It then creates authenticated streaming URLs in this form:

```
http://<server>:32400/library/parts/<partID>/<timestamp>/file.<ext>?X-Plex-Token=<token>
```

These URLs serve files directly. Plex serves the original file without transcoding. cliamp handles playback through its HTTP streaming pipeline. cliamp can play MP3, FLAC, AAC, OGG, OPUS, WAV, and other supported source formats.

## Troubleshooting

### macOS: `dial tcp ... connect: no route to host`

If cliamp reports `no route to host` for a Plex server on the LAN, but `curl` works with the same URL, macOS likely denies Local Network access to the app. macOS blocks the connection before it sends a packet and reports a routing error.

Fix: Open **System Settings > Privacy & Security > Local Network**. Enable access for the terminal app, such as Terminal, iTerm2, or Ghostty. Then restart cliamp. If the terminal is not listed, turn an entry off and on. Or reset the permission database with `tccutil reset All`, then restart the terminal so macOS prompts again.

## Known limitations

- **No scrobbling**: cliamp does not report play counts to Plex.
- **No playlist write-back**: cliamp cannot create or change Plex playlists.
- **Token is long-lived**: Store it safely. It gives full access to the Plex account.
- **Album list is flat**: Artist drill-down is not available. Search by scrolling or with cliamp search.
