# Yandex Music Integration

Enable Yandex Music to browse your liked tracks and personal playlists, listen to the personal "Моя волна" radio, search the catalog, and play tracks through direct signed CDN URLs resolved fresh at play time. Playback does not require `yt-dlp` or `ffmpeg` for MP3 streams.

## Quick Start

Get a personal OAuth token. Open this URL in a browser and authorize the official Yandex Music client:

```
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
| `M` | Open the Yandex Music provider |
| `Ctrl+F` | Search the Yandex Music catalog while the provider is active |
| `Enter` | Load the highlighted playlist or play the highlighted track |
| `Ctrl+R` | Refresh: reload the current playlist/wave in place, or return to the playlists pane |

The provider pane shows three sections: **My Music** (Liked Tracks and Моя волна), **My Playlists**, and **Saved Playlists** (playlists owned by other accounts that you follow).

**Моя волна** (My Wave) starts a personal radio session and loads about fifteen tracks. Playback is reported back to the session, so future batches adapt to what you actually listen to. Press `Ctrl+R` while the wave is open to drop the session and start a fresh batch in place.

Track stream URLs are resolved at play time, so playlists load instantly and links never expire while sitting in the queue.

## Limits

- Without a Yandex Music Plus subscription the service returns low-bitrate previews instead of full-quality streams.
- Track reports use the `/play-audio` endpoint and wave sessions receive rotor feedback, so listening counts toward your Yandex Music statistics and influences recommendations.
