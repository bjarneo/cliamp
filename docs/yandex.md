# Yandex Music Integration

Enable Yandex Music to browse your liked tracks and personal playlists, listen to the personal "Моя волна" radio, search the catalog, and play tracks through direct signed CDN URLs resolved fresh at play time. Playback does not require `yt-dlp` or `ffmpeg` for MP3 streams.

## Quick Start

Get a personal OAuth token. Open this URL in a browser and authorize the official Yandex Music client:

```text
https://oauth.yandex.ru/authorize?response_type=token&client_id=23cabbbdc6cd418abb4b39c32c41195d
```

After authorization the token appears in the address bar in the `#access_token=` fragment. Copy it and add to `~/.config/cliamp/config.toml`:

```toml
[yandex]
enabled = true
token = "y0_YourPersonalOAuthToken"
```

Keep the token private. You can read it from an environment variable instead:

```toml
[yandex]
enabled = true
token = "$YANDEX_TOKEN"
```

## Usage

Start cliamp with Yandex Music selected:

```sh
cliamp --provider yandex
```

Inside the TUI:

| Key | Action |
|---|---|
| — | No in-TUI hotkey yet: start cliamp with `--provider yandex` or set `provider = "yandex"` in `config.toml` |
| `Ctrl+F` | Search the Yandex Music catalog while the provider is active |
| `Enter` | Load the highlighted playlist or play the highlighted track |
| `Ctrl+R` | Refresh: reload the current playlist/wave in place, or return to the playlists pane |

The provider pane shows three sections: **My Music** (Liked Tracks and Моя волна), **My Playlists**, and **Saved Playlists** (playlists owned by other accounts that you follow).

**Моя волна** (My Wave) starts a personal radio session with about fifteen
tracks. More tracks append automatically when the cursor reaches the last row
(using arrows, j/k, Page Down or End), or while the last loaded track plays.
If playback reaches the end before the request completes, it waits for the
new batch and continues automatically. The cursor and existing tracks stay in
place. This on-demand behavior applies to the wave, not liked tracks or ordinary
playlists.

Only one continuation request runs at a time. Duplicate track IDs are skipped.
A failed request keeps the existing queue and shows a status message; retries
are limited to once every five seconds. While the last track is still playing,
retry is automatic; after playback has stopped, press Next or move the cursor
to the end to retry. An empty batch ends continuation until the wave is reloaded.
Stop prevents a pending batch from restarting playback. Opening another list,
switching providers or refreshing discards stale results.

Playback feedback keeps each track's original batch ID, so later batches can
adapt to what you actually listen to. Repeat-one and explicit queued tracks keep
their usual priority; a sequential repeat-all wave requests new tracks before
wrapping. Press `Ctrl+R` while the wave is open to discard the session and start
a fresh batch in place.

Track stream URLs are resolved at play time, so playlists load instantly and links never expire while sitting in the queue.

## Limits

- Without a Yandex Music Plus subscription the service returns low-bitrate previews instead of full-quality streams.
- Track reports use the `/play-audio` endpoint and wave sessions receive rotor feedback, so listening counts toward your Yandex Music statistics and influences recommendations.
