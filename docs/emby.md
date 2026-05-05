# Emby

cliamp can stream music directly from an Emby server using Emby's authenticated HTTP API. The integration exposes your music libraries as a flat album list in the normal provider pane, following the same shape as the Jellyfin and Plex providers.

> **Quick start:** run `cliamp setup` for a guided TUI that lets you pick API-key or username+password auth, validates against `/System/Info`, and writes the `[emby]` block for you. Manual setup steps are below.

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
| `user` | Emby username — used for password login, and to select the matching account when using an API key |
| `password` | Emby password for password-based login |
| `token` | Emby API key — alternative to username/password |
| `user_id` | Optional Emby user id to skip discovery |

## Usage

Once configured, **Emby** appears as a provider alongside Radio, Navidrome, Plex, Jellyfin, Spotify, and the YouTube providers.

To start cliamp with Emby selected:

```bash
cliamp --provider emby
```

Or set it in config:

```toml
provider = "emby"
```

The provider exposes a flat list of albums:

```text
Artist — Album Title (Year)
```

Select an album to load its tracks, then play as normal. Press `E` anywhere in the UI to switch to Emby quickly.

## How it works

cliamp authenticates with either a configured API key or the supplied username/password, resolves the active Emby user, enumerates music library views, fetches album items from those views, then fetches track items for the selected album. Playback uses Emby's authenticated download endpoint, so the existing cliamp HTTP pipeline can stream the result directly.

## Known limitations

- **Album list is flat**: no artist drill-down yet
- **Token-based access**: store the API key carefully
- **API key user selection**: Emby API keys are server-level (no "current user"). When no `user` is configured, cliamp picks the first user returned by `/Users`. On single-user servers this is always correct; on multi-user servers, set `user_id` explicitly in `[emby]` to target a specific account.
