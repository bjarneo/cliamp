# Lyrics

Press `y` to show lyrics for the current track. For local files, cliamp uses embedded lyrics from the file tags first. If no embedded lyrics are present, lyrics are fetched from LRCLIB and NetEase Cloud Music.

For Spotify tracks, cliamp asks Spotify directly for synced lyrics first (requires being signed in to the Spotify provider); if that fails it falls back to the same lookup as every other source.

## Modes

- **Synced lyrics**: for local files, Navidrome, and Spotify tracks, lyrics auto scroll and highlight the active line in time with playback. If the highlight is consistently early or late (some Spotify/Musixmatch tracks are offset from the audio master), nudge the timing with `[`/`]` while the lyrics overlay is open; the offset is saved to `lyrics_offset_ms` in your config and applies to all sources.
- **Scroll mode**: for streams and plain lyrics without timestamps, use `j`/`k` or arrow keys to scroll manually.

Embedded LRC lyrics keep their timestamps. Embedded plain text lyrics are shown in scroll mode.

## Streams

Lyrics auto update when the ICY metadata changes (e.g., internet radio station transitions).

## YouTube and SoundCloud

Titles like "Artist - Song (Official Video)" are parsed to build better search queries.
