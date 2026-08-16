# Audio Quality

cliamp lets you tune the output sample rate, speaker buffer size, resample quality, and bit depth via `~/.config/cliamp/config.toml`.

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

Changes take effect on next launch.

## Bit-perfect output (Linux/ALSA)

Bit-perfect mode sends the decoder's samples to the audio device unaltered: no resampling anywhere in cliamp, and the output device is reopened at each track's own sample rate instead of a fixed one. It is currently implemented for ALSA on Linux only; on other platforms (or if the ALSA backend can't be opened) cliamp falls back to the normal output path automatically.

```toml
# Enable bit-perfect output (Linux/ALSA only)
bitperfect = true

# ALSA device to use, e.g. "hw:0,0" for a specific card/device.
# Leave unset to go through the system default (PipeWire/PulseAudio), which
# may still resample internally — point this at a hardware device for a hard
# guarantee.
# bitperfect_device = "hw:0,0"
```

Both are also available as flags: `cliamp --bitperfect --bitperfect-device hw:0,0`, and `--no-bitperfect` to override a config file that enables it.

When enabled, `sample_rate` still matters: it's the rate the device opens at before anything is playing, and the fallback rate if a track's native rate can't be probed or the device refuses it.

`bit_depth` is ignored in bit-perfect mode — FFmpeg-decoded formats (m4a, aac, alac, opus, wma, webm) always decode to 32-bit float so a 24-bit source isn't truncated before it reaches the device.

A **◆ BIT PERFECT** indicator appears next to the playback status when the full chain is bit-exact. That requires all of:

- bit-perfect mode enabled and the device locked to the track's exact native rate (no resampling, no driver-side rate conversion)
- volume at exactly +0 dB
- EQ flat (all ten bands at 0 dB)
- mono downmix off
- playback speed at 1.0x

Any of the above breaks bit-exactness — the indicator simply disappears rather than explaining why, since at that point cliamp is intentionally modifying the signal (that's what volume, EQ, and speed controls are for).

Notes and caveats:

- Local files and buffered network sources (e.g. Navidrome/Subsonic streams) have their native rate probed via `ffprobe` — for a URL this runs concurrently with opening the connection, capped at 2s, so a slow server delays playback by at most that much rather than blocking indefinitely. HTTP/Icecast radio and yt-dlp sources are continuously transcoded already and have no fixed native rate to match, so bit-perfect mode does not apply to them.
- Gapless preloading is skipped across a sample-rate change: switching to a track at a different native rate reopens the device once the current track ends, rather than mid-buffer.
- Going through a sound server (PipeWire, PulseAudio) still gets you the right rate and format, but the server itself can resample under the hood. Point `bitperfect_device` at a `hw:` device for a guarantee that nothing downstream of cliamp touches the samples.
