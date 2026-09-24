# Lyrics

Press `y` to show lyrics for the current track. For a local file, cliamp first uses lyrics embedded in file tags. If the file has no embedded lyrics, cliamp fetches lyrics from LRCLIB and NetEase Cloud Music.

For Spotify tracks, cliamp asks Spotify directly for synced lyrics first (requires being signed in to the Spotify provider); if that fails it falls back to the same lookup as every other source.

## Modes

- **Synced lyrics**: For any track whose playback position maps to song time, lyrics scroll automatically and highlight the active line during playback. That covers local files and provider tracks such as Navidrome, Spotify, and Qobuz, plus YouTube/yt-dlp tracks with a known duration. If the highlight is consistently early or late (some Spotify and Musixmatch tracks are offset from the audio master), nudge the timing with `[`/`]` while the lyrics overlay shows timestamped lines; the offset is saved to `lyrics_offset_ms` in your config and applies to all synced sources.
- **Scroll mode**: For plain lyrics without timestamps, live radio (ICY), and YouTube Live, use `j`/`k` or the arrow keys to scroll manually. Live streams use this mode even with timestamped lyrics and station metadata, because their playback position is not relative to the song.

cliamp keeps timestamps in embedded LRC lyrics. It shows embedded plain-text lyrics in scroll mode.

## Streams

cliamp updates lyrics when ICY metadata changes, for example when an internet radio station changes tracks.

## YouTube and SoundCloud

cliamp parses titles such as "Artist - Song (Official Video)" to build search queries.
