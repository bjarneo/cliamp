# YouTube & YouTube Music Integration

Cliamp can browse your [YouTube](https://youtube.com/) and [YouTube Music](https://music.youtube.com/) playlists and play tracks through its audio pipeline. EQ, visualizer, and all effects apply. Playback uses yt-dlp, which must be installed.

Your playlists are automatically classified into two providers:
- **YouTube Music**: playlists containing music content
- **YouTube**: playlists containing non-music content (podcasts, vlogs, tutorials, etc.)

> **Quick start:** YouTube Music works out of the box with built-in fallback credentials — just install yt-dlp and select it in the provider browser. Run `cliamp setup` if you want to disable it, supply your own OAuth client, or configure cookie-based age-gated playback. Manual setup steps for the custom path are below.

## Setup

### Creating your client ID

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

### Configuring cliamp

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

Supported browsers: `chrome`, `firefox`, `brave`, `edge`, `opera`, `safari`, `chromium`.

You can also point at a specific profile or path using yt-dlp's `browser:path` syntax. For example, Zen browser (a Firefox fork) stores its profile outside the default location:

```toml
[ytmusic]
cookies_from = "firefox:~/.config/zen"
```

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
| `Esc` / `b` | Open provider browser |

After loading a playlist you return to the standard playlist view with all the usual controls (seek, volume, EQ, shuffle, repeat, queue, search, lyrics).

## Playlists

Playlists are automatically split between the two providers:

**YouTube Music** shows:
- **Liked Music**: your liked songs (YouTube Music's special `LM` playlist)
- Playlists containing music content (auto-classified by video category)

**YouTube** shows:
- **Liked Videos**: your liked videos (YouTube's special `LL` playlist)
- Playlists containing non-music content

Classification is determined by sampling a video from each playlist and checking its YouTube category. Results are cached at `~/.config/cliamp/ytmusic_classification.json`. Delete this file to reclassify.

## Troubleshooting

- **"ERR: waiting for audio data: EOF" / playback stops immediately**: yt-dlp couldn't produce a stream. cliamp now surfaces yt-dlp's real message (e.g. "Sign in to confirm you're not a bot") instead of the bare EOF, so read the full error. The common causes:
  - **Outdated yt-dlp**: update it (`yt-dlp -U`, or reinstall from the [official repo](https://github.com/yt-dlp/yt-dlp)). Distro and winget builds are frequently stale and break when YouTube changes.
  - **Bot detection**: YouTube blocks anonymous requests. Set `cookies_from` (see above) so yt-dlp reuses your logged-in browser session. For Zen browser use `cookies_from = "firefox:~/.config/zen"`.
  - **Wrong `cookies_from` value**: the browser must be installed and logged in to YouTube, and the profile path must be correct.
- **"OAuth failed"**: Make sure your Google Cloud project has YouTube Data API v3 enabled and your OAuth client type is "Desktop app".
- **"Access blocked"**: While your app is in "Testing" status, only test users you've added can sign in. Add your Google account as a test user in the OAuth consent screen settings.
- **Playlist not showing**: Only playlists in your library are listed. Save/follow a playlist in YouTube Music for it to appear.
- **Re-authenticate**: Delete `~/.config/cliamp/ytmusic_credentials.json` and restart cliamp to trigger a fresh login.
- **Private/deleted videos**: These are automatically skipped when loading a playlist.

## Requirements

- [yt-dlp](https://github.com/yt-dlp/yt-dlp) installed and on your PATH (for audio playback)
- A Google Cloud project with YouTube Data API v3 enabled
- No Spotify Premium or other paid subscription required. YouTube Music free tier works
