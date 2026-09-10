# Qobuz Integration

cliamp can stream your [Qobuz](https://www.qobuz.com/) library directly through its audio pipeline. EQ, visualizer, and all effects apply. Requires an active Qobuz subscription.

Qobuz delivers lossless FLAC, so cliamp streams it through the same buffer-while-playing + ffmpeg pipeline used for other lossless providers. `ffmpeg` must be on `PATH`.

## Setup

The fastest path is the interactive wizard: run `cliamp setup`, pick **Qobuz**, choose a stream quality, and it writes the `[qobuz]` block for you.

Or configure it manually in `~/.config/cliamp/config.toml`:

```toml
[qobuz]
enabled = true
quality = 6
```

No developer credentials are needed. The `app_id`, signing secrets, and OAuth private key are scraped automatically from the Qobuz web player.

If the private key can no longer be scraped (Qobuz changed their bundle format), set it manually as a fallback — extract the current value from the web player's `bundle.js` at `play.qobuz.com`:

```toml
[qobuz]
private_key = "..."
```

The scraped value always takes precedence; the config key is only consulted when scraping fails.

Run `cliamp`, select Qobuz as a provider, and press `Enter` to sign in. A browser window opens for Qobuz's OAuth login. Once you authorize, credentials are cached at `~/.config/cliamp/qobuz_credentials.json` and subsequent launches refresh silently.

> **Click "Back" to finish.** After you authorize, Qobuz shows a *"You are signed in, you can leave this page"* screen with a **Back** button rather than redirecting automatically. Click that **Back** button. It fires the redirect that hands the sign-in code to cliamp and completes authentication. cliamp waits (up to 5 minutes) for it.

### Quality

`quality` selects the Qobuz `format_id`. If omitted, cliamp uses `6` (FLAC CD). Supported values:

| Value | Format |
|---|---|
| `5`  | MP3 320 kbps |
| `6`  | FLAC 16-bit / 44.1 kHz (CD) |
| `7`  | FLAC 24-bit up to 96 kHz (Hi-Res) |
| `27` | FLAC 24-bit up to 192 kHz (Hi-Res) |

Hi-Res tiers require a Qobuz plan that includes them. Any other value falls back to `6`.

## Usage

Start directly on Qobuz:

```sh
cliamp --provider qobuz
```

Once authenticated, Qobuz appears as a provider alongside the others. Press `Q` to jump straight to Qobuz, or `Esc`/`b` to open the provider browser and select it.

The provider surfaces your Qobuz library:

- **Favorite Tracks**: your liked songs.
- **Random Tracks**: a random sample of up to 500 tracks drawn from across all your playlists, with duplicates removed. Press `Ctrl+R` to reshuffle the sample.
- **Your playlists**: playlists you created or subscribed to.
- **Favorite albums**: browsable in the album view.
- **Favorite artists**: browse an artist to see their albums.

Press `Ctrl+F` while Qobuz is active to search the Qobuz catalog for tracks.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate |
| `Enter` | Load the selected playlist/album or play the selected track |
| `Ctrl+F` | Search Qobuz tracks |
| `Ctrl+R` | Refresh (re-resolves stream URLs) |
| `Tab` | Switch between provider and playlist focus |
| `Esc` / `b` | Open provider browser |

After loading a playlist or album you return to the standard playlist view with all the usual controls (seek, volume, EQ, shuffle, repeat, queue, search, lyrics).

## Troubleshooting

- **"OAuth failed" / browser doesn't open**: cliamp opens a localhost redirect listener on a random port. Make sure nothing is blocking outbound access to `qobuz.com` and that a default browser is configured. The flow times out after 5 minutes.
- **"OAuth private key not found in the web-player bundle"**: Qobuz changed their bundle format and the private key could not be scraped. Extract the current value from the web player's `bundle.js` at `play.qobuz.com`, set it as `[qobuz] private_key` (see [Setup](#setup)), and retry sign-in.
- **Sign-in seems to hang / "you can leave this page"**: after authorizing, the Qobuz OAuth page shows a confirmation screen with a **Back** button instead of redirecting automatically. Click **Back** to complete sign-in. cliamp keeps waiting (up to 5 minutes) until the redirect arrives.
- **Re-authenticate**: run `cliamp qobuz reset` to clear stored credentials, then relaunch cliamp and select Qobuz to sign in again. (Equivalent to deleting `~/.config/cliamp/qobuz_credentials.json` manually.)
- **Track is unplayable / skipped**: the track may not be streamable on your subscription tier or in your region. cliamp marks such tracks unplayable and moves on.
- **Hi-Res not delivered**: setting `quality = 27` does not upgrade a tier that lacks Hi-Res. Qobuz returns the best your plan allows.
- **Stalls after a long idle session**: signed stream URLs expire over time. Press `Ctrl+R` to refresh, which re-resolves the URLs.

## Requirements

- An active Qobuz subscription
- `ffmpeg` on `PATH` for FLAC decoding
- No developer/API registration: credentials are obtained automatically
