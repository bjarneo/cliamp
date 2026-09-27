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
| `Ctrl+S` | Download the current playback track to the configured downloads directory |
| `Ctrl+R` | Refresh: reload the current playlist/wave in place, or return to the playlists pane |

The provider pane shows three sections: **My Music** (Liked Tracks and Моя волна), **My Playlists**, and **Saved Playlists** (playlists owned by other accounts that you follow).

**Моя волна** (My Wave) starts a personal radio session and loads about fifteen tracks. Playback is reported back to the session, so future batches adapt to what you actually listen to. Press `Ctrl+R` while the wave is open to drop the session and start a fresh batch in place.

Track stream URLs are resolved at play time, so playlists load instantly and links never expire while sitting in the queue.

## Limits

- Without a Yandex Music Plus subscription the service returns low-bitrate previews instead of full-quality streams.
- Track reports use the `/play-audio` endpoint and wave sessions receive rotor feedback, so listening counts toward your Yandex Music statistics and influences recommendations.

## Offline files

While a Yandex track is playing in the TUI, press `Ctrl+S`. The footer shows
`Downloading...`, then `Saved to <path>` or an error. Playback continues normally.
This saves the current playback track, regardless of the highlighted row or active
provider pane. Providers without download support show a message.

The default directory is `~/Music/cliamp`. To use an external drive, add this to
`~/.config/cliamp/config.toml` and restart cliamp:

```toml
[downloads]
directory = "/media/usb/CLAPt/Music"
```

Use an absolute path (a literal `~` is not expanded). Mount the external drive
before saving; cliamp creates missing directories and does not detect mounts.
This setting also applies to existing TUI track saves.

Downloads reuse the playback URL resolver and the stream quality selected by the
service. Previews remain previews; unavailable tracks are rejected. No DRM or
service restriction is bypassed, and no token or signed URL is saved in metadata.
The extension follows the stream codec. MP3 plays directly; AAC needs FFmpeg.

Files have sanitized artist/title names and a random suffix, so repeat downloads
create separate files. Data streams to a `.part` in the destination directory;
only a completed, flushed and closed file is renamed to its audio extension.
Failed or cancelled transfers remove the temporary file. A forced process exit
or power loss can leave an unfinished `.part`, which may be deleted manually.

Only one provider download from `Ctrl+S` runs at a time. Provider downloads have a 30-minute timeout
and receive cancellation when quitting the TUI. There is no resume, batch mode,
automatic offline substitution, metadata tagging or offline-library database.
The original provider track stays in the queue. This MVP targets the TUI;
headless daemon saving is unchanged.

To play offline, pass the saved path or directory to the existing local player:

```sh
cliamp "/media/usb/CLAPt/Music/track-Artist - Song-123456.mp3"
cliamp "/media/usb/CLAPt/Music"
```

The path in the completion message is the actual filename. No Yandex connection
is needed to play the local file.
