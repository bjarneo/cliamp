# Spotify Integration

Use Cliamp to stream your [Spotify](https://www.spotify.com/) library through its audio pipeline. EQ, the visualizer, and other effects apply. You need a [Spotify Premium](https://www.spotify.com/premium/) account.

> **Windows:** Build cliamp with CGO enabled and a MinGW toolchain for Spotify support. See [Building from source](../README.md#building-from-source) in the README. Pre-built Windows binaries from Releases include Spotify support.
>
> **Quick start:** Run `cliamp setup`, select Spotify, and follow the prompts. Register a Spotify Developer app and enter its `client_id` to get a private Web API rate-limit quota, including for search. Cliamp authorizes playback separately with the built-in Spotify identity. You can instead use the built-in shared `client_id` without registering an app.

## Setup

### Recommended: bring your own client ID

Register a Spotify Developer app. Set its `client_id` in `~/.config/cliamp/config.toml`:

```toml
[spotify]
client_id = "your_client_id_here"
bitrate = 320
```

To register an app:

1. Go to [developer.spotify.com/dashboard](https://developer.spotify.com/dashboard) and sign in.
2. Click **Create app**.
3. Enter a name, such as "cliamp", and a description.
4. Add `http://127.0.0.1:19872/login` as a **Redirect URI**.
5. Select **Web API** under "Which API/SDKs are you planning to use?".
6. Click **Save**.
7. Open the app **Settings** and copy the **Client ID**.

`bitrate` is optional. If omitted, cliamp uses `320`. Supported values are `96`, `160`, and `320`. Values less than or equal to zero use `320`. cliamp rounds other positive values to the nearest supported bitrate.

Run `cliamp`, select Spotify, and press Enter to sign in. With your own `client_id`, the browser completes two authorization steps in one tab: one for Web API access and one for playback. The built-in client path needs one step. cliamp stores credentials in `~/.config/cliamp/spotify_credentials.json`. Later launches refresh them without a message.

### Development Mode search page size

Spotify introduced the current Development Mode restrictions for new apps on February 11, 2026. It migrated existing Development Mode apps on March 9, 2026. Extended Quota Mode apps are not affected. See the Spotify [February 2026 migration guide](https://developer.spotify.com/documentation/web-api/tutorials/february-2026-migration-guide) for the full timeline.

Search remains available in Development Mode, but `/v1/search` accepts at most **10 results per request**. A larger request returns `400 "Invalid limit"`. This does not mean search is blocked. Cliamp uses `offset` to page results in groups of 10. <kbd>Ctrl+F</kbd> returns the full result set.

Other Development Mode changes remove endpoints such as `/v1/browse/new-releases`. They restrict playlist items to playlists the user owns or collaborates on. `/v1/search` remains available and does not require Extended Quota Mode.

### Alternative: built-in shared client ID

To use no registered app, omit the `client_id` line:

```toml
[spotify]
bitrate = 320
```

cliamp uses a built-in `client_id`. [librespot](https://github.com/librespot-org/librespot) and [spotify-player](https://github.com/aome510/spotify-player) use the same client ID.

> **Shared rate limit:** The built-in `client_id` is shared by librespot, spotify-player, and cliamp users worldwide. Spotify applies its per-app quota globally. A busy pool can cause `429 Too Many Requests` during search or playlist loading. Cliamp retries with backoff. Persistent 429 errors indicate a busy pool. Your own `client_id` has a separate quota.

## Usage

After authentication, Spotify appears in the provider list. Press `Esc`/`b` to open the provider browser and select Spotify.

The provider panel lists Spotify playlists. Use the arrow keys to select one and press `Enter` to load it. Tracks stream through the cliamp audio pipeline. EQ, the visualizer, mono, and other effects work as they do for local files.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate playlists |
| `Enter` | Load the selected playlist |
| `Tab` | Switch between provider and playlist focus |
| `Esc` / `b` | Open provider browser |

After you load a playlist, Cliamp returns to the standard playlist view. Use the usual controls for seek, volume, EQ, shuffle, repeat, queue, search, and lyrics.

## Playlists

The provider shows only playlists in the Spotify library. This includes playlists you created and saved, or followed. If a public playlist is missing, open Spotify and click **Save** first. You do not need to copy tracks to a new playlist.

## Podcasts

Podcast episodes work as tracks. Press `Ctrl+F` to search Spotify. Matching episodes, such as "Joe Rogan", appear with songs. Press `Enter` to play. Playlists can load and play both songs and episodes.

## Troubleshooting

- **"OAuth failed"**: Ensure the Spotify dashboard redirect URI is exactly `http://127.0.0.1:19872/login`, without a trailing slash.
- **Two authorization steps**: This is expected with your own `client_id`. After you approve Web API access, the same browser tab redirects to create a playback credential with the required Spotify built-in identity.
- **Playlist not showing**: Save or follow the playlist in Spotify. The provider lists only library playlists.
- **Playback issues**: Spotify integration needs a Premium account. Free accounts cannot stream.
- **Re-authenticate**: Run `cliamp spotify reset` to clear stored credentials. Then restart cliamp, select Spotify, and sign in again. This is the same as deleting `~/.config/cliamp/spotify_credentials.json`.
- **Persistent "rate-limited" errors on `/v1/me`**: Stored authorization has expired or been revoked. Cliamp usually detects this at startup and prompts for sign-in. If it does not, run `cliamp spotify reset` and authenticate again. This is *not* a Spotify rate limit. Waiting does not fix it.
- **`429 Too Many Requests` on search or playlist loading (using the built-in fallback)**: The built-in `client_id` is shared with librespot- and spotify-player-based clients. When the global pool is busy, Spotify limits requests for every client that uses it. Cliamp retries with exponential backoff. If errors continue, register a developer app and set `client_id` in `[spotify]`. Your app has a separate quota.
- **`400 "Invalid limit"` on <kbd>Ctrl+F</kbd>**: Development Mode apps limit `/v1/search` to 10 results per request. Cliamp pages results automatically. This error means the limit is now less than 10. Open an issue.

## Requirements

- Spotify Premium account
- No additional system dependencies beyond cliamp itself
- A registered app at [developer.spotify.com/dashboard](https://developer.spotify.com/dashboard) is **optional**: cliamp has a built-in fallback `client_id`
