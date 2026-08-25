# Audio Quality

cliamp lets you tune the output sample rate, speaker buffer size, resample quality, and bit depth via `~/.config/cliamp/config.toml`. The active settings are shown in the `OUT` status line below the EQ.

These controls apply to the default native backend. The optional
[MPV backend](mpv.md) leaves format and sample-rate negotiation to MPV and the
output device.

## Configuration

Add any of these to your config file:

```toml
# Output sample rate in Hz (22050, 44100, 48000, 96000, 192000)
sample_rate = 44100

# Speaker buffer in milliseconds (50-5000)
buffer_ms = 250

# Resample quality (1-4, where 4 is best)
resample_quality = 4

# PCM bit depth for FFmpeg-decoded formats: 16 (default) or 32 (lossless)
bit_depth = 16
```

All four are optional. Defaults are shown above.

## What they do

| Setting            | Effect                                                                 |
|--------------------|------------------------------------------------------------------------|
| `sample_rate`      | Output rate sent to your sound card. 48000 matches most modern DACs.   |
| `buffer_ms`        | Lower = less latency, higher = fewer glitches. Try 200 if audio pops, or 2000 for unstable radio streams. |
| `resample_quality` | Sinc interpolation quality when a file's native rate differs from output. 4 is best, 1 is fastest. |
| `bit_depth`        | PCM precision for FFmpeg-decoded formats (m4a, aac, alac, opus, wma, webm). 32 uses float PCM which preserves up to 24-bit audio without truncation. Native formats (mp3, flac, wav, ogg) always decode at full precision regardless of this setting. |

## Quick recipes

**Lossless / hi-res setup** (good DAC, beefy CPU):

```toml
sample_rate = 96000
buffer_ms = 250
resample_quality = 4
bit_depth = 32
```

**Low-latency / weak hardware**:

```toml
sample_rate = 44100
buffer_ms = 200
resample_quality = 1
```

**Unstable radio connection**:

```toml
buffer_ms = 2000
```

This adds up to two seconds of playback latency, but gives the audio device more time to absorb short network interruptions.

Live HTTP radio also uses an automatic decoded-audio jitter buffer. If the
network runs dry, cliamp outputs silence without blocking the UI and waits for
a short refill before resuming, which avoids rapid sputtering. A source that
stays slower than its audio bitrate can still have silent gaps because the
missing audio cannot be reconstructed.

Changes take effect on next launch.
