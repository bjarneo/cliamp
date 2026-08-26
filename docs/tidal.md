# Tidal Integration

Use cliamp to stream your [Tidal](https://tidal.com/) library through its audio pipeline. EQ, the visualizer, and other effects apply. You need a paid Tidal subscription. Every paid plan includes lossless FLAC.

Tidal delivers lossless FLAC. cliamp uses the same buffer-while-playing and `ffmpeg` pipeline as other lossless providers. Put `ffmpeg` on `PATH`.

## Setup

Run `cliamp setup` for the fastest setup. Select **Tidal**, select a stream quality, and let the wizard write the `[tidal]` block.

Or configure Tidal in `~/.config/cliamp/config.toml`:

```toml
[tidal]
enabled = true
quality = "lossless"
```

You do not need developer credentials. cliamp includes OAuth client credentials.

Run `cliamp`, select Tidal, and press `Enter` to sign in. cliamp shows a `link.tidal.com` URL and a short device code. It also opens the URL in a browser. Approve the device there. cliamp waits for up to 5 minutes. After authorization, cliamp stores credentials in `~/.config/cliamp/tidal_credentials.json`. Later launches refresh them without a message.

### Quality

`quality` selects the Tidal stream tier. If you omit it, cliamp uses `"lossless"`. Supported values:

| Value | Format |
|---|---|
| `"low"` | AAC 96 kbps |
| `"high"` | AAC 320 kbps |
| `"lossless"` / `"hires"` | FLAC up to 24-bit / 192 kHz where available (see note) |

> **FLAC availability note:** Tidal serves FLAC to third-party clients of this type only for tracks with a hi-res ("Max") master. It delivers these tracks as unencrypted MPEG-DASH. cliamp plays them natively at full hi-res quality. For a track without a hi-res master, Tidal downgrades this client to AAC 320 on the server. The python-tidal ecosystem reports the same limit. cliamp shows a one-time footer notice. Sign-in through the Tidal Android-type (PKCE) client, which unlocks FLAC for the full catalog, is planned.

Any other value uses `"lossless"`.

For hi-res playback settings, see [audio-quality.md](audio-quality.md). Use `bit_depth = 32` with a matching `sample_rate` for lossless playback.

### Custom client credentials

The cliamp OAuth client credentials are shared with other open-source Tidal clients. Tidal sometimes revokes these client IDs. If sign-in fails with a client error, set a new pair without waiting for a cliamp release:

```toml
[tidal]
client_id = "your-client-id"
client_secret = "your-client-secret"
```

After you change credentials, run `cliamp tidal reset` and sign in again.

## Usage

Start cliamp with Tidal selected:

```sh
cliamp --provider tidal
```

After authentication, Tidal appears in the provider list. Press `T` to select Tidal. Press `Esc`/`b` to open the provider browser and select it.

The provider shows your Tidal library:

- **Favorite Tracks**: your liked songs (up to 500).
- **Your playlists**: playlists you created or subscribed to.
- **Favorite albums**: browsable in the album view.
- **Favorite artists**: browse an artist to see their albums.

Press `Ctrl+F` while Tidal is active to search the Tidal catalog for tracks and albums. Album results appear first. When you select an album, it expands to its track list. Enter plays, `a` appends, and `q` queues the next track.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate |
| `Enter` | Load the selected playlist/album or play the selected track |
| `Ctrl+F` | Search Tidal (tracks and albums) |
| `Ctrl+R` | Refresh (re-resolves stream URLs) |
| `Tab` | Switch between provider and playlist focus |
| `Esc` / `b` | Open provider browser |

After you load a playlist or album, cliamp returns to the standard playlist view. Use the usual controls for seek, volume, EQ, shuffle, repeat, queue, search, and lyrics.

## Troubleshooting

- **Sign-in fails immediately / "client_id may be revoked"**: Tidal sometimes revokes the shared cliamp client credentials. Set `client_id` and `client_secret` in `[tidal]`, run `cliamp tidal reset`, and sign in again.
- **Device code expired**: The `link.tidal.com` code is valid for about 5 minutes. Press `Enter` on the Tidal provider again to get a new code.
- **Re-authenticate**: Run `cliamp tidal reset` to clear stored credentials. Then restart cliamp, select Tidal, and sign in again. This is the same as deleting `~/.config/cliamp/tidal_credentials.json`.
- **Track is unplayable / skipped**: The track might not stream in the region, or its stream might be encrypted for this client type. cliamp marks the track unplayable and continues.
- **"delivered as HIGH (AAC)" notice**: The track has no hi-res master, so Tidal does not send FLAC to this client type. See the FLAC availability note. Playback continues at AAC 320.
- **Long-idle sessions**: cliamp resolves stream URLs when each track starts. Queued tracks continue after an idle period without manual refresh. `Ctrl+R` gets the playlist lists again.

## Requirements

- A paid Tidal subscription
- `ffmpeg` on `PATH` for FLAC/AAC decoding
- No developer/API registration: cliamp uses built-in credentials automatically

## A note on the API

cliamp uses the Tidal private client API, like open-source python-tidal-based clients. The official Tidal developer API allows only 30-second previews for third-party apps. cliamp only streams. It does not write decoded audio to disk.
