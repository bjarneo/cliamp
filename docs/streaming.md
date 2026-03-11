# Streaming

cliamp can play audio from URLs, M3U/PLS playlists, and podcast RSS feeds.

## HTTP Streams

Play audio directly from URLs:

```sh
cliamp https://example.com/song.mp3
cliamp http://radio-station.com/stream.m3u
cliamp local.mp3 https://example.com/remote.mp3   # mix local + remote
```

For non-seekable HTTP streams, the UI shows `● Streaming` with a static seek bar, and seek keys are silently ignored.

## PLS Playlists

PLS playlist files are supported alongside M3U:

```sh
cliamp https://radio.cliamp.stream/lofi/stream.pls
```

## Podcasts

Play any podcast by passing its RSS feed URL:

```sh
cliamp https://example.com/podcast/feed.xml
```

Episode titles and the podcast name are extracted from the feed and shown in the playlist.

### Xiaoyuzhou (小宇宙)

Play individual episodes from [Xiaoyuzhou](https://www.xiaoyuzhoufm.com) by passing the episode URL:

```sh
cliamp https://www.xiaoyuzhoufm.com/episode/xxxx
```

## Load URL at Runtime

Press `u` while playing to load a new stream or playlist URL without restarting. Supports the same URL types as CLI arguments: direct audio URLs, M3U/PLS playlists, RSS podcast feeds, and yt-dlp compatible links.

## Run Your Own Radio Station

Run your own internet radio with [cliamp-server](https://github.com/bjarneo/cliamp-server). Point it at a directory of audio files and it starts broadcasting. Supports multiple stations, live metadata, and on-the-fly transcoding.

See also: [playlists.md](playlists.md) for M3U playlist details.
