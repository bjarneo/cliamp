# Apple Music

Play the Apple Music catalog on macOS with your own subscription. Music.app holds the subscription and decodes each song. cliamp takes the decoded audio and plays it through its own pipeline, so cliamp owns the sound.

| Feature | Where it happens |
|---------|------------------|
| Volume | cliamp, in dB. Music stays at 100% during playback |
| 10-band EQ | cliamp. Music's EQ is not used |
| Visualizers | cliamp |
| Lyrics | cliamp's lookup, timed by cliamp's position. `[` and `]` shift them |
| Pause and seek | cliamp, followed by Music |

Audio passes through a short playback buffer. cliamp does not save it.

## Requirements

- macOS 14.2 or later, and Music.app signed in to an Apple Music subscription.
- A build of cliamp with the helpers next to the binary:

  ```sh
  go build -o dist/cliamp .
  sh external/applemusic/helper/build.sh dist
  ```

  The provider registers when `applemusic-helper` is next to the binary, or when `CLIAMP_APPLEMUSIC_HELPER` names that directory.
- Accessibility access for your terminal, under System Settings > Privacy & Security > Accessibility.
- System audio recording access for CliampTap. macOS asks on first play. The setting is under System Settings > Privacy & Security > Screen & System Audio Recording.

## Use

Press `Ctrl+F` in the Apple Music provider to search the catalog. The Music Library playlist lists your library. Search needs no account.

Search uses the `us` storefront. To use another, set `CLIAMP_APPLEMUSIC_COUNTRY`, for example `CLIAMP_APPLEMUSIC_COUNTRY=gb`.

## Quality

Music.app chooses the stream. To get Lossless, set **Lossless** under Music > Settings > Playback, and set **Dolby Atmos** to **Off**. With Atmos on, a song that has an Atmos mix plays in Atmos and reaches cliamp as a stereo mixdown. `cliamp.log` records the quality badge of each song.

The tap runs at the sample rate of your output device. A song at another rate is resampled by macOS, so the audio is not bit-perfect. Set your device rate in Audio MIDI Setup to match the music you play most.

## Limits

- Starting a song takes about 5 seconds, and gapless playback is not possible.
- Music appears for about half a second when a song starts, then hides. Song links only work while Music is active.
- Using Music.app during playback interferes with cliamp, and starting a song replaces what Music is playing.
- Playback speed other than 1.0 is not supported.
- This route depends on Music's interface and an English UI. A Music update can break it.
- A library file that is not in the catalog cannot play.
- Quit cliamp normally. If it crashes, Music's volume stays at 100%.
