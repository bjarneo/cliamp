# Lyrics

Press `y` to show lyrics for the current track. For local files, cliamp uses embedded lyrics from the file tags first. If no embedded lyrics are present, lyrics are fetched from LRCLIB and NetEase Cloud Music.

## Modes

- **Synced lyrics**: for local files, Navidrome tracks, and YouTube/yt-dlp tracks with a known duration, lyrics auto scroll and highlight the active line in time with playback.
- **Scroll mode**: for plain lyrics without timestamps, live radio (ICY), and YouTube Live (position is not song-relative), use `j`/`k` or arrow keys to scroll manually.

Embedded LRC lyrics keep their timestamps. Embedded plain text lyrics are shown in scroll mode.

## Streams

Lyrics auto update when the ICY metadata changes (e.g., internet radio station transitions).

## YouTube and SoundCloud

Titles like "Artist - Song (Official Video)" are parsed to build better search queries.
