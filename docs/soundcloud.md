# SoundCloud Integration

cliamp ships with [SoundCloud](https://soundcloud.com) as a first-class provider. Search, paste-to-play, browse a profile, and (with a browser cookie hookup) stream subscriber-gated tracks. Powered by [yt-dlp](https://github.com/yt-dlp/yt-dlp), so it requires `yt-dlp` on `PATH`.

> SoundCloud closed its OAuth program to new applications in 2014, so the bring-your-own-`client_id` pattern Spotify uses isn't available. cliamp signs you in by reusing your browser's existing SoundCloud session — see [Sign in via browser cookies](#sign-in-via-browser-cookies) below.

## What works out of the box

No configuration needed:

- **Search** with `Ctrl+F` while SoundCloud is the active provider — runs `scsearch:` against SoundCloud's public index.
- **Paste a URL** (`u`) — any `soundcloud.com/<artist>/<track>` URL plays.
- **Browse list with curated genres** — when no profile is configured, the playlists pane is seeded with **Trending**, **Hip-Hop**, **Electronic**, **House**, **Lo-Fi**, **Indie**, and **Pop**. These are search-backed virtual playlists (real-time scsearch results), not editorial charts — SoundCloud's official chart endpoints all 404 through yt-dlp at present.

## Browse a profile

Set a username to expose that profile's content in the browse pane:

```toml
[soundcloud]
user = "yourname"
```

This replaces the curated Browse list with three playlists for `soundcloud.com/yourname`:

- **Tracks** — everything the user has uploaded
- **Likes** — tracks they've liked
- **Reposts** — tracks they've reposted

Works for any public profile. No SoundCloud sign-in required at this level.

## Sign in via browser cookies

For private likes, hidden uploads, or SoundCloud Go+ subscriber-gated tracks, point yt-dlp at your browser's cookie jar:

```toml
[soundcloud]
user = "yourname"
cookies_from = "firefox"   # also: chrome, chromium, brave, edge, opera, safari, vivaldi
```

cliamp passes `--cookies-from-browser <name>` to every yt-dlp invocation — search, browse, and playback. As long as you're signed into SoundCloud in that browser (no need to keep it open), yt-dlp acts as logged-in-you and can access content your account is authorized for.

This is the same mechanism `[ytmusic] cookies_from` uses. If you set both, the last one to initialize wins for the playback path; in practice users have one default browser they're signed into multiple sites with, so this is fine.

## CLI

```sh
cliamp https://soundcloud.com/forss/flickermood   # play a track
cliamp https://soundcloud.com/forss/sets/album    # play a set / playlist
cliamp https://soundcloud.com/forss               # play a profile's tracks
cliamp --provider soundcloud                      # start with SoundCloud as the active provider
cliamp search-sc "lofi beats"                     # legacy: SoundCloud search from the shell
```

## Disable

```toml
[soundcloud]
enabled = false
```

The provider won't be registered and won't appear in the provider list.

## When playback fails

Some tracks 404 on SoundCloud's per-track format API even though the page and search index still show them. Common causes: subscriber-gated content (Go+), region-blocked streams, deleted-but-cached entries, or transient yt-dlp extractor glitches. cliamp surfaces yt-dlp's exit message and shows a status notification — *"Couldn't play X — track is gated, restricted, or unavailable."* — so you know it's an upstream issue rather than a cliamp bug.

If you hit this on tracks you expect to play, set `cookies_from` (above) and confirm you're signed into SoundCloud in that browser.

## Requirements

- [yt-dlp](https://github.com/yt-dlp/yt-dlp) on `PATH`
- Optional: a browser with an active SoundCloud session, for `cookies_from`
