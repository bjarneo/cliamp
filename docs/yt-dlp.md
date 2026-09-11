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

## Autoplay

With `autoplay_radio = true` (or the `c` key in the player), cliamp keeps the
music going when the queue runs out: it loads the YouTube Mix seeded from the
last track — the same "related tracks" radio YouTube's own autoplay uses — and
appends the top 5 entries that are not already in the queue. When those run
out, the last of them seeds the next batch. This works for any YouTube or
YouTube Music track, including single tracks played from `Ctrl+F` search.

Playing a new track yourself ends the run: the previously played track and the
related tracks autoplay queued behind it are removed, so the track you picked
reuses that slot instead of stacking up at the end of the queue. Tracks you
appended or queued yourself, and bookmarked tracks, are never touched.

Autoplay is a player feature: headless daemon mode (`--daemon`) has its own
track-advance path and ignores `autoplay_radio`. Repeat modes take precedence:
autoplay only fires when repeat is off and the queue is exhausted. Live streams
reconnect as before, and tracks from other sources (SoundCloud, local files, …)
end playback as before.

## Disclaimer

**Use at your own risk.** Downloading or streaming copyrighted content can violate the terms of service of these platforms. You are responsible for use of this feature.
