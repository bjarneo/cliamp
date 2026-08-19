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

**`bitperfect_device` is required — a `hw:...` or `plughw:...` device, not left unset.** `bitperfect = true` on its own (going through the system default, i.e. PipeWire/PulseAudio) is not a supported way to get bit-perfect output: a sound server mixes multiple clients into one shared graph, there's no fixed hardware device for cliamp to verify against, and the **◆ BIT PERFECT** badge will never light up that way regardless of what rate the negotiation reports. See ["Why not the default device?"](#why-not-the-default-device) below for the full reason. cliamp warns at startup if bit-perfect mode is on without a hardware device configured.

```toml
# Enable bit-perfect output (Linux/ALSA only)
bitperfect = true

# Required for bit-perfect to actually work: a specific hardware device,
# e.g. "hw:0,0" or "plughw:0,0". Find candidates with `aplay -l`, or use a
# stable name form like "hw:K6,0" (card name) instead of a numeric index,
# which can shift across reboots/reconnects. Leaving this unset does not
# give you bit-perfect output — see above.
bitperfect_device = "hw:0,0"
```

Both are also available as flags: `cliamp --bitperfect --bitperfect-device hw:0,0`, and `--no-bitperfect` to override a config file that enables it.

### Multichannel interfaces

Some audio interfaces have no native stereo mode at all — their one PCM substream only accepts a fixed, larger channel count (e.g. a 6-output interface used as a stereo DAC). `aplay -l` shows one subdevice either way; the tell is `speaker-test -D hw:N,0 -c 2 ...` failing with "Channels count (2) not available" while some larger count (found by trying `-c 1` through `-c 8`) succeeds. Point `bitperfect_channels` at whichever physical pair should carry left/right, as 0-based indices:

```toml
bitperfect_device = "hw:0,0"
bitperfect_channels = "0,1"   # the first channel pair; "2,3" for the next, etc.
```

Or as a flag: `--bitperfect-channels 0,1`. cliamp widens its channel request to whatever the device needs to fit the pair you chose, and writes silence to every other channel.

**This requires `bitperfect_device` to be a raw `hw:` device, not `plughw:`.** `plughw:`'s own channel-conversion layer will silently accept a plain 2-channel request too (that's its job) and remap it however its internal heuristic decides, without cliamp having any way to confirm where left/right actually landed — the same reason `plughw:` can't be fully trusted for rate exactness applies here. Through `hw:`, a request for fewer channels than the hardware needs simply fails, which is what lets cliamp force the negotiation wide enough and then verify the result (same `/proc/asound/.../hw_params` check described below, extended to also confirm channel count and sample format, not just rate). cliamp warns at startup if `bitperfect_channels` is set without a raw `hw:` device.

When `bitperfect_device` points at real hardware (`hw:...`/`plughw:...`), cliamp asks the desktop's D-Bus device-reservation service for temporary exclusive access before opening it — the same protocol JACK has long used to borrow a card from PulseAudio/PipeWire, and what WirePlumber implements today. The device is handed back automatically as soon as cliamp closes it, so it's free for other apps the rest of the time; no permanent config or manual toggling needed. This is best-effort: without a session D-Bus bus, or with a sound server that doesn't support the protocol, cliamp behaves exactly as it did before and may still report the device as busy.

When enabled, `sample_rate` still matters: it's the rate the device opens at before anything is playing, and the fallback rate if a track's native rate can't be probed or the device refuses it.

`bit_depth` is ignored in bit-perfect mode — FFmpeg-decoded formats (m4a, aac, alac, opus, wma, webm) always decode to 32-bit float so a 24-bit source isn't truncated before it reaches the device.

A **◆ BIT PERFECT** indicator appears next to the playback status when the full chain is bit-exact, with the device's sample rate shown right after it (e.g. `◆ BIT PERFECT 96kHz`). That requires all of:

- bit-perfect mode enabled, `bitperfect_device` set to a `hw:...`/`plughw:...` device, and the device confirmed locked to the track's exact native rate, sample format, and channel count (no resampling, no driver-side conversion of any of the three)
- volume at exactly +0 dB
- EQ flat (all ten bands at 0 dB)
- mono downmix off
- playback speed at 1.0x

