# Mixcloud Integration

Enable [Mixcloud](https://www.mixcloud.com) to browse catalog metadata through
the Mixcloud public JSON API. Playback uses the existing `yt-dlp` and FFmpeg
pipeline. Put `yt-dlp` and `ffmpeg` on `PATH`.

## Feature summary

| Feature | Requirement | Where to use it |
|---|---|---|
| Direct Mixcloud show URL playback | `yt-dlp`; provider need not be enabled | Pass the URL to `cliamp` or press `u` |
| Recent releases, popular shows, show browsing, and show search | `[mixcloud] enabled = true` | Provider pane, `N`, or `Ctrl+F` |
| Live category catalogue and Latest/Popular genre charts | Provider enabled | **Genres** in the provider pane or `N` browser |
| Genre/tag search and local genre favorites | Provider enabled and writable config file | `/`, `Enter`, and `f` in **Genres** |
| Public profile activity, uploads, show favorites, listening history, and collections | Public profile `username`, or an `access_token` | **Your Mixcloud** and **Collections** sections |
| Following stream and followed-creator browser | Public profile `username`, or an `access_token` | **Stream (Following Releases)** and **Creators** |
| Open a highlighted show's creator Uploads/Favorites | Provider enabled; account not required | Press `N` on a Mixcloud show |
| Listen Later | Developer OAuth `access_token` | **Your Mixcloud** section |
| Signed-in or subscriber-gated playback | `cookies_from` for a supported browser with a Mixcloud session | Playback through yt-dlp |
| Exclusive-show warning | Provider enabled | `[E]` suffix on show rows |
| Resume and seek-by-restart for finite shows | Playable finite Mixcloud show | Normal cliamp seek keys and clean-exit resume |

The provider does not implement Mixcloud write actions. You cannot follow a
creator, favorite or repost a show, edit a collection, or upload. The
**Favorites** lists are read-only. Genre favorites are a separate local cliamp
feature described below.

## Setup

Run the setup wizard. Select **Mixcloud**:

```sh
cliamp setup
```

Public discovery does not need an account. Use this minimal manual
configuration:

```toml
[mixcloud]
enabled = true
```

Add a public username to show account views:

```toml
[mixcloud]
enabled = true
username = "your-mixcloud-username"
```

Use the username from the public profile URL. Do not use an email address or
display label. Keep the spelling from
`https://www.mixcloud.com/<username>/`.

To start with Mixcloud, also set the top-level provider option:

```toml
provider = "mixcloud"
```

## Provider pane

Press `X` to select Mixcloud. The provider pane has these sections.

| Section | Entry | Behaviour |
|---|---|---|
| **Your Mixcloud** | **Stream (Following Releases)** | Merges recent uploads from followed creators. See [Limits and tuning](#limits-and-tuning). |
| **Your Mixcloud** | **Favorites** | Reads the configured public account or the token owner `/me/` connections |
| **Your Mixcloud** | **Creators** | Lists the configured account and followed creators. Separates each creator **Uploads** and **Favorites**. |
| **Your Mixcloud** | **Uploads**, **Profile Activity**, **Listening History** | Reads the configured public account or the token owner `/me/` connections |
| **Your Mixcloud** | **Listen Later** | Appears only when `access_token` is set |
| **Browse** | **Shows** | Opens the global show catalogue. Sort by Recent Releases, Popular, or each configured style. |
| **Browse** | **Genres** | Loads the current Mixcloud category catalogue, shows local favorite state, and opens Latest or Popular shows for a category |
| **Collections** | One row per collection | Reads all collections for the account and shows the API track count |
| **Discover** | **Recent Releases**, **Popular** | Global public discovery. No account required. |
| **Music Styles** | `<Style> — Latest`, `<Style> — Popular` | One pair for each entry in `[mixcloud].styles` |

With a configured account, **Your Mixcloud** is first. It lists **Stream**,
then **Favorites**, then **Creators**. Public-only and degraded-account views
start with **Browse** and omit **Creators**. The hierarchy rows are shortcuts.
When you back out of the top-level Shows, Creators, or Genres list, cliamp
returns to this provider pane.

Select a **Show**, creator **Uploads** or **Favorites**, or genre **Latest** or
**Popular** view. cliamp loads the results in the main playlist and closes the
provider browser. For empty results, cliamp keeps the playlist and browser open
and shows a warning.

`Ctrl+R` reloads provider lists and clears the cached `/me/` identity. cliamp
otherwise gets show, category, collection, and account results when you open
them. It does not retain them as an expiring audio-URL cache.

If account resolution or collection loading fails, cliamp shows a warning. A
mistyped username, expired token, or API rate limit can cause this. cliamp then
temporarily omits **Your Mixcloud** and **Collections**. Public **Browse**,
**Discover**, and **Music Styles** entries remain available.

## Hierarchical browsing and controls

Press `N` with no recognized Mixcloud show selected to open the normal provider
browser. Mixcloud has four routes:

- **By Show** browses the global show catalogue. Press `s` to cycle Recent
  Releases, Popular, and Latest/Popular for each configured style.
- **By Creator** selects a creator and combines creator Uploads and Favorites
  into a track list.
- **By Creator / Show** selects a creator, then loads an **Uploads** or
  **Favorites** collection into the main playlist.
- **Genres** browses and searches categories, then loads Latest or Popular into
  the main playlist.

The creator list contains the configured account and followed creators. Both
creator routes need `username` or `access_token`. **By Show** and **Genres** are
public. Breadcrumbs use the Mixcloud terms **Creator** and **Show** to show the
current route.

When a Mixcloud show is selected in the main playlist, `N` becomes
**Browse creator**. It opens that show creator and the separate Uploads/Favorites
choices. This works even when you do not follow the creator. `Esc` returns to
the normal browse menu. Press `Esc` again to return to the source playlist.
Browsing through **Shows**, **Creators**, **Genres**, or the normal `N` menu
uses one-level back navigation.

| Key | Mixcloud action |
|---|---|
| `X` | Open the Mixcloud provider |
| `N` | Open the browse menu, or open the selected Mixcloud show creator |
| `Ctrl+F` | Search Mixcloud shows from the provider pane or browser |
| `/` | Filter the visible creator, show, genre, sort, or track list |
| `Enter` while filtering **Genres** | Replace the local category filter with a Mixcloud server tag search |
| `f` in the **Genres** list | Add or remove the selected genre from local Mixcloud favorites |
| `s` in the global show list | Cycle Recent, Popular, and configured-style show views |
| `Enter` / `→` / `l` | Drill in. On a track row, play it and queue the remaining visible rows. |
| `a` | Append visible browser tracks to the queue |
| `q` on a browser track | Queue the selected show to play next |
| `R` on browser tracks | Replace the queue with visible shows, with confirmation when needed |
| `Esc` / `←` / `h` / `b` | Go back one level or close the browser |
| `Ctrl+R` in the provider pane | Refresh Mixcloud lists and account identity |
| `u` | Paste a Mixcloud show URL |
| `Ctrl+S` | Save or download the current show through the cliamp track-save flow |

Search and browse result lists use the normal cliamp filter, append, play,
queue-next, and replace-queue actions.

## Genre browsing and favorites

cliamp loads **Genres** from the current Mixcloud public category catalogue. A
row shows `★` when a genre is in `[mixcloud].styles` and `☆` when it is not.
cliamp shows Mixcloud category groups, such as Music or Talk, next to the
category name.

Type after `/` to filter the loaded catalogue by category name or group. Press
`Enter` to submit the same text to the Mixcloud tag search. It can find
categories outside the initial catalogue. Select a category or search result to
open **Latest** and **Popular** show lists.

Press `f` on a genre to change its star. cliamp then:

1. Atomically rewrites only `styles` in the `[mixcloud]` config section.
2. Preserves other Mixcloud settings, comments, and unrelated sections.
3. Updates the in-memory favorite state.
4. Refreshes **Music Styles** provider rows. It adds or removes that genre Latest/Popular pair.

This is a local cliamp preference and does not need an access token. It does not
favorite anything on the Mixcloud website. If config write fails, cliamp keeps
the previous UI and configuration state.

The default styles are ambient, chillout, deep house, disco, drum and bass,
electronica, funk, hip-hop, house, jazz, reggae, soul, techno, trance, and
world. Override them with Mixcloud genre slugs:

```toml
[mixcloud]
enabled = true
styles = ["ambient", "balearic", "deep-house", "jazz"]
```

Omit `styles` to use defaults. Set `styles = []` to start with no favorite genres
and no **Music Styles** rows. The live **Genres** catalogue remains available,
so you can add styles again with `f`. cliamp normalizes values to lowercase
hyphenated slugs and removes duplicates.

### Favorite terminology

Mixcloud has several similarly named concepts:

- **Your Mixcloud / Favorites** reads shows that the configured account favorited.
- **Creator / Favorites** reads public show favorites for a selected creator.
- `f` on a show in the cliamp main playlist changes a local cliamp track bookmark.
- `f` in **Genres** changes a local Mixcloud style in `[mixcloud].styles`.

None of these controls writes a show favorite to Mixcloud.

## Account identity and authentication

Mixcloud allows public API reads without authentication. cliamp supports four
configuration levels:

| Configuration | Purpose |
|---|---|
| `enabled = true` | Public discovery, shows, genres, genre/tag search, and show search |
| `username` | Public account connections: following, activity, uploads, show favorites, listening history, and collections |
| `access_token` | Resolves the authorized user through `/me/` and adds Listen Later |
| `cookies_from` | Gives yt-dlp a signed-in browser session for playback only |

If both `username` and `access_token` are set, the access-token owner `/me/`
identity controls all account views. This prevents a stale or different public
username from mixing two accounts in one menu.

Browser cookies do not authenticate the JSON API, reveal private library data,
or replace a developer token. cliamp has no generic "logged in" badge for
cookie playback. It reads cookies only when yt-dlp starts a show.

## Access token and Listen Later

A developer OAuth access token enables `/me/` account resolution and the
read-only **Listen Later** view:

```toml
[mixcloud]
enabled = true
access_token = "${MIXCLOUD_ACCESS_TOKEN}"
```

Create an application and get a token through the Mixcloud browser OAuth flow.
See the [official API documentation](https://www.mixcloud.com/developers/).
cliamp does not request or store the application client secret. Treat the access
token as a secret. Environment interpolation keeps it out of `config.toml`.

cliamp uses the access token only for API metadata. It never adds it to playable
show URLs or passes it to yt-dlp. The Mixcloud API has write endpoints for
following, favoriting, reposting, and Listen Later. cliamp does not call them.

## Signed-in playback

Public shows normally play without authentication. To give yt-dlp the same
Mixcloud session as the browser for subscriber-only or other gated content,
configure a browser cookie source:

```toml
[mixcloud]
enabled = true
username = "your-mixcloud-username"
cookies_from = "firefox" # brave, chrome, chromium, edge, firefox, opera, safari, vivaldi, whale
```

cliamp passes `cookies_from` to the yt-dlp `--cookies-from-browser` option. The
installed yt-dlp version can also support custom profiles such as
`chrome:Profile 1` or `firefox:default-release`. Run `yt-dlp --help` and inspect
`--cookies-from-browser` for the current list.

yt-dlp does not currently expose Arc as a separate cookie source. Select a
supported browser where you are signed in to Mixcloud. On macOS, an
`Operation not permitted` error while reading `~/Library` means that the terminal
app that starts cliamp needs Full Disk Access. Restart the terminal after you
grant it.

cliamp selects yt-dlp cookies by service URL. Mixcloud can use a different
browser or profile from SoundCloud, NetEase, or YouTube. One provider does not
override another provider session.

Shows that Mixcloud flags as exclusive have an `[E]` suffix in the cliamp UI.
This presentation marker is not written to exported playlists, IPC output, or
Now Playing metadata. It is a warning, not an automatic skip. A signed-in user
may have access through a subscription. Another account gets the Mixcloud
restricted-show error when playback starts. cliamp cannot determine access from
public metadata, so it does not filter these rows.

## Direct URLs and playback metadata

Direct show URLs work anywhere cliamp accepts a URL, even when the provider is
not enabled:

```sh
cliamp https://www.mixcloud.com/creator/show-name/
```

In the TUI, press `u` and paste the same URL. Provider-generated queue entries
also store stable `https://www.mixcloud.com/<creator>/<show>/` page URLs. yt-dlp
resolves the current media URL only when playback starts. An idle or long queue
does not retain expired CDN URLs.

Mixcloud tracks contain the show title, creator, duration, publication year,
category tags, and the best image URL that the API returns. cliamp uses these
fields in track rows, the metadata view, IPC, and desktop media integration.

## Resume and seeking

When cliamp exits cleanly during a finite Mixcloud show, it saves the current
position. When cliamp next plays that show, it restarts the yt-dlp and FFmpeg
pipeline at the saved position. This also applies to shows first opened in the
provider browser. cliamp does not record live shows or failed or unstarted
playback.

Seek and resume can take several seconds. They start a replacement pipeline
instead of moving an in-memory decoder. cliamp combines several rapid seek
presses before it restarts.

The Mixcloud website and official widget limit fast-forward and rewind for some
accounts. cliamp does not call the widget seek operation. It restarts the same
playable media and asks FFmpeg to discard input before the target timestamp. The
website seek allowance is not available to cliamp. A large jump can take longer
while the replacement pipeline catches up.
See [Mixcloud's playback-limits article](https://help.mixcloud.com/hc/en-us/articles/360004054059-Why-are-there-limits-for-rewinds-and-fast-forwards)
and the [`seek` result in its Widget API](https://www.mixcloud.com/developers/widget/).

If Mixcloud, yt-dlp, or FFmpeg cannot create the replacement, cliamp restores
the existing pipeline and continues at the previous position. It does not leave
the player silent.

## Configuration reference

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `false` | Registers the Mixcloud provider |
| `username` | empty | Public profile URL username for account views when no token is set |
| `access_token` | empty | Developer OAuth token for `/me/` and Listen Later |
| `cookies_from` | empty | Browser/profile sent to yt-dlp for signed-in playback |
| `styles` | cliamp default style list | Local genre favorites. Each produces Latest and Popular provider rows and show sort modes. |
| `max_items` | `100` | Maximum shows, creators, or collections per view. Setup accepts 1 to 500. Manual values at or below zero use the default. Values above 500 use 500. |
| `stream_creators` | `20` | Maximum followed creators for the following stream. Setup accepts 1 to 100. Manual values at or below zero use the default. Values above 100 use 100. |

Use this complete example:

```toml
provider = "mixcloud"

[mixcloud]
enabled = true
username = "your-mixcloud-username"
access_token = "${MIXCLOUD_ACCESS_TOKEN}"
cookies_from = "firefox"
styles = ["chillout", "deep-house", "drum-bass", "electronica", "house", "techno"]
max_items = 100
stream_creators = 20
```

## Limits and tuning

The API does not expose the exact personalized Mixcloud website home feed.
**Stream (Following Releases)** is an approximation. It gets the first
`stream_creators` followed accounts, merges their newest uploads, sorts by
publication time, removes duplicates, and returns at most `max_items` shows.
Lower these values if you encounter API rate limits. cliamp includes the
Mixcloud `Retry-After` value in the error it shows.

Mixcloud does not provide audio streams through its JSON API. The official
third-party playback method is the visible web widget. Terminal playback depends
on the yt-dlp Mixcloud extractor. It is not a Mixcloud-supported API and can
break when the website changes. Keep yt-dlp current. Use this integration only
where Mixcloud terms and content rights permit it.

The cliamp provider contract has no generic UI for Mixcloud write actions. You
cannot follow, favorite a show, repost, change Listen Later, upload, comment,
or edit collections. Mixcloud Live is not available. Show search uses Mixcloud
cloudcast search. Creator discovery uses followed creators and selected-show
jumps. Genre discovery uses the category and tag APIs.
