# SoundCloud Integration

Enable [SoundCloud](https://soundcloud.com) to search, paste URLs to play, browse profiles, and stream subscriber-gated tracks with browser cookies. Playback uses [yt-dlp](https://github.com/yt-dlp/yt-dlp). Put `yt-dlp` on `PATH`.

> SoundCloud closed its OAuth program to new applications in 2014. You cannot use the Spotify-style bring-your-own-`client_id` pattern. cliamp uses the existing SoundCloud session in your browser. See [Sign in via browser cookies](#sign-in-via-browser-cookies).

## Enable

SoundCloud is **off by default**. To enable it, add this to `~/.config/cliamp/config.toml`:

```toml
[soundcloud]
enabled = true
```

After you enable it:

- **Search** with `Ctrl+F` while SoundCloud is active. cliamp runs `scsearch:` against the SoundCloud public index.
- **Paste a URL** (`u`). Any `soundcloud.com/<artist>/<track>` URL plays.
- **Browse list with curated genres**. Without a configured profile, the playlists pane lists **Trending**, **Hip-Hop**, **Electronic**, **House**, **Lo-Fi**, **Indie**, and **Pop**. These are virtual playlists backed by real-time scsearch results, not editorial charts. SoundCloud official chart endpoints currently return 404 through yt-dlp.

## Browse a profile

Set a username to show the profile content in the browse pane:

```toml
[soundcloud]
enabled = true
user = "yourname"
```

This replaces the curated Browse list with three playlists from `soundcloud.com/yourname`:

- **Tracks**: All tracks the user uploaded.
- **Likes**: Tracks the user liked.
- **Reposts**: Tracks the user reposted.

This works for any public profile. It does not require SoundCloud sign-in.

## Sign in via browser cookies

For private likes, hidden uploads, or SoundCloud Go+ subscriber-gated tracks, point yt-dlp to the browser cookie jar:

```toml
[soundcloud]
enabled = true
user = "yourname"
cookies_from = "firefox"   # also: chrome, chromium, brave, edge, opera, safari, vivaldi
```

cliamp passes `--cookies-from-browser <name>` to every yt-dlp command for search, browse, and playback. Sign in to SoundCloud in that browser. You do not need to keep it open. yt-dlp then uses the account access permissions.

This is the same mechanism as `[ytmusic] cookies_from`. cliamp selects cookie
sources by service. SoundCloud and YouTube can use different browsers or
profiles in one cliamp process.

## CLI

```sh
cliamp https://soundcloud.com/forss/flickermood   # play a track
cliamp https://soundcloud.com/forss/sets/album    # play a set / playlist
cliamp https://soundcloud.com/forss               # play a profile's tracks
cliamp --provider soundcloud                      # start with SoundCloud as the active provider
cliamp search-sc "lofi beats"                     # legacy: SoundCloud search from the shell
```

URL playback works whether or not `[soundcloud]` is enabled. yt-dlp resolves any SoundCloud link passed by cliamp. The `enabled` flag controls only the in-app provider entry.

## When playback fails

Some tracks return 404 from the SoundCloud per-track format API even when the page and search index still list them. Common causes are Go+ subscriber-gated content, region-blocked streams, deleted cached entries, and temporary yt-dlp extractor errors. cliamp shows the yt-dlp exit message and the status notification *"Couldn't play X — track is gated, restricted, or unavailable."* This indicates an upstream issue.

If this occurs for an expected track, set `cookies_from` and confirm SoundCloud sign-in in that browser.

## Requirements

- [yt-dlp](https://github.com/yt-dlp/yt-dlp) on `PATH`
- Optional: a browser with an active SoundCloud session, for `cookies_from`
