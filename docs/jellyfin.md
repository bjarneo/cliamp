# Jellyfin

cliamp can stream music directly from a Jellyfin server using Jellyfin's authenticated HTTP API. The first integration exposes your music libraries as a flat album list in the normal provider pane, following the same shape as the Plex provider.

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
| `password` | Jellyfin password for password-based login |
| `token` | Optional Jellyfin API token instead of username/password |
| `user_id` | Optional Jellyfin user id to skip discovery |

## Usage

Once configured, **Jellyfin** appears as a provider alongside Radio, Navidrome, Plex, Spotify, and the YouTube providers.

To start cliamp with Jellyfin selected:

```bash
cliamp --provider jellyfin
```

Or set it in config:

```toml
provider = "jellyfin"
```

The provider currently exposes a flat list of albums:

```text
Artist - Album Title (Year)
```

Select an album to load its tracks, then play as normal.

## How it works

cliamp authenticates with either a configured token or the supplied username/password, resolves the active Jellyfin user, enumerates music library views, fetches album items from those views, then fetches track items for the selected album. Playback uses Jellyfin's authenticated audio endpoint, so the existing cliamp HTTP pipeline can stream the result directly.

## Known limitations

- **Album list is flat**: no artist drill-down yet
- **No scrobbling/write-back**: plays are not reported back to Jellyfin yet
- **Token-based access**: store the API token carefully
