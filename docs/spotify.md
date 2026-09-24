# Spotify Integration

Use Cliamp to stream your [Spotify](https://www.spotify.com/) library through its audio pipeline. EQ, the visualizer, and other effects apply. You need a [Spotify Premium](https://www.spotify.com/premium/) account.

> **Windows:** The pre-built Windows binaries from Releases include Spotify support as a single `cliamp.exe` — unzip and run, no DLLs or extra installs. To build from source you need CGO with a MinGW toolchain; see [Building from source](../README.md#building-from-source) in the README.
>
> **Windows:** Stored Spotify credentials are encrypted with Windows DPAPI bound to your user account; existing plaintext spotify_credentials.json files are migrated automatically on first load.
>
> Sign-in listens for the OAuth callback on an ephemeral 127.0.0.1 port and falls back to port 19872 if needed. Custom Spotify apps should register a loopback redirect URI such as http://127.0.0.1:19872/login - Spotify allows any port on loopback redirect URIs.
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

Search remains available in Development Mode, but `/v1/search` accepts at most **10 results per request**. A larger request returns `400 "Invalid limit"`. This does not mean search is blocked. Cliamp uses `offset` to page results in groups of 10. <kbd>Ctrl+F</kbd> asks for 20 results of each kind — albums, tracks, and episodes — so a Development Mode app fetches them as two pages of 10.

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

The provider panel groups your library under four headers:

- **Library**: `Your Music` (liked songs), `Top Tracks` (roughly the last four weeks of listening, up to 200 tracks), and `Recently Played` (your last 50 plays, deduplicated).
- **Your playlists**: playlists you own.
- **Followed playlists**: playlists you've saved from other people.
- **Saved albums**: albums in your library; each one expands to its track list.

Navigate with the arrow keys and press `Enter` to load one. Tracks are streamed through cliamp's audio pipeline, so EQ, visualizer, mono, and all other effects work exactly as with local files.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate playlists |
| `Enter` | Load the selected playlist |
| `/` | Filter the playlist list |
| `Ctrl+R` | Refresh the playlist list |
| `N` | Open the library browser (Spotify: artists, albums, and the artist page) |
| `H` | Open the Home view |
| `Ctrl+F` | Search Spotify |
| `D` | Delete (owned) / unfollow (followed) the highlighted playlist, after an inline confirm |
| `r` | Rename the highlighted playlist you own |
| `Tab` | Return to playback controls, starting at Source when visible ([navigation](keybindings.md#navigation)) |
| `Esc` / `b` | Open provider browser |

After you load a playlist, Cliamp returns to the standard playlist view. Use the usual controls for seek, volume, EQ, shuffle, repeat, queue, search, and lyrics.

Large playlists fill in as they load. Cliamp shows the first tracks, appends the remaining pages in the background, and stays usable while the list arrives.

## Smart Shuffle

Press `Z` in the main view to toggle Smart Shuffle; lowercase `z` remains plain shuffle. Smart Shuffle works on Spotify queues only — cliamp resolves the provider that owns the queue, and other providers get a toast.

Turning it on also enables shuffle if it is off, and immediately tops the queue up with recommendations so the effect is visible right away. Further recommendations mix in near the end of the queue as it drains: injected rows are marked ✚, a `[Smart N]` chip beside `[Shuffle]` shows how many are pending, and each injection announces itself (`Smart Shuffle: +N queued`). Turning it off removes recommended rows that have not played yet and reports how many went (`Smart Shuffle off (-N queued)`); the current track and anything already played stay. A recommended track never repeats within a session, and recommendation failures back off quietly — playback is never interrupted.

The setting is saved as `smart_shuffle` in `~/.config/cliamp/config.toml` (top level, beside `shuffle`) and restored on the next launch; like the `Z` key, it implies shuffle.

## Library Browser

Press `N` at any time (or from the provider panel) to open the full-screen Spotify library browser. It lets you explore your library in three modes:

- **By Album**: browse the albums saved in Your Music, then open any album to see its tracks.
- **By Artist**: browse the artists you follow; selecting one opens their artist page (see below).
- **By Artist / Album**: same artist list — selecting one opens the artist page, whose Discography section replaces the album-list drill-down.

Artist discographies include albums and singles only; "appears on" and compilation entries are not listed. Artist rows show no album count because Spotify doesn't report one.

### Browser controls
## Playlists and albums

The provider lists both playlists and saved albums in the Spotify library. Playlists include those you created and saved, or followed. If a public playlist is missing, open Spotify and click **Save** first. You do not need to copy tracks to a new playlist.

Saved albums appear under a **Saved albums** section, labelled `Artist - Album` and sorted alphabetically by artist. These are the albums in **Your Library**. To add one, open the album in Spotify and click **Save**. Selecting a saved album loads all of its tracks in disc and track order.

Spotify's current API returns a playlist's items only for playlists you own or collaborate on. Opening any other playlist — one you follow, or one found in search results — may fail with an error.

### Write operations

Write actions apply to your Spotify account and require the same Premium account as playback:

| Key | Where | Action |
|---|---|---|
| `*` | Queue, library browser track list, artist page, Home view | Like/unlike the highlighted track |
| `S` | Search results and drill lists, artist page | Like/unlike the highlighted track |
| `x` | Queue mirroring a loaded Spotify playlist | Remove the track from the remote playlist |
| `p` | Search results and drill lists, artist page, Home view | Add the track to a Spotify playlist |
| `w` | Queue | Save tracks through the playlist picker, which offers a "Spotify Playlists" section (plus new-playlist creation) when the selected tracks are Spotify tracks; selections are added in batches |
| `D` | Provider panel, playlist row | Delete an owned playlist / unfollow a followed one |
| `r` | Provider panel, owned playlist row | Rename the playlist (inline input, prefilled) |
| `f` | Library browser artist list, search Artists tab, artist page | Follow/unfollow the artist |
| `f` | Search Playlists tab | Follow/unfollow the playlist (followed playlists only — owned rows point you to `D` in the provider pane) |

Notes:

- Likes are for music tracks only; podcast episodes can't be liked from cliamp.
- `x` refuses with a toast when the queue is shuffled or the row is outside the mirrored playlist; local playlists keep the plain remove behavior. Remote removal targets the track's URI rather than its position, so a track added to a playlist more than once is removed at every occurrence.
- Spotify returns `403` for modifications to a followed playlist you don't own, so rename and track removal apply to playlists you own. Following and unfollowing work on any playlist.
- **Unfollowing a playlist you own deletes it** — that's Spotify's semantics. The `D` confirm prompt says "Delete playlist" for owned rows and "Unfollow playlist" for followed ones; `Enter`/`y` confirms and any other key cancels.

## Podcasts

Podcast episodes work as tracks. Press `Ctrl+F` to search Spotify. Matching episodes, such as "Joe Rogan", appear with songs. Press `Enter` to play. Playlists can load and play both songs and episodes.

## Troubleshooting

- **"OAuth failed"**: Ensure the Spotify dashboard redirect URI is exactly `http://127.0.0.1:19872/login`, without a trailing slash. The temporary callback server listens only on this local address and does not accept connections from the network.
- **Two authorization steps**: This is expected with your own `client_id`. After you approve Web API access, the same browser tab redirects to create a playback credential with the required Spotify built-in identity.
- **Playlist not showing**: Save or follow the playlist in Spotify. The provider lists only library playlists.
- **Playback issues**: Spotify integration needs a Premium account. Free accounts cannot stream.
- **Re-authenticate**: Run `cliamp spotify reset` to clear stored credentials. Then restart cliamp, select Spotify, and sign in again. This is the same as deleting `~/.config/cliamp/spotify_credentials.json`.
- **Expired or revoked authorization**: Web API calls fail with `401 Unauthorized`, and playback asks for a new sign-in. Cliamp usually detects this at startup and prompts. If it does not, run `cliamp spotify reset` and authenticate again.
- **`429 Too Many Requests`, including `rate-limited on /v1/me`**: Spotify accepted the credentials and throttled the *app*, so re-authenticating does not help. With the built-in `client_id`, the quota is shared with librespot- and spotify-player-based clients worldwide, and a busy pool limits every client that uses it. Cliamp retries with exponential backoff, honoring `Retry-After`. A `Retry-After` of hours (for example `86400`) means the app has no quota left for that window: register a developer app and set `client_id` in `[spotify]`. Your app has a separate quota.
- **`400 "Invalid limit"` on <kbd>Ctrl+F</kbd>**: Development Mode apps limit `/v1/search` to 10 results per request. Cliamp pages results automatically. This error means the limit is now less than 10. Open an issue.

## Requirements

- Spotify Premium account
- No additional system dependencies beyond cliamp itself
- A registered app at [developer.spotify.com/dashboard](https://developer.spotify.com/dashboard) is **optional**: cliamp has a built-in fallback `client_id`
