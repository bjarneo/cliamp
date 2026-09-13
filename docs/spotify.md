# Spotify Integration

Cliamp can stream your [Spotify](https://www.spotify.com/) library directly through its audio pipeline. EQ, visualizer, and all effects apply. Requires a [Spotify Premium](https://www.spotify.com/premium/) account.

> **Windows:** Spotify is currently unavailable on Windows builds because the `go-librespot` playback backend used by cliamp does not compile there yet.
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

### Newer apps and the search caveat

Apps registered in Development Mode (the default for anything created on developer.spotify.com after Nov 27, 2024) still work for your library, your playlists, save/follow actions, and OAuth itself. Playback uses its separate authorization. The one specific thing newer apps cannot do is hit Spotify's **catalog endpoints**: `/v1/search` and a handful of related endpoints.

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

The provider panel groups your library under three headers:

- **Library**: `Your Music` (liked songs), `Top Tracks` (roughly the last four weeks of listening, up to 200 tracks), and `Recently Played` (your last 50 plays, deduplicated).
- **Your playlists**: playlists you own.
- **Followed playlists**: playlists you've saved from other people.

Navigate with the arrow keys and press `Enter` to load one. Tracks are streamed through cliamp's audio pipeline, so EQ, visualizer, mono, and all other effects work exactly as with local files.

Large playlists load incrementally: the first 200 tracks appear immediately and playback can start right away, while the rest stream in behind a "Loading more tracks…" indicator. Editing the queue while more tracks are loading simply stops the background loading.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate playlists |
| `Enter` | Load the selected playlist |
| `/` | Filter the playlist list |
| `Ctrl+R` | Refresh the playlist list |
| `N` | Open the Spotify library browser |
| `H` | Open the Home view |
| `Ctrl+F` | Search Spotify |
| `D` | Delete (owned) / unfollow (followed) the highlighted playlist, after an inline confirm |
| `r` | Rename the highlighted playlist you own |
| `Tab` | Switch between provider and playlist focus |
| `Esc` / `b` | Back to the playlist pane |

After loading a playlist you return to the standard playlist view with all the usual controls (seek, volume, EQ, shuffle, repeat, queue, search, lyrics).

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

**Mode menu:**

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Navigate |
| `Enter` | Select mode |
| `Esc` / `N` | Close browser |

**Artist or album list:**

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Navigate |
| `Enter` / `→` | Artist list: open the artist page · album list: drill in |
| `s` | Cycle album sort order (saved-album list only) |
| `f` | Follow/unfollow the highlighted artist |
| `Esc` / `←` | Back |

**Track list:**

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Navigate |
| `Enter` | Append selected track to playlist |
| `a` | Append all tracks to playlist |
| `R` | Replace playlist with all tracks and start playing |
| `*` | Like/unlike the highlighted track |
| `Esc` / `←` | Back |

### Album sort order

While viewing the saved-album list (By Album mode), press `s` to cycle through sort modes:

| Value | Description |
|---|---|
| `recent` | Recently saved (default) |
| `title` | A → Z by album title |
| `artist` | A → Z by artist name, then title |
| `year` | Release year, newest first |

The chosen sort is saved automatically to `~/.config/cliamp/config.toml` under the `[spotify]` section as `album_sort` and is restored on the next launch.

## Home

Press `H` from the main view to open the Home view: a two-pane browser for your library. The sidebar is sectioned into **Playlists** (yours and followed), **Albums** (saved in Your Music), and **Artists** (followed), with a `+ New playlist` row at the top — `Enter` on it creates a playlist through an inline name prompt. `/` filters all three lists at once, and `s` cycles the sidebar order (recents / recently added / alphabetical). The `s` order is applied locally: "recently added" only reorders albums, reversing the saved order, because playlists and artists expose no added-at. `S` cycles the saved-albums sort and persists it — the same `album_sort` setting as the library browser's `s`.

Press `Enter` on a playlist to open its tracks in the content pane (large playlists load incrementally, like the queue), on an album for its tracks, or on an artist for their artist page; `Esc` returns from the artist page to Home. `Tab` and `Ctrl+arrows` switch pane focus, a breadcrumb shows the drill path (`Home / Playlists / <name>`), and track rows carry the usual actions (`Enter` plays and enqueues the rest, `a`, `q`, `*`, `p` — in Home, liking is `*` only, since `S` is the album-sort cycle). `Esc` pops the content pane and then closes Home. The full key table is in [Keybindings](keybindings.md#home-view-h-key).

## Search

Press `Ctrl+F` to search Spotify. Results are grouped into four tabs — **Tracks**, **Albums**, **Artists**, **Playlists** — each with a live count. Switch tabs with `←` `→` (or `Tab`/`Shift+Tab`); switching resets the cursor.

Press `Enter` on a row to drill in: an album loads its tracks, an artist opens their artist page, and a playlist loads its tracks incrementally. A breadcrumb above the list shows the drill path, for example `Album — Rumours`. Drilled lists support the usual track actions: `Enter` to play, `a` to append, `q` to queue next, `p` to add to a playlist, `S` to like. `Esc`/`Backspace` (also `←`/`h`) pops one drill level; backing out of the last level returns to the tab bar with the previous tab and cursor intact.

Each tab shows up to 10 results — Spotify's current API caps search at 10 results per type. Podcast episodes still merge into the track results alongside songs.

## Artist page

Selecting an artist — from the search Artists tab, the library browser's artist lists, or the Home view's Artists section — opens their artist page instead of a flat album list. The header shows follower count, genres, and a `Following` marker once you follow, and the body is sectioned into:

- **Popular**: the artist's top tracks. `s` cycles the sort — popularity, recency, liked-first.
- **Liked Songs**: liked tracks among the artist's popular picks (not your complete liked-songs-by-artist list).
- **Discography**: albums and singles. `Enter` drills into an album's tracks; `Esc` returns.

`Enter` on a track plays it and enqueues the rest of its section, `f` follows/unfollows the artist, and track rows carry the same `a`/`q`/`p`/`*`/`S` actions as the search drill lists. `Esc`/`Backspace` pops back to whatever opened the page. Spotify's API doesn't expose play counts, so the Popular ordering uses its popularity score instead, and follower and genre data come from fields Spotify has deprecated but still returns.

## Playlists

Only playlists in your Spotify library are shown. This includes playlists you've created and playlists you've saved (followed). If a public playlist doesn't appear, open Spotify and click **Save** on it first. There's no need to copy tracks to a new playlist.

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

Podcast episodes work like tracks. Press `Ctrl+F` to search Spotify and matching episodes (for example "Joe Rogan") appear alongside songs; press `Enter` to play. Playlists that mix songs and episodes load and play both.

## Troubleshooting

- **"OAuth failed"**: Make sure your redirect URI is exactly `http://127.0.0.1:19872/login` in the Spotify dashboard (no trailing slash).
- **Two authorization steps**: This is expected when using your own `client_id`. After Web API access is approved, the same browser tab redirects to create a playback credential using Spotify's required built-in identity.
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
