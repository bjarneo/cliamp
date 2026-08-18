# YouTube & YouTube Music Integration

Cliamp can browse your [YouTube](https://youtube.com/) and [YouTube Music](https://music.youtube.com/) playlists and play tracks through its audio pipeline. EQ, visualizer, and all effects apply. Playback uses yt-dlp, which must be installed.

Your playlists are automatically classified into two providers:
- **YouTube Music**: playlists containing music content
- **YouTube**: playlists containing non-music content (podcasts, vlogs, tutorials, etc.)

> **Quick start:**
> - **Cookie-based (zero OAuth):** Set `cookies_from = "your_browser"` in `~/.config/cliamp/config.toml` (or pick a browser via `cliamp setup`). Playlists load via your existing browser session; no OAuth credentials or `ytmusic_credentials.json`.
> - **OAuth-based:** Provide Google Cloud OAuth credentials (below), then press Enter in the provider browser to sign in; credentials are cached at `~/.config/cliamp/ytmusic_credentials.json`.

## Setup

### Option 1: Browser cookies (zero setup)

Add your browser name to `~/.config/cliamp/config.toml` (or run `cliamp setup`):

```toml
[ytmusic]
cookies_from = "chrome"
```

Supported browsers: `chrome`, `firefox`, `brave`, `edge`, `opera`, `safari`, `chromium`.

You can also point at a specific profile or path using yt-dlp's `browser:path` syntax. For example, Zen browser (a Firefox fork) stores its profile outside the default location:

```toml
[ytmusic]
cookies_from = "firefox:~/.config/zen"
```

### Option 2: Custom Google Cloud OAuth client

#### Creating your client ID

1. Go to [console.cloud.google.com](https://console.cloud.google.com/) and log in
2. Create a new project (or select an existing one)
3. Navigate to **APIs & Services > Library**
4. Search for **YouTube Data API v3** and click **Enable**
5. Go to **APIs & Services > Credentials**
6. Click **Create Credentials > OAuth client ID**
7. If prompted, configure the OAuth consent screen first:
   - User Type: **External**
   - Fill in app name (e.g. "cliamp") and your email
   - Add scope: `https://www.googleapis.com/auth/youtube.readonly`
   - Add yourself as a test user (required while app is in "Testing" status)
8. For the OAuth client ID:
   - Application type: **Desktop app**
   - Name: anything (e.g. "cliamp")
9. Copy the **Client ID** and **Client Secret**

#### Configuring cliamp with OAuth

Add your client ID and client secret to `~/.config/cliamp/config.toml`:

```toml
[ytmusic]
client_id = "your_client_id_here"
client_secret = "your_client_secret_here"
```

Optional: to play uploaded/private tracks, add your browser for cookie access:

```toml
[ytmusic]
client_id = "your_client_id_here"
client_secret = "your_client_secret_here"
cookies_from = "chrome"
```

Optional: control whether `list=` URLs expand the full playlist or resolve as a single video:

```toml
[ytmusic]
expand_playlist = false
```

When `expand_playlist` is `true` (default), URLs with a `list=` parameter — like auto-generated mixes (RDAMVM, RDMM), album playlists (OLAK), or custom playlists (PL) — are resolved incrementally: the first 20 tracks load instantly so playback starts quickly, while the remaining tracks are fetched in background batches. Set to `false` (or pass `--no-expand-playlist`) to strip the playlist parameter and resolve only the single video.

Run `cliamp` (or `cliamp --provider ytmusic` / `cliamp --provider youtube`), select a provider, and press Enter to sign in. Credentials are cached at `~/.config/cliamp/ytmusic_credentials.json`. Subsequent launches refresh silently.

## Usage

Once authenticated, **YouTube** and **YouTube Music** appear as separate providers alongside Spotify, Navidrome, and Radio. Press `Esc`/`b` to open the provider browser.

- **YouTube Music** shows playlists classified as music (video category "Music")
- **YouTube** shows all other playlists (podcasts, vlogs, tutorials, etc.)

Both share the same Google account login. Classification is automatic (based on video category) and cached to disk so subsequent launches are instant.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate playlists |
| `Enter` | Load the selected playlist |
| `Tab` | Switch between provider and playlist focus |
| `Ctrl+R` | Refresh playlists from YouTube |
| `Esc` / `b` | Open provider browser |

After loading a playlist you return to the standard playlist view with all the usual controls (seek, volume, EQ, shuffle, repeat, queue, search, lyrics).

## Playlists

When using OAuth authentication, playlists are automatically split between the two providers:

**YouTube Music** shows:
- **Liked Music**: your liked songs (YouTube Music's special `LM` playlist)
- Playlists containing music content (auto-classified by video category)

**YouTube** shows:
- **Liked Videos**: your liked videos (YouTube's special `LL` playlist)
- Playlists containing non-music content

For OAuth setups, classification is determined by sampling a video from each playlist and checking its YouTube category. Results are cached at `~/.config/cliamp/ytmusic_classification.json` (and `~/.config/cliamp/ytmusic_cache.json`). Delete these files or press `Ctrl+R` in the TUI to reclassify and refresh.

For cookie-backed providers (`cookies_from`), all custom playlists are appended to both YouTube Music and YouTube results without category classification, and `ytmusic_classification.json` is not populated. Results and tracks are cached at `~/.config/cliamp/ytmusic_cache.json` (refresh with `Ctrl+R` or by deleting the cache file).

## Troubleshooting

- **Linux Keyring / Cookie Decryption (`cannot decrypt v11 cookies: no key found`)**: On Linux desktop environments / window managers (Hyprland, Sway, i3, etc.) where Chromium/Chrome encrypts cookies via GNOME Keyring or KWallet, append the keyring name to `cookies_from`:

  ```toml
  [ytmusic]
  cookies_from = "chromium+gnomekeyring"  # or "chrome+gnomekeyring", "brave+kwallet"
  ```

- **"ERR: waiting for audio data: EOF" / playback stops immediately**: yt-dlp couldn't produce a stream. cliamp now surfaces yt-dlp's real message (e.g. "Sign in to confirm you're not a bot") instead of the bare EOF, so read the full error. The common causes:
  - **Outdated yt-dlp**: update it (`yt-dlp -U`, or reinstall from the [official repo](https://github.com/yt-dlp/yt-dlp)). Distro and winget builds are frequently stale and break when YouTube changes.
  - **Bot detection**: YouTube blocks anonymous requests. Set `cookies_from` (see above) so yt-dlp reuses your logged-in browser session. For Zen browser use `cookies_from = "firefox:~/.config/zen"`.
  - **Wrong `cookies_from` value**: the browser must be installed and logged in to YouTube, and the profile path must be correct.
- **"OAuth failed"**: Make sure your Google Cloud project has YouTube Data API v3 enabled and your OAuth client type is "Desktop app".
- **"Access blocked"**: While your app is in "Testing" status, only test users you've added can sign in. Add your Google account as a test user in the OAuth consent screen settings.
- **Playlist not showing**: Only playlists in your library are listed. Save/follow a playlist in YouTube Music for it to appear.
- **Re-authenticate / Reset Cache**: Delete `~/.config/cliamp/ytmusic_credentials.json` (for OAuth) or press `Ctrl+R` in the TUI / remove `~/.config/cliamp/ytmusic_cache.json`.
- **Private/deleted videos**: These are automatically skipped when loading a playlist.

## Requirements

- [yt-dlp](https://github.com/yt-dlp/yt-dlp) installed and on your PATH (for audio playback)
- Either browser cookies (`cookies_from = "browser"`, zero Google Cloud setup required) OR a Google Cloud project with YouTube Data API v3 enabled (OAuth path)
- No Spotify Premium or other paid subscription required. YouTube Music free tier works
