# Configuration

Copy the example config to get started:

```sh
mkdir -p ~/.config/cliamp
cp config.toml.example ~/.config/cliamp/config.toml
```

## Options

```toml
# Default volume in dB (range: -30 to 6)
volume = 0

# Repeat mode: "off", "all", or "one"
repeat = "off"

# Start with shuffle enabled
shuffle = false

# Start with mono output (L+R downmix)
mono = false

# Shift+Left/Right seek jump in seconds
seek_large_step_sec = 30

# EQ preset: "Flat", "Rock", "Pop", "Jazz", "Classical",
#             "Bass Boost", "Treble Boost", "Vocal", "Electronic", "Acoustic"
# Leave empty or "Custom" to use manual eq values below
eq_preset = "Flat"

# 10-band EQ gains in dB (range: -12 to 12)
# Bands: 70Hz, 180Hz, 320Hz, 600Hz, 1kHz, 3kHz, 6kHz, 12kHz, 14kHz, 16kHz
# Only used when eq_preset is "Custom" or empty
eq = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0]

# Visualizer mode (leave empty for default Bars)
# Options: Bars, Bricks, Columns, Wave, Scatter, Flame, Retro, None
visualizer = "Bars"

# Compact mode: cap UI width at 80 columns (default: fluid/full-width)
compact = false

# UI theme name (see available themes in ~/.config/cliamp/themes/)
theme = "Tokyo Night"

```

## Default Provider

Set which provider to start with:

```toml
provider = "radio"
```

Valid values: `radio` (default), `navidrome`, `spotify`.

You can also override from the CLI: `cliamp --provider navidrome`.

## Custom Radio Stations

Add your own stations to `~/.config/cliamp/radios.toml`:

```toml
[[station]]
name = "Jazz FM"
url = "https://jazz.example.com/stream"

[[station]]
name = "Ambient Radio"
url = "https://ambient.example.com/stream.m3u"
```

These appear alongside the built-in cliamp radio in the Radio provider.

See [audio-quality.md](audio-quality.md) for sample rate, buffer, bit depth, and resample quality settings.

## WSL2 (Windows Subsystem for Linux)

cliamp uses ALSA for audio on Linux. WSL2 doesn't expose ALSA hardware directly, but WSLg provides a PulseAudio server that ALSA can route through.

If you see errors like `ALSA lib pcm.c: Unknown PCM default`, fix it with two steps:

**1. Install the ALSA PulseAudio plugin:**

```sh
sudo apt install libasound2-plugins
```

**2. Create `~/.asoundrc` to route ALSA through PulseAudio:**

```sh
cat > ~/.asoundrc << 'EOF'
pcm.default pulse
ctl.default pulse
EOF
```

WSLg must be active (`echo $PULSE_SERVER` should print a path). If it's empty, ensure you're on Windows 11 with WSLg enabled and run `wsl --shutdown` then reopen your terminal.

## ffmpeg (optional)

AAC, ALAC (`.m4a`), Opus, and WMA playback requires [ffmpeg](https://ffmpeg.org/):

```sh
# Arch
sudo pacman -S ffmpeg
# Debian/Ubuntu
sudo apt install ffmpeg
# macOS
brew install ffmpeg
```

MP3, WAV, FLAC, and OGG work without ffmpeg.
