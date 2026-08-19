# Spotify Integration

Cliamp can stream your [Spotify](https://www.spotify.com/) library directly through its audio pipeline. EQ, visualizer, and all effects apply. Requires a [Spotify Premium](https://www.spotify.com/premium/) account.

> **Windows:** Spotify support requires building cliamp with CGO enabled and a MinGW toolchain — see [Building from source](../README.md#building-from-source) in the README. Pre-built Windows binaries from Releases include Spotify support.
>
> **Quick start:** run `cliamp setup`, pick Spotify, and follow the prompts. The recommended path is to register your own Spotify Developer app and paste its `client_id` for a private Web API rate-limit quota. Cliamp authorizes playback separately with Spotify's built-in identity. A built-in shared `client_id` is also available for users who specifically need Spotify search.

## Setup

### Recommended: bring your own client ID

Register a Spotify Developer app and set `client_id` in `~/.config/cliamp/config.toml`:

```toml
[spotify]
client_id = "your_client_id_here"
bitrate = 320
```

To register one:

1. Go to [developer.spotify.com/dashboard](https://developer.spotify.com/dashboard) and log in
2. Click **Create app**
3. Fill in a name (e.g. "cliamp") and description (anything works)
4. Add `http://127.0.0.1:19872/login` as a **Redirect URI**
5. Check **Web API** under "Which API/SDKs are you planning to use?"
6. Click **Save**
7. Open your app's **Settings** and copy the **Client ID**

`bitrate` is optional. If omitted, cliamp uses `320`. Supported values are `96`, `160`, and `320`. Non-positive values (≤ 0) are treated as `320`. Other positive values are rounded to the nearest supported bitrate.

Run `cliamp`, select Spotify as a provider, and press Enter to sign in. When using your own `client_id`, the browser completes two authorization steps in the same tab: one for Web API access and one for playback. The built-in client path needs one step. Credentials are cached at `~/.config/cliamp/spotify_credentials.json`; subsequent launches refresh silently.

### Development Mode search page size

Spotify introduced its current Development Mode restrictions for new apps on February 11, 2026, then migrated existing Development Mode apps on March 9, 2026. Extended Quota Mode apps are unaffected. See Spotify's [February 2026 migration guide](https://developer.spotify.com/documentation/web-api/tutorials/february-2026-migration-guide) for the full timeline.

Search remains available in Development Mode, but `/v1/search` accepts at most **10 results per request**. Asking for more returns `400 "Invalid limit"`, which is not a sign that search is blocked. Cliamp handles this for you by paging through results 10 at a time with `offset`, so <kbd>Ctrl+F</kbd> returns the full set either way.

Other Development Mode changes remove endpoints such as `/v1/browse/new-releases` and restrict playlist items to playlists the user owns or collaborates on. `/v1/search` remains available and does not require Extended Quota Mode.

### Alternative: built-in shared client ID

If you would rather not register an app at all, drop the `client_id` line:

```toml
[spotify]
bitrate = 320
```

cliamp falls back to a built-in `client_id`, the same one [librespot](https://github.com/librespot-org/librespot) and [spotify-player](https://github.com/aome510/spotify-player) ship with.

> **Heads-up — shared rate limit:** The built-in `client_id` is shared with every librespot-, spotify-player-, and cliamp user worldwide. Spotify's per-app quota is global, so when the pool is busy you may see `429 Too Many Requests` errors during search or playlist loading. Cliamp retries with backoff, but persistent 429s mean the pool is hot — your own `client_id` doesn't share that problem.

## Usage

Once authenticated, Spotify appears as a provider alongside Navidrome and local playlists. Press `Esc`/`b` to open the provider browser and select Spotify.

Your Spotify playlists are listed in the provider panel. Navigate with the arrow keys and press `Enter` to load one. Tracks are streamed through cliamp's audio pipeline, so EQ, visualizer, mono, and all other effects work exactly as with local files.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate playlists |
| `Enter` | Load the selected playlist |
| `Tab` | Switch between provider and playlist focus |
| `Esc` / `b` | Open provider browser |

After loading a playlist you return to the standard playlist view with all the usual controls (seek, volume, EQ, shuffle, repeat, queue, search, lyrics).

## Playlists

Only playlists in your Spotify library are shown. This includes playlists you've created and playlists you've saved (followed). If a public playlist doesn't appear, open Spotify and click **Save** on it first. There's no need to copy tracks to a new playlist.

## Podcasts

Podcast episodes work like tracks. Press `Ctrl+F` to search Spotify and matching episodes (for example "Joe Rogan") appear alongside songs; press `Enter` to play. Playlists that mix songs and episodes load and play both.

## Troubleshooting

- **"OAuth failed"**: Make sure your redirect URI is exactly `http://127.0.0.1:19872/login` in the Spotify dashboard (no trailing slash).
- **Two authorization steps**: This is expected when using your own `client_id`. After Web API access is approved, the same browser tab redirects to create a playback credential using Spotify's required built-in identity.
- **Playlist not showing**: You must save/follow the playlist in Spotify for it to appear. Only your library playlists are listed.
- **Playback issues**: Spotify integration requires a Premium account. Free accounts cannot stream.
- **Re-authenticate**: Run `cliamp spotify reset` to clear stored credentials, then relaunch cliamp and select Spotify to sign in again. (Equivalent to deleting `~/.config/cliamp/spotify_credentials.json` manually.)
- **Persistent "rate-limited" errors on `/v1/me`**: Your stored auth has expired or been revoked. Cliamp will detect this on most launches and prompt you to sign in again, but if it does not, run `cliamp spotify reset` and re-authenticate. This is *not* a real Spotify rate limit — waiting will not resolve it.
- **`429 Too Many Requests` on search or playlist loading (using the built-in fallback)**: The built-in `client_id` is shared with every librespot- and spotify-player-based client; when the global pool is busy, Spotify caps requests for everyone using it. Cliamp retries with exponential backoff, but if the errors keep returning the simplest fix is to register your own developer app and set `client_id` in `[spotify]` — your personal app gets its own quota.
- **`400 "Invalid limit"` on <kbd>Ctrl+F</kbd>**: Development Mode apps cap `/v1/search` at 10 results per request. Cliamp pages around that automatically, so seeing this error means the cap moved below 10; please open an issue.

## Requirements

- Spotify Premium account
- No additional system dependencies beyond cliamp itself
- A registered app at [developer.spotify.com/dashboard](https://developer.spotify.com/dashboard) is **optional** — cliamp ships with a built-in fallback `client_id`
