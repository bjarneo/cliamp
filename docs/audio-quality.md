# Audio Quality

Set the output sample rate, speaker buffer size, resample quality, and bit depth in `~/.config/cliamp/config.toml`. The `OUT` status line below the EQ shows the active settings.

## Configuration

Add these settings to your config file as needed:

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

All settings are optional. The defaults are shown above.

## What they do

| Setting            | Effect                                                                 |
|--------------------|------------------------------------------------------------------------|
| `sample_rate`      | Output rate sent to the sound card. 48000 matches most modern DACs. |
| `buffer_ms`        | Lower values reduce latency. Higher values reduce glitches. Try 200 if audio pops, or 2000 for unstable radio streams. |
| `resample_quality` | Sinc interpolation quality when a file rate differs from the output rate. 4 gives the best quality; 1 is fastest. |
| `bit_depth`        | PCM precision for FFmpeg-decoded formats (m4a, aac, alac, opus, wma, webm). 32 uses float PCM and preserves up to 24-bit audio without truncation. Native formats (mp3, flac, wav, ogg) and tracker modules (mod, s3m, xm, it, and other libopenmpt formats) always decode at full precision. This setting does not affect them. |

## Quick recipes

**Lossless / high-resolution setup** (good DAC, high CPU capacity):

```toml
sample_rate = 96000
buffer_ms = 250
resample_quality = 4
bit_depth = 32
```

**Low-latency / low-capacity hardware**:

```toml
sample_rate = 44100
buffer_ms = 200
resample_quality = 1
```

**Unstable radio connection**:

```toml
buffer_ms = 2000
```

This can add up to two seconds of playback latency. It gives the audio device more time to handle short network interruptions.

Live HTTP radio also uses an automatic decoded-audio jitter buffer. If the
network has no data, cliamp outputs silence without blocking the UI. It waits
for a short refill before it resumes. This avoids rapid sound breaks. A source
that stays slower than its audio bitrate can still have silent gaps. cliamp
cannot reconstruct missing audio.

Changes take effect when you next start cliamp.