Any of the above breaks bit-exactness — the indicator simply disappears rather than explaining why, since at that point cliamp is intentionally modifying the signal (that's what volume, EQ, and speed controls are for). When it's dark, a dim sample-rate readout still shows next to the playback status whenever bit-perfect mode is on and a rate is known — either the device's current rate, or `source→device` (e.g. `192→96kHz`) when they don't match — so you can see what's actually happening without an external tool.

`hw:...` and `plughw:...` are both eligible for the badge, but verified differently. `hw:` has no conversion capability at all: a request either matches the hardware exactly or fails outright, so its own report of an exact match is trustworthy on its own. `plughw:` wraps the same hardware in ALSA's automatic format/channel/rate conversion layer, entirely in userspace — it can silently resample, reformat, or channel-map to satisfy a request it can't actually honor natively and still report success, so cliamp doesn't take its word for it: it separately confirms the rate, sample format, and channel count against the kernel's own view of the underlying hardware substream (`/proc/asound/.../hw_params`, the same file described below), and only lights up the badge when that independently confirms an exact match on all three. This means a device that only works through `plughw:` (e.g. it exposes more channels than cliamp requests, so a raw `hw:` open fails on channel count) can still earn the badge for the rates it genuinely supports natively — it just won't for a rate the hardware doesn't support, even though the `plughw:` open itself succeeds by silently resampling. A device requiring `bitperfect_channels` to reach at all (see above) can only ever earn the badge through `hw:`, since `plughw:` will accept the narrower default channel request without ever forcing the widening cliamp needs to verify against.

Notes and caveats:

- Local files and buffered network sources (e.g. Navidrome/Subsonic streams) have their native rate probed via `ffprobe` — for a URL this runs concurrently with opening the connection, capped at 2s, so a slow server delays playback by at most that much rather than blocking indefinitely. HTTP/Icecast radio and yt-dlp sources are continuously transcoded already and have no fixed native rate to match, so bit-perfect mode does not apply to them.
- Gapless preloading is skipped across a sample-rate change: switching to a track at a different native rate reopens the device once the current track ends, rather than mid-buffer.
- Going through `plughw:` still gets you the requested rate and format at the client end, but a conversion layer sits between cliamp and the physical hardware — cliamp accounts for this by independently confirming the real rate (see above), so the badge/readout stay accurate, but you can check it yourself too: read the kernel's live view of the open PCM substream directly, e.g. `cat /proc/asound/card0/pcm0p/sub0/hw_params` while a track is playing (card/device numbers from `aplay -l`). A sound server (PipeWire, PulseAudio, `default`) is different: there's no fixed card/device to inspect this way, so cliamp has no way to independently verify it, and the badge never lights up there regardless of what the negotiation reports. Point `bitperfect_device` at a `hw:`/`plughw:` device for a verifiable result.

### Why not the default device?

`bitperfect = true` without `bitperfect_device` still plays audio — through the system default (PipeWire/PulseAudio) — but it can never be verified bit-perfect, for reasons that don't have a workaround at cliamp's level:

- **No fixed hardware to check.** `hw:`/`plughw:` map to one specific kernel PCM substream, which is what makes independent verification possible (see above). `default` isn't a hardware device at all — it's a virtual PCM handed off to a sound server over a socket. Which physical card that resolves to is decided by the sound server's own session policy and can change while cliamp is running (e.g. you switch output devices); there's no fixed `/proc/asound/cardN/...` path for cliamp to check.
- **It's a shared, multi-client graph.** Other applications can be feeding the same hardware sink at the same time. The sound server gives each client its own converter before mixing, so even if the final hardware output happened to land on the exact rate you want, that doesn't prove *cliamp's own stream* wasn't resampled on the way in — unlike `hw:`/`plughw:`, where cliamp is the only client touching that PCM substream (it asks for, and gets, temporary exclusive access — see above).
- **The graph's rate is a policy choice, not a fact to discover.** PipeWire in particular is commonly configured with a fixed shared clock rate (`default.clock.rate` in `pipewire.conf`) that every concurrent client has to live with — if your hardware and your files support higher rates but the shared clock doesn't, PipeWire is resampling by design, and no amount of inspection changes that outcome.

If you want bit-perfect output, `bitperfect_device` pointed at your actual hardware is not optional — it's the only path that gives cliamp sole ownership of the device and a fixed point to verify against.
