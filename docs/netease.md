# NetEase Cloud Music Integration

Enable NetEase Cloud Music to browse account playlists, saved playlists, liked songs, and public charts. Playback uses `yt-dlp`. Put `yt-dlp` and `ffmpeg` on `PATH`.

## Quick Start

Sign in to `music.163.com` in a browser. Then run:

```sh
cliamp setup
```

Select **NetEase Cloud Music**. Select the browser where you signed in. The wizard validates the session and writes:

```toml
[netease]
enabled = true
cookies_from = "chrome"
user_id = "your-account-user-id"
```

cliamp stores only the browser name and user id. It does not store your password or copy cookies to `config.toml`.

## Manual Config

```toml
[netease]
enabled = true
cookies_from = "chrome"
user_id = "78819429"
```

cliamp passes `cookies_from` to `yt-dlp --cookies-from-browser`. Supported names depend on the installed `yt-dlp` version. Common names include `chrome`, `chromium`, `firefox`, `brave`, `edge`, `opera`, `safari`, and `vivaldi`. The setup wizard lists common browsers. Use **Custom browser/profile** only for a profile value such as `chrome:Profile 1` or `firefox:default-release`.

`user_id` is optional with valid cookies. If you omit it, cliamp gets it from the signed-in account.

## Usage

Start cliamp with NetEase selected:

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

You can also use direct NetEase URLs:

```sh
cliamp 'https://music.163.com/#/song?id=1973665667'
cliamp 'https://music.163.com/#/playlist?id=3778678'
```

## Limits

NetEase playback depends on the account, region, and track rights. If a song is unavailable upstream, cliamp shows the `yt-dlp` error. `cookies_from` gives `yt-dlp` the same account context as the browser. This can improve access to tracks that the account can play.
