# Plex Media Server

Use cliamp to stream music from Plex Media Server, including any library served by PlexAmp. Streaming uses the Plex HTTP API. You do not need extra software.

> **Quick start:** Run `cliamp setup`, select **Sign in with Plex**, approve
> cliamp in the browser, and select a server. cliamp discovers and verifies
> local, remote, and relay connections automatically. Manual setup remains
> available.

## Prerequisites

- Plex Media Server running and signed in to your Plex account
- At least one music library configured in Plex

For access away from home, enable Plex Remote Access. Plex Relay is used as a
fallback when the account advertises no reachable direct connection.

## Browser sign-in

Run `cliamp setup`, select **Plex Media Server**, and keep the recommended
browser sign-in option. cliamp opens the Plex-hosted authorization page and
waits for approval. If more than one server is available, select one in the
wizard. The chosen server and a stable identifier for this cliamp installation
are saved in `[plex]`.

On every start, cliamp asks Plex for the selected server's current connection
addresses. It prefers a reachable local address, then a secure direct remote
address, and finally a secure relay. Public plain-HTTP addresses are never
selected. Pressing `Ctrl+R` also repeats connection discovery.

This means the same configuration can move between home and work without
hard-coding a `192.168.x.x` address. Remote playback still requires the Plex
server to advertise a reachable remote or relay connection.

## Sign out

Run `cliamp setup`, select **Plex Media Server**, and choose **Sign out and
remove saved credentials**. While `[plex]` holds a token, this is the only
option: to change the server or the login type, sign out and sign in again.

- After a browser sign-in, cliamp asks Plex to revoke the token, checks that
  Plex no longer accepts it, and removes `token`, `server_id`, `url`, and
  `libraries` from `[plex]`.
- A manual token was copied from Plex Web and belongs to that session, so
  cliamp does not revoke it. It removes `token` and `libraries` and keeps the
  server URL.

`client_id` stays, so a later sign-in is the same Plex device. If Plex cannot be reached or still accepts the
token, cliamp removes it anyway and asks you to remove the device under
**Authorized Devices** in your Plex account. Restart a running cliamp, which
keeps the token in memory.

## Manual token setup

To configure a fixed server URL instead, select the manual option in setup or
find an `X-Plex-Token`:

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
client_id = "generated-per-installation-id"
server_id = "selected-server-id"
libraries = ["Music", "Jazz"]
```

| Key | Description |
|-----|-------------|
| `url` | Plex Media Server base URL, including port (default `32400`) |
| `token` | `X-Plex-Token` for authentication |
| `client_id` | Stable cliamp installation ID written by browser sign-in |
| `server_id` | Selected Plex Media Server ID written by browser sign-in |
| `libraries` | Optional comma-separated music library names to load. If omitted, cliamp loads all music libraries. Name matching is case-insensitive. |

`client_id` and `server_id` enable account discovery. Omit both for a fixed
manual URL. Do not copy them between installations; rerun browser sign-in on
the other computer so Plex lists it as a separate authorized device.

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

Plex likes are shown as hearts in cliamp. Press `f` on a Plex track to change
its rating on the Plex server; the change appears in Plexamp and other Plex
clients. Plex is the source of truth for these hearts. cliamp neither reads nor
writes its local `favorites.toml` entry when it draws or toggles a Plex heart.
Existing local Plex favorites are left intact but do not control that heart, so
the change does not discard old data. Press `Ctrl+R` to pick up likes changed
in another client while the list is open.

While a Plex track plays, cliamp reports its timeline position about every 15 seconds. After at least half of a known-duration track has played, leaving the track records one Plex play and updates Plex history. Short skips do not increment the play count.

When Plex is the default provider (`provider = "plex"` or `--provider plex`),
cliamp remembers the most recently played Plex track, its playback position,
and the album or playlist it was chosen from. State is saved when a track
starts, every two seconds during confirmed playback, and during a normal exit.
On the next launch with no explicit files, URLs, or playlist, cliamp restores
that list with the last track selected; the time and seek bar already show the
saved position. Press `Enter` to continue from it; `auto_play` is ignored for a restored list. The saved list keeps only
token-free `plex:track:` paths, and the current Plex credentials are added when
playback starts. This works as it does for [Jellyfin](jellyfin.md#usage).

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
