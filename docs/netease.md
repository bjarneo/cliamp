# NetEase Cloud Music Integration

cliamp supports NetEase Cloud Music as an opt-in provider. It can browse your account playlists, saved playlists, liked songs, and public charts. Playback is handled by `yt-dlp`, so `yt-dlp` and `ffmpeg` must be on `PATH`.

## Quick Start

Sign in at `music.163.com` in your browser, then run:

```sh
cliamp setup
```

Pick **NetEase Cloud Music**, then choose the browser where you are signed in. The wizard validates the session and writes:

```toml
[netease]
enabled = true
cookies_from = "chrome"
user_id = "your-account-user-id"
```

cliamp stores the browser name and user id only. It does not store your password or copy cookies into `config.toml`.

## Manual Config

```toml
[netease]
enabled = true
cookies_from = "chrome"
user_id = "78819429"
```

`cookies_from` is passed to `yt-dlp --cookies-from-browser`. Supported names depend on your `yt-dlp` version and commonly include `chrome`, `chromium`, `firefox`, `brave`, `edge`, `opera`, `safari`, and `vivaldi`. The setup wizard has common browsers as menu choices; use **Custom browser/profile** only for profile-specific values such as `chrome:Profile 1` or `firefox:default-release`.

`user_id` is optional when cookies are valid. If omitted, cliamp discovers it from the signed-in account.

## Usage

Start directly on NetEase:

```sh
cliamp --provider netease
```

Inside the TUI:

| Key | Action |
|---|---|
| `M` | Open NetEase provider |
| `Ctrl+F` | Search NetEase songs while NetEase is active |
| `Enter` | Load the highlighted playlist or play the highlighted track |
| `Ctrl+R` | Refresh playlists |

Direct NetEase URLs also work:

```sh
cliamp 'https://music.163.com/#/song?id=1973665667'
cliamp 'https://music.163.com/#/playlist?id=3778678'
```

## Limits

NetEase playback availability depends on the account, region, and track rights. If a song is unavailable upstream, cliamp surfaces the `yt-dlp` error. Using `cookies_from` gives `yt-dlp` the same account context as your browser, which improves access for tracks your account can play.
