# Streaming

cliamp plays audio from URLs, M3U/PLS playlists, and podcast RSS feeds.

## HTTP Streams

Play audio directly from a URL:

```sh
cliamp https://example.com/song.mp3
cliamp http://radio-station.com/stream.m3u
cliamp local.mp3 https://example.com/remote.mp3   # mix local + remote
```

For a non-seekable HTTP stream, the UI shows `● Streaming` and a static seek bar. Seek keys have no effect.

## PLS Playlists

cliamp supports PLS playlist files and M3U files:

```sh
cliamp https://radio.cliamp.stream/lofi/stream.pls
```

## HLS Streams

cliamp supports HLS master and media playlists (`.m3u8`). Large broadcasters, such as Brazilian RBS/Wowza stations, use these playlists:

```sh
cliamp "https://example.com/live/playlist.m3u8"
```

cliamp passes the URL to ffmpeg. ffmpeg resolves relative chunklist and segment URIs and follows the live segment window. This requires `ffmpeg`, which AAC and Opus also require.

Live HLS uses timed metadata, not inline ICY. cliamp does not update the now-playing track title for HLS streams.

## Podcasts

Play a podcast by passing its RSS feed URL:

```sh
cliamp https://example.com/podcast/feed.xml
```

cliamp reads episode titles and the podcast name from the feed and shows them in the playlist.

### Xiaoyuzhou (小宇宙)

Play an individual [Xiaoyuzhou](https://www.xiaoyuzhoufm.com) episode by passing its URL:

```sh
cliamp https://www.xiaoyuzhoufm.com/episode/xxxx
```

## Radio Catalog

Press `R` in the player to browse about 58,000 online radio stations in the [Radio Browser](https://www.radio-browser.info/) directory. Use `/` to search by name, `Enter` to play, and `a` to add a station to the playlist.

To cut the list down by location, browse by country (`N`), pin the countries you listen to (`f`), and narrow the catalog to one of them (`c`). See [radio.md](radio.md).

## Track Info

For live radio, cliamp shows the current track from inline ICY metadata (`StreamTitle`). This works for most stations and codecs, including MP3, AAC, and Opus.

Some broadcasters do not send inline metadata. They publish now-playing data through a separate API. cliamp reads these APIs automatically:

| Station | Source | Shown |
| --- | --- | --- |
| FIP (and FIP Jazz, Rock, Groove, Reggae, Electro, Metal, Monde, Nouveautes) | Radio France livemeta API | Artist - Title |
| NTS 1 / NTS 2 | NTS live API | Current show |

NTS is live DJ radio with no tag for each track. cliamp shows the show or host name, not a song.

## Load URL at Runtime

Press `u` during playback to load a stream or playlist URL without restarting. It supports the same URL types as CLI arguments: direct audio URLs, M3U/PLS playlists, RSS podcast feeds, and yt-dlp compatible links.

## Run Your Own Radio Station

Run an internet radio station with [cliamp-server](https://github.com/bjarneo/cliamp-server). Point it to a directory of audio files to start broadcasting. It supports multiple stations, live metadata, and on-the-fly transcoding.

See [radio.md](radio.md) for the radio provider in full.

See [playlists.md](playlists.md) for M3U playlist details.
