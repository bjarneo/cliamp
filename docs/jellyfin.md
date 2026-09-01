# Jellyfin

Use cliamp to stream music from a Jellyfin server through Jellyfin's authenticated HTTP API. Jellyfin opens in an artists-first hierarchy by default: artist, then album, then songs.

> **Quick start:** Run `cliamp setup`. Select API-token or username+password authentication. The TUI validates `/Users/Me` and writes the `[jellyfin]` block. Manual steps follow.

## Prerequisites

- A reachable Jellyfin server
- At least one library with `CollectionType = music`
- A Jellyfin API token

## Configuration

Add a `[jellyfin]` section to `~/.config/cliamp/config.toml`:

```toml
[jellyfin]
url = "https://jellyfin.example.com"
user = "finamp"
password = "your_password_here"
# optional alternatives:
# token = "xxxxxxxxxxxxxxxxxxxx"
# user_id = "00000000000000000000000000000000"
```

| Key | Description |
|-----|-------------|
| `url` | Base URL of your Jellyfin server |
| `user` | Jellyfin username for password-based login |
| `password` | Jellyfin password for password login |
| `token` | Optional Jellyfin API token. Use it instead of a username and password. |
| `user_id` | Optional Jellyfin user id to skip discovery |

## Usage

After configuration, **Jellyfin** appears in the provider list.

To start cliamp with Jellyfin selected:

```bash
cliamp --provider jellyfin
```

Or set the provider in configuration:

```toml
provider = "jellyfin"
```

Jellyfin opens directly in **By Artist / Album** mode. Artists are listed alphabetically; selecting one opens their albums, and selecting an album opens its songs.

Press `N` while browsing Jellyfin to temporarily switch to **By Album** or **By Artist**. These alternate modes apply only to the current session; the next launch always returns to **By Artist / Album**.

## How it works

cliamp authenticates with a configured token or the supplied username and password. It resolves the active Jellyfin user, lists music library views, derives an alphabetical artist index from the album catalog, then gets tracks for the selected album. Playback uses Jellyfin's authenticated audio endpoint and streams through the cliamp HTTP pipeline.

## Known limitations

- **No scrobbling/write-back**: cliamp does not report plays to Jellyfin.
- **Token-based access**: Store the API token safely.
