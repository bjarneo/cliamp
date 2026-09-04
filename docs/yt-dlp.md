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

`HTTP Error 403: Forbidden` means the source rejected the media URL yt-dlp
handed to cliamp. A single 403 is usually transient, and cliamp retries the
track automatically. When every retry fails, cliamp shows yt-dlp's own output
for the last attempt, including any warnings it printed. On YouTube, common
causes include:

- **Outdated yt-dlp.** yt-dlp warns when its version is more than 90 days old.
  Update it, or set `ytdlp_path` to a newer binary.
- **No JavaScript runtime.** yt-dlp solves YouTube's challenges with an
  external runtime, `deno` by default. Install [Deno](https://deno.com), or see
  yt-dlp's [EJS guide](https://github.com/yt-dlp/yt-dlp/wiki/EJS) for the other
  supported runtimes and how to point yt-dlp at one that is not on `PATH`.
- **Missing EJS components.** The runtime executes the
  [yt-dlp-ejs](https://github.com/yt-dlp/ejs) solver scripts. Official yt-dlp
  binaries bundle them; a pip or pipx install needs the `default` dependency
  group (`pip install -U "yt-dlp[default]"`), and a distribution package may
  ship an outdated `yt-dlp-ejs` or none at all. The
  [EJS guide](https://github.com/yt-dlp/yt-dlp/wiki/EJS) also covers fetching
  the scripts at runtime with `--remote-components`.
- **Missing PO token.** Some YouTube clients only serve formats to a request
  carrying a proof-of-origin token. See yt-dlp's
  [PO token guide](https://github.com/yt-dlp/yt-dlp/wiki/PO-Token-Guide);
  browser cookies (`cookies_from`) change which clients yt-dlp uses and often
  help.

Run the diagnostics against the binary cliamp actually runs, not whichever
`yt-dlp` your shell resolves first — with `ytdlp_path` set they can be
different installs:

```sh
# $CLIAMP_YTDLP, else the ytdlp_path from config.toml, else yt-dlp on PATH
YTDLP="${CLIAMP_YTDLP:-yt-dlp}"   # or: YTDLP=~/.local/bin/yt-dlp
"$YTDLP" --version
"$YTDLP" -v --simulate 'https://www.youtube.com/watch?v=dQw4w9WgXcQ'
```

The `-v` header covers the first three, and the `[pot]` lines show which
proof-of-origin providers, if any, are plugged in:

```text
[debug] yt-dlp version stable@2026.08.19 from yt-dlp/yt-dlp (linux_aarch64_exe)
[debug] Optional libraries: ..., yt_dlp_ejs-0.8.0   # absent = no EJS scripts
[debug] JS runtimes: deno-2.9.6                     # "none" = no runtime
[debug] [youtube] [pot] PO Token Providers: none
```

yt-dlp reports the outdated-version and missing-runtime cases as warnings on
stderr, and cliamp passes them through, so they normally show up in the error
without running this by hand.

## Disclaimer

**Use at your own risk.** Downloading or streaming copyrighted content can violate the terms of service of these platforms. You are responsible for use of this feature.
