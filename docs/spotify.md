# Spotify Integration

Cliamp can stream your [Spotify](https://www.spotify.com/) library directly through its audio pipeline. EQ, visualizer, and all effects apply. Requires a [Spotify Premium](https://www.spotify.com/premium/) account.

> **Quick start:** run `cliamp setup`, pick Spotify, and follow the prompts. The recommended path is to register your own Spotify Developer app and paste its `client_id` — it gives you a private rate-limit quota and works for playback, library, and playlists. There's also a built-in shared `client_id` available for users who specifically need Spotify search.

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

Run `cliamp`, select Spotify as a provider, and press Enter to sign in. Credentials are cached at `~/.config/cliamp/spotify_credentials.json`. Subsequent launches refresh silently.

### Newer apps and the search caveat

Apps registered in Development Mode (the default for anything created on developer.spotify.com after Nov 27, 2024) **still work for almost everything** — playback, your library, your playlists, save/follow actions, OAuth itself. The one specific thing they can't do is hit Spotify's **catalog endpoints**: `/v1/search` and a handful of related endpoints.

You'll see the catalog restriction as `400 "Invalid limit"` whenever you press <kbd>Ctrl+F</kbd> to search Spotify — Spotify [introduced this restriction on Nov 27, 2024](https://developer.spotify.com/blog/2024-11-27-changes-to-the-web-api) and rarely grants Extended Quota Mode to personal/non-commercial apps. Cliamp surfaces a friendlier error explaining what's actually wrong instead of the raw "Invalid limit" message.

If you don't use Spotify search often, your own `client_id` is the better choice — keep it.

### Alternative: built-in shared client ID

If Spotify search is essential to you and your own app hits the dev-mode restriction above, drop the `client_id` line:

```toml
[spotify]
bitrate = 320
```

cliamp falls back to a built-in `client_id` (the same one [librespot](https://github.com/librespot-org/librespot) and [spotify-player](https://github.com/aome510/spotify-player) ship with) which predates the Nov 27, 2024 cutoff and retains catalog access.

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

## Troubleshooting

- **"OAuth failed"**: Make sure your redirect URI is exactly `http://127.0.0.1:19872/login` in the Spotify dashboard (no trailing slash).
- **Playlist not showing**: You must save/follow the playlist in Spotify for it to appear. Only your library playlists are listed.
- **Playback issues**: Spotify integration requires a Premium account. Free accounts cannot stream.
- **Re-authenticate**: Run `cliamp spotify reset` to clear stored credentials, then relaunch cliamp and select Spotify to sign in again. (Equivalent to deleting `~/.config/cliamp/spotify_credentials.json` manually.)
- **Persistent "rate-limited" errors on `/v1/me`**: Your stored auth has expired or been revoked. Cliamp will detect this on most launches and prompt you to sign in again, but if it does not, run `cliamp spotify reset` and re-authenticate. This is *not* a real Spotify rate limit — waiting will not resolve it.
- **`429 Too Many Requests` on search or playlist loading (using the built-in fallback)**: The built-in `client_id` is shared with every librespot- and spotify-player-based client; when the global pool is busy, Spotify caps requests for everyone using it. Cliamp retries with exponential backoff, but if the errors keep returning the simplest fix is to register your own developer app and set `client_id` in `[spotify]` — your personal app gets its own quota.
- **"search blocked — your client_id is too new" on <kbd>Ctrl+F</kbd>**: Your registered Spotify Developer app is in Development Mode and can't hit `/v1/search` (Spotify's Nov 27, 2024 change). Everything else on your app — playback, library, playlists, save/follow — still works fine. Either remove `client_id` from `[spotify]` to use the built-in fallback for search, or just don't use Spotify search and keep your own app.

## Requirements

- Spotify Premium account
- No additional system dependencies beyond cliamp itself
- A registered app at [developer.spotify.com/dashboard](https://developer.spotify.com/dashboard) is **optional** — cliamp ships with a built-in fallback `client_id`
