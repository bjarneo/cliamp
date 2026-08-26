# YouTube & YouTube Music Integration

Use Cliamp to browse [YouTube](https://youtube.com/) and [YouTube Music](https://music.youtube.com/) playlists and play tracks through its audio pipeline. EQ, the visualizer, and other effects apply. Install yt-dlp for playback.

Cliamp automatically sorts playlists into two providers:
- **YouTube Music**: Playlists with music content.
- **YouTube**: Playlists with non-music content, such as podcasts, vlogs, and tutorials.

> **Quick start:**
> - **Cookie-based (zero OAuth):** Set `cookies_from = "your_browser"` in `~/.config/cliamp/config.toml`, or select a browser in `cliamp setup`. cliamp loads playlists through the existing browser session. You do not need OAuth credentials or `ytmusic_credentials.json`.
> - **OAuth-based:** Set Google Cloud OAuth credentials as described below. Then press Enter in the provider browser to sign in. cliamp stores credentials in `~/.config/cliamp/ytmusic_credentials.json`.

## Setup

### Option 1: Browser cookies (zero setup)

Add the browser name to `~/.config/cliamp/config.toml`, or run `cliamp setup`:

```toml
[ytmusic]
cookies_from = "chrome"
```

Supported browsers: `chrome`, `firefox`, `brave`, `edge`, `opera`, `safari`, `chromium`.

You can set a specific profile or path with yt-dlp `browser:path` syntax. For example, the Zen browser, a Firefox fork, stores its profile outside the default path:

```toml
[ytmusic]
cookies_from = "firefox:~/.config/zen"
```

### Option 2: Custom Google Cloud OAuth client

#### Creating your client ID

1. Go to [console.cloud.google.com](https://console.cloud.google.com/) and sign in.
2. Create a project or select an existing project.
3. Open **APIs & Services > Library**.
4. Search for **YouTube Data API v3** and click **Enable**.
5. Open **APIs & Services > Credentials**.
6. Click **Create Credentials > OAuth client ID**.
7. If prompted, configure the OAuth consent screen first:
    - User Type: **External**
    - Enter an app name, such as "cliamp", and an email address.
    - Add scope: `https://www.googleapis.com/auth/youtube.readonly`
    - Add yourself as a test user. This is required while the app is in "Testing" status.
8. For the OAuth client ID:
    - Application type: **Desktop app**
    - Name: Any name, such as "cliamp".
9. Copy the **Client ID** and **Client Secret**.

#### Configuring cliamp with OAuth

Add the client ID and client secret to `~/.config/cliamp/config.toml`:

```toml
[ytmusic]
client_id = "your_client_id_here"
client_secret = "your_client_secret_here"
```

Optional: To play uploaded or private tracks, set the browser for cookie access:

```toml
[ytmusic]
client_id = "your_client_id_here"
client_secret = "your_client_secret_here"
cookies_from = "chrome"
```

Optional: Control whether `list=` URLs expand a full playlist or resolve as one video:

```toml
[ytmusic]
expand_playlist = false
```

When `expand_playlist` is `true` (default), cliamp expands URLs with a `list=` parameter. This includes auto-generated mixes (RDAMVM, RDMM), album playlists (OLAK), and custom playlists (PL). cliamp loads the first 20 tracks immediately, then gets the remaining tracks in background batches. Set it to `false`, or pass `--no-expand-playlist`, to remove the playlist parameter and resolve only one video.

Run `cliamp`, `cliamp --provider ytmusic`, or `cliamp --provider youtube`. Select a provider and press Enter to sign in. cliamp stores credentials in `~/.config/cliamp/ytmusic_credentials.json`. Later launches refresh them without a message.

## Usage

After authentication, **YouTube** and **YouTube Music** appear as separate providers. Press `Esc`/`b` to open the provider browser.

- **YouTube Music** shows playlists classified as music (video category "Music").
- **YouTube** shows all other playlists, such as podcasts, vlogs, and tutorials.

Both use the same Google account sign-in. cliamp classifies playlists by video category and stores the result on disk. Later launches use the cached result.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate playlists |
| `Enter` | Load the selected playlist |
| `Tab` | Switch between provider and playlist focus |
| `Ctrl+R` | Refresh playlists from YouTube |
| `Esc` / `b` | Open provider browser |

After you load a playlist, Cliamp returns to the standard playlist view. Use the usual controls for seek, volume, EQ, shuffle, repeat, queue, search, and lyrics.

## Playlists

With OAuth authentication, cliamp splits playlists between the two providers:

**YouTube Music** shows:
- **Liked Music**: Liked songs in the YouTube Music special `LM` playlist.
- Playlists with music content, automatically classified by video category.

**YouTube** shows:
- **Liked Videos**: Liked videos in the YouTube special `LL` playlist.
- Playlists with non-music content.

For OAuth setups, cliamp samples a video from each playlist and checks its YouTube category. It stores results in `~/.config/cliamp/ytmusic_classification.json` and `~/.config/cliamp/ytmusic_cache.json`. Delete these files or press `Ctrl+R` in the TUI to classify again and refresh.

For cookie-backed providers (`cookies_from`), cliamp adds all custom playlists to both YouTube Music and YouTube results without category classification. It does not create `ytmusic_classification.json`. It stores results and tracks in memory for the current session. Press `Ctrl+R` to refresh them.

## Troubleshooting

- **Linux Keyring / Cookie Decryption (`cannot decrypt v11 cookies: no key found`)**: On Linux desktops or window managers, such as Hyprland, Sway, and i3, Chromium/Chrome can encrypt cookies with GNOME Keyring or KWallet. Append the keyring name to `cookies_from`:

  ```toml
  [ytmusic]
  cookies_from = "chromium+gnomekeyring"  # or "chrome+gnomekeyring", "brave+kwallet"
  ```

- **"ERR: waiting for audio data: EOF" / playback stops immediately**: yt-dlp did not produce a stream. cliamp shows the yt-dlp message, such as "Sign in to confirm you're not a bot", instead of only EOF. Read the full error. Common causes follow:
  - **Outdated yt-dlp**: Update it with `yt-dlp -U`, or reinstall from the [official repo](https://github.com/yt-dlp/yt-dlp). Distro and winget builds are often stale and can fail when YouTube changes.
  - **Bot detection**: YouTube blocks anonymous requests. Set `cookies_from` so yt-dlp uses the signed-in browser session. For Zen browser, use `cookies_from = "firefox:~/.config/zen"`.
  - **Wrong `cookies_from` value**: Install the browser, sign in to YouTube, and use the correct profile path.
- **"OAuth failed"**: Ensure that the Google Cloud project has YouTube Data API v3 enabled and uses OAuth client type "Desktop app".
- **"Access blocked"**: While the app is in "Testing" status, only added test users can sign in. Add the Google account as a test user in OAuth consent screen settings.
- **Playlist not showing**: The provider lists only library playlists. Save or follow a playlist in YouTube Music for it to appear.
- **Re-authenticate / Reset Cache**: Delete `~/.config/cliamp/ytmusic_credentials.json` for OAuth. Or press `Ctrl+R` in the TUI or remove `~/.config/cliamp/ytmusic_cache.json`.
- **Private/deleted videos**: cliamp automatically skips these when it loads a playlist.

## Requirements

- [yt-dlp](https://github.com/yt-dlp/yt-dlp) installed and on `PATH` for audio playback
- Browser cookies (`cookies_from = "browser"`, no Google Cloud setup) or a Google Cloud project with YouTube Data API v3 enabled for OAuth
- No Spotify Premium or other paid subscription is required. The YouTube Music free tier works.
