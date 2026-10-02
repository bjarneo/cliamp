# Emby

Use cliamp to stream music from an Emby server through Emby's authenticated HTTP API. The provider pane shows music libraries as a flat album list, like the Plex provider.

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

When Emby is the default provider, cliamp remembers the most recently played Emby track, its playback position, and the album or track list it was chosen from. cliamp saves this state when a track starts, every two seconds during confirmed playback, and during a normal exit.

On the next launch with no explicit files, URLs, or playlist, cliamp restores that list with the last track selected. Press `Enter` to continue from the saved position. An `auto_play` setting is ignored for a restored list. Saved stream URLs use the current authentication when playback or preloading starts. This works as it does for [Jellyfin](jellyfin.md#usage).

Press `N` in the Emby pane to open the Emby browser. Select **By Album**, **By Artist**, or **By Artist / Album**. In **By Artist / Album**, the artists are in alphabetical order. Select an artist to open the albums of that artist. Select an album to open its songs.

## How it works

cliamp authenticates with an API key or the supplied username and password. It resolves the active Emby user and lists the music library views. It reads the albums of each view in pages of 500 and derives an alphabetical artist index from them. Then it gets the tracks for the selected album. Playback uses Emby's authenticated download endpoint and streams through the cliamp HTTP pipeline.

## Troubleshooting

### macOS: `dial tcp ... connect: no route to host`

If cliamp reports `no route to host` for an Emby server on the LAN, but `curl` works with the same URL, macOS likely denies Local Network access to the app. cliamp then adds a hint to the error.

Fix: Open **System Settings > Privacy & Security > Local Network**. Enable access for the terminal app, then restart cliamp. For more steps, see [Plex troubleshooting](plex.md#troubleshooting).

## Known limitations

- **Playback reporting**: cliamp reports now-playing status, progress, and stop
  events to Emby, so the server can track play activity and history.
- **Token-based access**: Store the API key safely.
- **API key user selection**: Emby API keys apply to the server and have no "current user". Without `user`, cliamp selects the first user returned by `/Users`. This is correct for a single-user server. On a multi-user server, set `user_id` in `[emby]` to select an account.
