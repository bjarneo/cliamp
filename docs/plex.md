# Plex Media Server

Use cliamp to stream music from Plex Media Server, including any library served by PlexAmp. Streaming uses the Plex HTTP API. You do not need extra software.

> **Quick start:** Run `cliamp setup`. Enter the server URL and `X-Plex-Token`, and optionally a comma-separated list of music libraries to include. The TUI checks the token and writes the `[plex]` block. Manual steps follow.

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

Press `Ctrl+R` while an open Plex album or playlist is focused to reload it
and pick up server-side changes. Album contents are cached for up to five
minutes; Plex playlists are always loaded fresh so smart playlists stay current.

Press `f` on a Plex track to add or remove it from cliamp Favorites. The same
change is copied to Plex and appears in Plexamp and other Plex clients.

While a Plex track plays, cliamp reports its timeline position about every 15 seconds. After at least half of a known-duration track has played, leaving the track records one Plex play and updates Plex history. Short skips do not increment the play count.

Start cliamp with Plex as the default provider:

```bash
cliamp --provider plex
```

Or set it in configuration:

```toml
provider = "plex"
```

## How it works

cliamp calls the Plex HTTP API to list audio playlists (`/playlists`) and music library albums. When you select an entry, cliamp gets its track list from the playlist-items or album-children endpoint. Queues, playlists, favorites, history, resume state, and IPC keep only a token-free logical path:

```
plex:track:<ratingKey>
```

When playback starts, cliamp resolves that path to the Plex part URL in memory and authenticates the request with your token. The token is not written into track paths or exposed through status output. Plex serves the original file without transcoding. cliamp handles playback through its HTTP streaming pipeline and can play MP3, FLAC, AAC, OGG, OPUS, WAV, and other supported source formats.

## Troubleshooting

### macOS: `dial tcp ... connect: no route to host`

If cliamp reports `no route to host` for a Plex server on the LAN, but `curl` works with the same URL, macOS likely denies Local Network access to the app. macOS blocks the connection before it sends a packet and reports a routing error.

Fix: Open **System Settings > Privacy & Security > Local Network**. Enable access for the terminal app, such as Terminal, iTerm2, or Ghostty. Then restart cliamp. If the terminal is not listed, turn an entry off and on. Or reset the permission database with `tccutil reset All`, then restart the terminal so macOS prompts again.

## Known limitations

- **No playlist write-back**: cliamp cannot create or change Plex playlists.
- **Token is long-lived**: Store it safely. It gives full access to the Plex account.
- **Album list is flat**: Artist drill-down is not available. Search by scrolling or with cliamp search.
