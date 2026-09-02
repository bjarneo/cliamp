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

## Choosing which yt-dlp to run

By default cliamp runs the first `yt-dlp` on your `PATH`. Some distributions
ship a yt-dlp that is months behind upstream, and YouTube stops working with
old versions quickly. Point cliamp at a current binary instead of shadowing the
packaged one:

```toml
# ~/.config/cliamp/config.toml
ytdlp_path = "~/.local/bin/yt-dlp"
```

`CLIAMP_YTDLP` overrides the config key for a single run:

```sh
CLIAMP_YTDLP=~/.local/bin/yt-dlp cliamp
```

## Playback fails with HTTP 403

`HTTP Error 403: Forbidden` means YouTube rejected the media URL yt-dlp handed
to cliamp. A single 403 is usually transient, and cliamp retries the track
automatically. A 403 that survives every retry is almost always local, and
cliamp appends what it found to the error message:

- **Outdated yt-dlp.** Update it, or set `ytdlp_path` to a newer binary.
  Check with `yt-dlp --version`.
- **No JavaScript runtime.** yt-dlp needs one (`deno` by default) to solve the
  challenge that signs media URLs. Install [Deno](https://deno.com), or see
  yt-dlp's [EJS guide](https://github.com/yt-dlp/yt-dlp/wiki/EJS) for the other
  supported runtimes. `yt-dlp -v --simulate` prints a `JS runtimes:` line that
  reads `none` when nothing is available.

## Disclaimer

**Use at your own risk.** Downloading or streaming copyrighted content can violate the terms of service of these platforms. You are responsible for use of this feature.
