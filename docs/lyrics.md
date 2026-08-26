# Lyrics

Press `y` to show lyrics for the current track. For a local file, cliamp first uses lyrics embedded in file tags. If the file has no embedded lyrics, cliamp fetches lyrics from LRCLIB and NetEase Cloud Music.

For Spotify tracks, cliamp asks Spotify directly for synced lyrics first (requires being signed in to the Spotify provider); if that fails it falls back to the same lookup as every other source.

## Modes

- **Synced lyrics**: For local files, Navidrome and Spotify tracks, and YouTube/yt-dlp tracks with a known duration, lyrics scroll automatically and highlight the active line during playback. If the highlight is consistently early or late (some Spotify/Musixmatch tracks are offset from the audio master), nudge the timing with `[`/`]` while the lyrics overlay is open; the offset is saved to `lyrics_offset_ms` in your config and applies to all sources.
- **Scroll mode**: For plain lyrics without timestamps, live radio (ICY), and YouTube Live, use `j`/`k` or the arrow keys to scroll manually. The YouTube Live position is not relative to the song.

cliamp keeps timestamps in embedded LRC lyrics. It shows embedded plain-text lyrics in scroll mode.

## Streams

cliamp updates lyrics when ICY metadata changes, for example when an internet radio station changes tracks.

## YouTube and SoundCloud

cliamp parses titles such as "Artist - Song (Official Video)" to build search queries.
