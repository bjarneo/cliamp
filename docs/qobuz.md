# Qobuz Integration

Use cliamp to stream your [Qobuz](https://www.qobuz.com/) library through its audio pipeline. EQ, the visualizer, and other effects apply. You need an active Qobuz subscription.

Qobuz delivers lossless FLAC. cliamp uses the same buffer-while-playing and `ffmpeg` pipeline as other lossless providers. Put `ffmpeg` on `PATH`.

## Setup

Run `cliamp setup` for the fastest setup. Select **Qobuz**, select a stream quality, and let the wizard write the `[qobuz]` block.

Or configure Qobuz in `~/.config/cliamp/config.toml`:

```toml
[qobuz]
enabled = true
quality = 6
```

You do not need developer credentials. cliamp gets the `app_id`, signing secrets, and OAuth private key from the Qobuz web player.

Run `cliamp`, select Qobuz, and press `Enter` to sign in. A browser window opens for Qobuz OAuth login. After authorization, cliamp stores credentials in `~/.config/cliamp/qobuz_credentials.json`. Later launches refresh them without a message.

> **Click "Back" to finish.** After authorization, Qobuz shows a *"You are signed in, you can leave this page"* screen with a **Back** button. It does not redirect automatically. Click **Back**. This sends the sign-in code to cliamp and completes authentication. cliamp waits for up to 5 minutes.

### Quality

`quality` selects the Qobuz `format_id`. If you omit it, cliamp uses `6` (FLAC CD). Supported values:

| Value | Format |
|---|---|
| `5`  | MP3 320 kbps |
| `6`  | FLAC 16-bit / 44.1 kHz (CD) |
| `7`  | FLAC 24-bit up to 96 kHz (Hi-Res) |
| `27` | FLAC 24-bit up to 192 kHz (Hi-Res) |

Hi-Res tiers need a Qobuz plan that includes them. Any other value uses `6`.

## Usage

Start cliamp with Qobuz selected:

```sh
cliamp --provider qobuz
```

After authentication, Qobuz appears in the provider list. Press `Q` to select Qobuz. Press `Esc`/`b` to open the provider browser and select it.

The provider shows your Qobuz library:

- **Favorite Tracks**: your liked songs.
- **Random Tracks**: A random sample of up to 500 tracks from all playlists, without duplicates. Press `Ctrl+R` to select a new sample.
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

After you load a playlist or album, cliamp returns to the standard playlist view. Use the usual controls for seek, volume, EQ, shuffle, repeat, queue, search, and lyrics.

## Troubleshooting

- **"OAuth failed" / browser doesn't open**: cliamp opens a localhost redirect listener on a random port. Ensure that nothing blocks outbound access to `qobuz.com` and that a default browser is set. The flow times out after 5 minutes.
- **Sign-in seems to hang / "you can leave this page"**: The Qobuz OAuth page shows a confirmation screen with a **Back** button after authorization. It does not redirect automatically. Click **Back** to complete sign-in. cliamp waits for the redirect for up to 5 minutes.
- **Re-authenticate**: Run `cliamp qobuz reset` to clear stored credentials. Then restart cliamp, select Qobuz, and sign in again. This is the same as deleting `~/.config/cliamp/qobuz_credentials.json`.
- **Track is unplayable / skipped**: The track may not be available for the subscription tier or region. cliamp marks the track unplayable and continues.
- **Hi-Res not delivered**: `quality = 27` does not add Hi-Res to a plan that lacks it. Qobuz returns the best quality allowed by the plan.
- **Stalls after a long idle session**: Signed stream URLs expire. Press `Ctrl+R` to refresh and resolve the URLs again.

## Requirements

- An active Qobuz subscription
- `ffmpeg` on `PATH` for FLAC decoding
- No developer/API registration: cliamp obtains credentials automatically
