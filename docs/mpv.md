# MPV playback backend

cliamp can use one persistent [MPV](https://mpv.io/) process as an optional
playback backend. The native backend remains the default and is unchanged.
cliamp still owns the playlist, queue, repeat, shuffle, next/previous, metadata,
TUI, daemon, and remote-control state. MPV receives one track at a time over its
newline-delimited JSON IPC socket.

## Install and run

Install `mpv` with your operating system's package manager and keep it on
`PATH`. cliamp does not silently fall back to native playback if MPV is missing
or fails to start.

```sh
cliamp --audio-backend native ~/Music

cliamp \
  --audio-backend mpv \
  --audio-device 'alsa/hw:CARD=Generic,DEV=0' \
  --bit-perfect \
  ~/Music
```

Equivalent persistent configuration:

```toml
audio_backend = "mpv"
audio_device = "alsa/hw:CARD=Generic,DEV=0"
bit_perfect = true
```

MPV receives the `audio_device` value exactly. cliamp does not convert `hw:` to
`default`, PipeWire, PulseAudio, or `plughw:`.

## What bit-perfect mode means

`bit_perfect = true` configures a bit-perfect-capable path. It:

- requires an explicit direct MPV ALSA device beginning with `alsa/hw:`;
- locks software volume to 0 dB / MPV volume 100;
- locks speed to 1.0x;
- disables MPV configuration files, audio filters, ReplayGain, pitch
  correction, and normalization;
- rejects mono and non-flat EQ configuration;
- does not force an output rate or PCM format, so MPV and ALSA can negotiate
  each source natively.

cliamp intentionally does not force `S16_LE`, `S24_LE`, or a fixed sample rate.
24-bit audio carried as `S32_LE` is normal for hardware such as HDA codecs.

"Bit-perfect mode enabled" is configuration status, not proof of the complete
hardware path. Mixer controls, ALSA plugins, drivers, firmware, and the DAC are
outside cliamp. Confirm that source and output rates match and inspect MPV/ALSA
logs before claiming end-to-end bit-perfect output.

Conflicting startup configuration is rejected with a clear error. While this
mode is active, volume, speed, EQ, and mono changes from the TUI, daemon, IPC,
MPRIS, or Lua plugins are rejected or ignored with a warning.

## PipeWire reservation on Linux

PipeWire may own the ALSA PCM node and prevent MPV from opening `hw:`. cliamp
never kills PipeWire or stops its services. You can release the card manually:

```sh
pw-reserve -n Audio2 -r
```

Or ask cliamp to own that helper for its process lifetime:

```sh
cliamp --audio-backend mpv \
  --audio-device 'alsa/hw:CARD=Generic,DEV=0' \
  --audio-reservation Audio2 \
  ~/Music
```

Persistent configuration:

```toml
audio_reservation = "Audio2"
```

This optional feature is Linux-only and requires `pw-reserve` on `PATH`.
Reservation names follow the `AudioN` convention, but the mapping from an ALSA
card name such as `Generic` to `Audio2` is not reliably derivable. cliamp
therefore requires the explicit reservation name and makes no guess. The helper
is terminated and the reservation released when cliamp exits.

## Unsupported MPV-mode features

The first implementation deliberately reports these as unavailable rather than
faking them:

- cliamp EQ and mono conversion;
- PCM visualizers and plugin visualizers;
- native sample-rate, resampler-quality, bit-depth, and speaker-buffer controls;
- gapless preloading;
- Spotify playback through cliamp's native Spotify engine;
- provider sources that require cliamp's segmented native decoder.

Normal MPV mode supports MPV software volume and playback speed. Bit-perfect
mode locks both. Local files, direct URLs, and MPV-supported yt-dlp URLs use MPV.
Provider browsing, playlists, queueing, shuffle, repeat, metadata, TUI controls,
daemon control, and JSON IPC remain owned by cliamp.

## Status and logs

The track-information overlay and `cliamp status` expose the backend, configured
device, source/output format and rate when MPV reports them, DSP-disabled state,
and whether the observed output is direct ALSA. This status avoids an
unqualified `BIT PERFECT` claim.

Set `--log-level debug` to record the MPV command line, IPC connection and
requests, MPV errors, file and end events, audio parameters, shutdown, and
reservation lifetime in `~/.config/cliamp/cliamp.log`.

## Manual Linux verification

1. Find the card and device:

   ```sh
   aplay -l
   ```

2. If PipeWire owns the target, reserve it in a second terminal. For the
   example machine:

   ```sh
   pw-reserve -n Audio2 -r
   ```

3. Confirm no process owns the PCM node:

   ```sh
   fuser -v /dev/snd/pcmC2D0p
   ```

   Expected: no process.

4. Test MPV directly:

   ```sh
   mpv -v \
     --audio-device='alsa/hw:CARD=Generic,DEV=0' \
     --no-video \
     song.flac
   ```

   A 48 kHz, 24-bit file may correctly report:

   ```text
   requested format: 48000 Hz, stereo channels, s32
   opening device 'hw:CARD=Generic,DEV=0'
   AO: [alsa] 48000Hz stereo 2ch s32
   ```

5. Test cliamp:

   ```sh
   cliamp \
     --audio-backend mpv \
     --audio-device 'alsa/hw:CARD=Generic,DEV=0' \
     --bit-perfect \
     song.flac
   ```

6. Verify TUI playback, pause/resume, seeking, next/previous, queue changes,
   duration/position, repeat, shuffle, and end-of-track advancement. Test
   `16/44.1`, `16/48`, `24/44.1`, `24/48`, `24/96`, and `24/192` files. Confirm
   the output rate follows each source instead of being forced to 192 kHz.

7. Exit cliamp. Confirm MPV, the runtime socket, and any `pw-reserve` helper are
   gone, then confirm PipeWire can reclaim the card.

The MPV IPC and audio-device syntax used here follow the
[official MPV manual](https://mpv.io/manual/stable/). The reservation helper is
documented by [PipeWire](https://docs.pipewire.org/page_man_pw-reserve_1.html).
