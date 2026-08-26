# YouTube, SoundCloud, Mixcloud, NetEase, Bandcamp and Bilibili

Install [yt-dlp](https://github.com/yt-dlp/yt-dlp), then play YouTube, SoundCloud, Mixcloud, NetEase, Bandcamp, and Bilibili URLs:

```sh
cliamp https://www.youtube.com/watch?v=dQw4w9WgXcQ
cliamp https://soundcloud.com/artist/track
cliamp https://www.mixcloud.com/creator/show-name/
cliamp 'https://music.163.com/#/song?id=1973665667'
cliamp https://artist.bandcamp.com/album/name
cliamp https://www.bilibili.com/video/BV1xxxxxxxxx
cliamp https://space.bilibili.com/uid/lists/id  # season/series playlists
```

You can play playlists and albums. Press `S` to save a downloaded track to `~/Music/cliamp/`.

You can also play live streams, such as 24/7 YouTube lofi radios. They have no audio-only formats. cliamp uses the best muxed stream and plays only its audio track. This also applies to live-stream URLs used as stations in `radios.toml`.

## Search

Search and play from the command line:

```sh
cliamp search "never gonna give you up"       # search YouTube
cliamp search-sc "lofi beats"                  # search SoundCloud
```

In the TUI, press `Ctrl+F` to search the active provider. This searches YouTube for YouTube/YT-Music, SoundCloud for SoundCloud, Mixcloud for Mixcloud, and NetEase for NetEase. These providers have signed-in playback guides: [SoundCloud](soundcloud.md), [Mixcloud](mixcloud.md), and [NetEase](netease.md).

## Disclaimer

**Use at your own risk.** Downloading or streaming copyrighted content can violate the terms of service of these platforms. You are responsible for use of this feature.
