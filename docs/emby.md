# Emby

Use cliamp to stream music from an Emby server through Emby's authenticated HTTP API. The provider pane shows music libraries as a flat album list, like the Jellyfin and Plex providers.

> **Quick start:** Run `cliamp setup`. Select API-key or username+password authentication. The TUI validates `/System/Info` and writes the `[emby]` block. Manual steps follow.

## Prerequisites

- A reachable Emby server
- At least one library with `CollectionType = music`
- An Emby API key or user credentials

## Configuration

Add an `[emby]` section to `~/.config/cliamp/config.toml`:

```toml
[emby]
url = "https://emby.example.com"
user = "alice"
password = "your_password_here"
# optional alternatives:
# token = "xxxxxxxxxxxxxxxxxxxx"
# user_id = "00000000000000000000000000000000"
```

| Key | Description |
|-----|-------------|
| `url` | Base URL of your Emby server |
| `user` | Emby username. Use it for password login and to select the account for an API key. |
| `password` | Emby password for password login |
| `token` | Emby API key. Use it instead of a username and password. |
| `user_id` | Optional Emby user id to skip discovery |

## Usage

After configuration, **Emby** appears in the provider list.

To start cliamp with Emby selected:

```bash
cliamp --provider emby
```

Or set the provider in configuration:

```toml
provider = "emby"
```

The provider shows a flat album list:

```text
Artist — Album Title (Year)
```

Select an album to load its tracks. Press `E` to select Emby.

## How it works

cliamp authenticates with an API key or the supplied username and password. It resolves the active Emby user, lists music library views, gets albums from those views, then gets tracks for the selected album. Playback uses Emby's authenticated download endpoint and streams through the cliamp HTTP pipeline.

## Known limitations

- **Album list is flat**: Artist drill-down is not available.
- **Token-based access**: Store the API key safely.
- **API key user selection**: Emby API keys apply to the server and have no "current user". Without `user`, cliamp selects the first user returned by `/Users`. This is correct for a single-user server. On a multi-user server, set `user_id` in `[emby]` to select an account.
