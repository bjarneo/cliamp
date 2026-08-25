# Mixcloud Integration

cliamp supports [Mixcloud](https://www.mixcloud.com) as an opt-in provider. It
uses Mixcloud's public JSON API for catalog metadata and the existing
`yt-dlp`/FFmpeg pipeline for playback, so `yt-dlp` and `ffmpeg` must be on
`PATH`.

## Feature summary

| Feature | Requirement | Where to use it |
|---|---|---|
| Direct Mixcloud show URL playback | `yt-dlp`; the provider does not need to be enabled | Pass the URL to `cliamp` or press `u` |
| Recent releases, popular shows, show browsing and show search | `[mixcloud] enabled = true` | Provider pane, `N`, or `Ctrl+F` |
| Live category catalogue and Latest/Popular genre charts | Provider enabled | **Genres** in the provider pane or `N` browser |
| Genre/tag search and local genre favorites | Provider enabled and a writable config file | `/`, `Enter`, and `f` in **Genres** |
| Public profile activity, uploads, show favorites, listening history and collections | Public profile `username`, or an `access_token` | **Your Mixcloud** and **Collections** sections |
| Following stream and followed-creator browser | Public profile `username`, or an `access_token` | **Stream (Following Releases)** and **Creators** |
| Jump from a highlighted show to that creator's Uploads/Favorites | Provider enabled; no configured account is required for the jump | Press `N` on a Mixcloud show |
| Listen Later | Developer OAuth `access_token` | **Your Mixcloud** section |
| Signed-in or subscriber-gated playback | `cookies_from` for a supported browser containing the Mixcloud session | Playback through yt-dlp |
| Exclusive-show warning | Provider enabled | An `[E]` suffix on show rows |
| Resume and seek-by-restart for finite shows | A successfully playable finite Mixcloud show | Normal cliamp seek keys and clean-exit resume |

The provider does not implement Mixcloud write actions such as following a
creator, favoriting or reposting a show, editing a collection, or uploading.
The **Favorites** lists are therefore read-only. Genre favorites are a separate,
local cliamp feature described below.

## Setup

Run the setup wizard and select **Mixcloud**:

```sh
cliamp setup
```

For public discovery, no account is required. The smallest manual
configuration is:

```toml
[mixcloud]
enabled = true
```

Add your public username to expose account-based views:

```toml
[mixcloud]
enabled = true
username = "your-mixcloud-username"
```

Use the username segment from your public profile URL, not an email address or
an unrelated display label. Preserve its spelling when copying it from
`https://www.mixcloud.com/<username>/`.

To open Mixcloud at startup, also set the top-level provider option:

```toml
provider = "mixcloud"
```

## Provider pane

Press `X` to switch directly to Mixcloud. The provider pane is grouped into the
following sections.

| Section | Entry | Behaviour |
|---|---|---|
| **Your Mixcloud** | **Stream (Following Releases)** | Merges recent uploads from followed creators; see [Limits and tuning](#limits-and-tuning) |
| **Your Mixcloud** | **Favorites** | Reads the configured public account, or the token owner's `/me/` connections |
| **Your Mixcloud** | **Creators** | Lists the configured account and followed creators, then separates each creator's **Uploads** and **Favorites** |
| **Your Mixcloud** | **Uploads**, **Profile Activity**, **Listening History** | Reads the configured public account, or the token owner's `/me/` connections |
| **Your Mixcloud** | **Listen Later** | Appears only when `access_token` is set |
| **Browse** | **Shows** | Opens the global show catalogue with Recent Releases, Popular, and every configured style as sort modes |
| **Browse** | **Genres** | Loads Mixcloud's current category catalogue, displays local favorite state, and opens Latest or Popular shows for a category |
| **Collections** | One row per collection | Reads every collection returned for the account and shows its API track count |
| **Discover** | **Recent Releases**, **Popular** | Global public discovery; no account required |
| **Music Styles** | `<Style> — Latest`, `<Style> — Popular` | One pair for every entry in `[mixcloud].styles` |

For configured accounts, **Your Mixcloud** is the first section and prioritizes
**Stream** followed by **Favorites**, then **Creators** immediately below them.
Public-only and degraded-account views begin with **Browse** and omit
**Creators** from the provider pane. The hierarchy rows are shortcuts: backing
out of their top-level Shows, Creators, or Genres list returns directly to this
provider pane.

Selecting an individual **Show**, a creator's **Uploads** or **Favorites**, or
a genre's **Latest** or **Popular** view loads those results into the main
playlist and closes the provider browser. Empty results leave the existing
playlist and browser intact and show a warning.

`Ctrl+R` reloads the provider lists and clears the cached `/me/` identity. Show,
category, collection and account results are otherwise fetched from Mixcloud
when opened rather than retained as an expiring audio-URL cache.

If account resolution or collection loading fails—for example because of a
mistyped username, expired token, or API rate limit—cliamp shows a warning and
temporarily omits **Your Mixcloud** and **Collections**. Public **Browse**,
**Discover**, and **Music Styles** entries remain available, so an account-side
problem does not make the entire provider appear broken.

## Hierarchical browsing and controls

Pressing `N` with no recognized Mixcloud show selected opens the normal provider
browser. Mixcloud exposes four routes:

- **By Show** browses the global show catalogue. Press `s` to cycle Recent
  Releases, Popular, and the Latest/Popular view for each configured style.
- **By Creator** selects a creator and combines that creator's Uploads and
  Favorites into a track list.
- **By Creator / Show** selects a creator, then loads an explicit **Uploads**
  or **Favorites** collection into the main playlist.
- **Genres** browses and searches categories before loading Latest or Popular
  into the main playlist.

The creator list contains the configured account and followed creators. Both
creator routes require `username` or `access_token`; **By Show** and **Genres**
are public. Breadcrumbs use the Mixcloud terms **Creator** and **Show** so the
current route remains visible.

When a Mixcloud show is highlighted in the main playlist, `N` changes to
**Browse creator** and jumps straight to that show's creator and their separate
Uploads/Favorites choices. This works even if that creator is not followed.
`Esc` returns to the normal browse menu from this shortcut; press `Esc` again to
return to the originating playlist. Browsing opened through **Shows**,
**Creators**, **Genres**, or the normal `N` menu uses one-level-at-a-time back
navigation.

| Key | Mixcloud action |
|---|---|
| `X` | Open the Mixcloud provider |
| `N` | Open the browse menu, or jump from the highlighted Mixcloud show to its creator |
| `Ctrl+F` | Search Mixcloud shows from the provider pane or provider browser |
| `/` | Filter the currently visible creator, show, genre, sort, or track list |
| `Enter` while filtering **Genres** | Replace the local category filter with a server-side Mixcloud tag search |
| `f` in the **Genres** list | Add or remove the highlighted genre from local Mixcloud favorites |
| `s` in the global show list | Cycle Recent, Popular and configured-style show views |
| `Enter` / `→` / `l` | Drill in; on a track row, play it and queue the remaining visible rows |
| `a` | Append all visible browser tracks to the queue |
| `q` on a browser track | Queue the highlighted show to play next |
| `R` on browser tracks | Replace the queue with all visible shows, with confirmation when needed |
| `Esc` / `←` / `h` / `b` | Walk back one level or close the browser |
| `Ctrl+R` in the provider pane | Refresh Mixcloud lists and account identity |
| `u` | Paste a Mixcloud show URL |
| `Ctrl+S` | Save/download the currently playing show through cliamp's normal track-save flow |

Search and browse result lists retain cliamp's normal filtering, append, play,
queue-next and replace-queue behaviour.

## Genre browsing and favorites

**Genres** is loaded from Mixcloud's current public category catalogue. Each
row shows `★` when the genre is in `[mixcloud].styles` and `☆` when it is not.
Category groups returned by Mixcloud, such as Music or Talk, are displayed next
to the category name.

Typing after `/` immediately filters the loaded catalogue by category name or
group. Pressing `Enter` submits the same text to Mixcloud's tag search, which
can find categories beyond the initial catalogue. Selecting any category or
search result offers **Latest** and **Popular** show lists.

Press `f` on a genre to toggle its star. cliamp then:

1. atomically rewrites only `styles` in the `[mixcloud]` config section;
2. preserves the other Mixcloud settings, comments and unrelated sections;
3. updates the in-memory favorite state; and
4. refreshes the **Music Styles** provider rows, adding or removing that
   genre's Latest/Popular pair.

No access token is required because this is a local cliamp preference. It does
not favorite anything on the Mixcloud website. A failed config write leaves the
previous UI and config state unchanged.

The default styles are ambient, chillout, deep house, disco, drum and bass,
electronica, funk, hip-hop, house, jazz, reggae, soul, techno, trance, and
world. Override them with Mixcloud genre slugs:

```toml
[mixcloud]
enabled = true
styles = ["ambient", "balearic", "deep-house", "jazz"]
```

Omit `styles` to use the defaults. Set `styles = []` to start with no favorited
genres and no **Music Styles** rows; the live **Genres** catalogue remains
available, so styles can be added again with `f`. Values are normalized to
lowercase hyphenated slugs and duplicates are removed.

### Favorite terminology

Mixcloud exposes several similarly named concepts:

- **Your Mixcloud / Favorites** reads shows favorited by the configured account.
- **Creator / Favorites** reads a selected creator's public show favorites.
- `f` on a show in cliamp's main playlist toggles a local cliamp track bookmark.
- `f` inside **Genres** toggles a local Mixcloud style in `[mixcloud].styles`.

None of these controls writes a show favorite back to Mixcloud.

## Account identity and authentication

Mixcloud permits public API reads without authentication. cliamp supports four
separate levels of configuration:

| Configuration | Purpose |
|---|---|
| `enabled = true` | Public discovery, shows, genres, genre/tag search and show search |
| `username` | Public account connections: following, activity, uploads, show favorites, listening history and collections |
| `access_token` | Resolves the authorized user through `/me/` and adds Listen Later |
| `cookies_from` | Gives yt-dlp a signed-in browser session for playback only |

If both `username` and `access_token` are configured, the access-token owner's
`/me/` identity is the source of truth for all account views. This prevents a
stale or different public username from mixing two accounts in one menu.

Browser cookies do not authenticate the JSON API, reveal private library data,
or replace a developer token. Consequently, cliamp has no generic "logged in"
badge for cookie playback: the cookies are read only when yt-dlp starts a show.

## Access token and Listen Later

A developer OAuth access token enables `/me/` account resolution and the
read-only **Listen Later** view:

```toml
[mixcloud]
enabled = true
access_token = "${MIXCLOUD_ACCESS_TOKEN}"
```

Create an application and obtain a token through Mixcloud's browser-based
OAuth flow as described in the [official API documentation](https://www.mixcloud.com/developers/).
cliamp does not ask for or store the application client secret. Treat the
access token as a secret; environment interpolation keeps it out of
`config.toml`.

The access token is used for API metadata only. It is never appended to
playable show URLs or passed to yt-dlp. Although Mixcloud's API offers write
endpoints for following, favoriting, reposting and Listen Later, cliamp does not
call them.

## Signed-in playback

Public shows normally play without authentication. To give yt-dlp the same
Mixcloud session as your browser—for subscriber-only or otherwise gated
content—configure a browser cookie source:

```toml
[mixcloud]
enabled = true
username = "your-mixcloud-username"
cookies_from = "firefox" # brave, chrome, chromium, edge, firefox, opera, safari, vivaldi, whale
```

`cookies_from` is passed to yt-dlp's `--cookies-from-browser` option. Custom
profiles such as `chrome:Profile 1` or `firefox:default-release` are also
accepted when supported by the installed yt-dlp version. Run `yt-dlp --help`
and inspect `--cookies-from-browser` for its current list.

Arc is not currently exposed as a separate yt-dlp cookie source. Choose a
supported browser where you are also signed in to Mixcloud. On macOS, an
`Operation not permitted` error while reading `~/Library` means the terminal
app launching cliamp needs Full Disk Access; restart that terminal after
granting it.

cliamp selects yt-dlp cookies by service URL. Mixcloud can therefore use a
different browser or profile from SoundCloud, NetEase, or YouTube without one
provider overriding another's session.

Shows that Mixcloud flags as exclusive display an `[E]` suffix in cliamp's UI.
The marker is presentation-only, so it is not written into exported
playlists, IPC output, or Now Playing metadata. It is a warning rather than an
automatic skip: a signed-in listener may be entitled through a subscription,
while another account will receive Mixcloud's restricted-show error when
playback begins. cliamp cannot determine entitlement from the public metadata,
so it does not filter these rows out.

## Direct URLs and playback metadata

Direct show URLs work wherever cliamp accepts a URL, even when the provider is
not enabled:

```sh
cliamp https://www.mixcloud.com/creator/show-name/
```

Inside the TUI, press `u` and paste the same URL. Provider-generated queue
entries also store stable `https://www.mixcloud.com/<creator>/<show>/` page
URLs. yt-dlp resolves the current media URL only when playback starts, so an
idle or long queue does not retain expired CDN URLs.

Mixcloud tracks carry the show title, creator, duration, publication year,
category tags and the best image URL returned by the API. cliamp uses the
available fields in its normal track rows, metadata view, IPC and desktop media
integration.

## Resume and seeking

When cliamp exits cleanly during a finite Mixcloud show, it saves the current
position. The next time that exact show is played—even when cliamp originally
opened it in the provider browser—it restarts the yt-dlp/FFmpeg pipeline at the
saved position. Live shows and failed/unstarted playback are not recorded.

Seeking and resume can take a few seconds because they start a replacement
pipeline rather than moving an in-memory decoder. Multiple rapid seek presses
are combined before the restart.

Mixcloud's website and official widget limit fast-forwards and rewinds for some
listener accounts. cliamp does not call that widget seek operation: it restarts
the same playable media and asks FFmpeg to discard input up to the target
timestamp. The website's seek allowance is therefore not available to cliamp,
and a large jump may take longer while the replacement pipeline catches up.
See [Mixcloud's playback-limits article](https://help.mixcloud.com/hc/en-us/articles/360004054059-Why-are-there-limits-for-rewinds-and-fast-forwards)
and the [`seek` result in its Widget API](https://www.mixcloud.com/developers/widget/).

If Mixcloud, yt-dlp, or FFmpeg cannot build the replacement, cliamp restores
the existing pipeline and continues from its previous position instead of
leaving the player silent.

## Configuration reference

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `false` | Registers the Mixcloud provider |
| `username` | empty | Public profile URL username used for account views when no token is set |
| `access_token` | empty | Developer OAuth token used for `/me/` and Listen Later |
| `cookies_from` | empty | Browser/profile passed to yt-dlp for signed-in playback |
| `styles` | cliamp's default style list | Local genre favorites; each produces Latest and Popular provider rows and show sort modes |
| `max_items` | `100` | Maximum shows, creators or collections returned per view; setup accepts 1–500, manual values at or below zero use the default, and larger values are capped at 500 |
| `stream_creators` | `20` | Maximum followed creators sampled for the following stream; setup accepts 1–100, manual values at or below zero use the default, and larger values are capped at 100 |

A complete example is:

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

The API does not expose Mixcloud's exact personalized website home feed.
**Stream (Following Releases)** is therefore a transparent approximation: it
fetches the first `stream_creators` followed accounts, merges their newest
uploads, sorts by publication time, removes duplicates, and returns at most
`max_items` shows. Lower the values if you encounter API rate limits; cliamp
preserves Mixcloud's `Retry-After` value in the error shown to the user.

Mixcloud explicitly does not provide audio streams through its JSON API. The
official playback mechanism for third-party sites is the visible web widget.
Terminal playback therefore depends on yt-dlp's Mixcloud extractor, which is
not a Mixcloud-supported API and can break when the website changes. Keep
yt-dlp current and use this integration only where permitted by Mixcloud's
terms and the rights attached to the content.

The current cliamp provider contract has no generic UI for Mixcloud write
actions such as follow, show favorite, repost, Listen Later mutation, upload,
comment, or collection editing. Mixcloud Live is not exposed. Show search uses
Mixcloud's cloudcast search; creator discovery comes from followed creators and
selected-show jumps, while genre discovery uses the category and tag APIs.
