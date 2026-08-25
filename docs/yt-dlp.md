# YouTube, SoundCloud, Mixcloud, NetEase, Bandcamp and Bilibili

Play from YouTube, SoundCloud, Mixcloud, NetEase, Bandcamp, and Bilibili URLs if [yt-dlp](https://github.com/yt-dlp/yt-dlp) is installed:

```sh
cliamp https://www.youtube.com/watch?v=dQw4w9WgXcQ
cliamp https://soundcloud.com/artist/track
cliamp https://www.mixcloud.com/creator/show-name/
cliamp 'https://music.163.com/#/song?id=1973665667'
cliamp https://artist.bandcamp.com/album/name
cliamp https://www.bilibili.com/video/BV1xxxxxxxxx
cliamp https://space.bilibili.com/uid/lists/id  # season/series playlists
```

Playlists and albums are supported. Press `S` to save a downloaded track to `~/Music/cliamp/`.

Live streams (for example 24/7 YouTube lofi radios) work too: they expose no audio-only formats, so cliamp falls back to the best muxed stream and plays only its audio track. This also applies to live-stream URLs used as stations in `radios.toml`.

## Search

Search and play directly from the command line:

```sh
cliamp search "never gonna give you up"       # search YouTube
cliamp search-sc "lofi beats"                  # search SoundCloud
```

Inside the TUI, press `Ctrl+F` to search the active provider — YouTube when you're on YouTube/YT-Music, SoundCloud when you're on SoundCloud, Mixcloud when you're on Mixcloud, and NetEase when you're on NetEase. These providers have dedicated docs covering signed-in playback: [SoundCloud](soundcloud.md), [Mixcloud](mixcloud.md), [NetEase](netease.md).

## Disclaimer

**Use at your own risk.** Downloading or streaming copyrighted content may violate the terms of service of these platforms. You are responsible for how you use this feature.
