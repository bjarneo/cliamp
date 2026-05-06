# Configuration

For remote providers (Navidrome, Plex, Jellyfin, Emby, Spotify, YouTube Music), the fastest path is the interactive wizard:

```sh
cliamp setup
```

It validates your credentials live and writes the right TOML block without touching the rest of your config. See [cli.md](cli.md#setup-wizard) for details.

For everything else, copy the example config and edit by hand:

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

# Initial directory for the file browser ('o' key)
initial_directory = "~/Music"

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
# Options: Bars, BarsDot, Rain, BarsOutline, Bricks, Columns, ClassicPeak, Wave, Scatter, Flame, Retro, Pulse, Matrix, Binary, Sakura, Firework, Bubbles, Logo, Terrain, Scope, Heartbeat, Butterfly, Ascii, Firefly, Mosaic, Sand, Geyser, None
visualizer = "Bars"

# Compact mode: cap UI width at 80 columns (default: fluid/full-width)
compact = false

# UI theme name (see available themes in ~/.config/cliamp/themes/)
theme = "Tokyo Night"

# Log level: "debug", "info", "warn", or "error" (default "info")
# Logs are written to ~/.config/cliamp/cliamp.log
log_level = "info"

```

## Secrets from Environment Variables

Any string value in `config.toml` can be read from an environment variable by setting the value to `$VAR_NAME` or `${VAR_NAME}`. This keeps passwords, tokens, and client secrets out of the file itself.

```toml
[navidrome]
url = "https://music.example.com"
user = "alice"
password = "${NAVIDROME_PASSWORD}"

[plex]
url = "http://plex.local:32400"
token = "$PLEX_TOKEN"

[jellyfin]
url = "https://jelly.example.com"
token = "${JELLYFIN_TOKEN}"

[emby]
url = "https://emby.example.com"
token = "${EMBY_TOKEN}"

[ytmusic]
client_id = "${YTMUSIC_CLIENT_ID}"
client_secret = "${YTMUSIC_CLIENT_SECRET}"
```

Rules:

- Interpolation only happens when the **entire** value is `$NAME` or `${NAME}`. Mixed values like `"p@$$word"` are kept literally — no escaping needed.
- Variable names match `[A-Za-z_][A-Za-z0-9_]*`.
- If the variable is unset, the value is empty (the same as if you had left it blank).
- Works for any string field, including plugin config under `[plugins.<name>]`.

## Default Provider

Set which provider to start with:

```toml
provider = "radio"
```

Valid values: `radio` (default), `navidrome`, `spotify`, `plex`, `jellyfin`, `emby`, `soundcloud`, `yt`, `youtube`, `ytmusic`.

You can also override from the CLI: `cliamp --provider jellyfin`.

## SoundCloud

SoundCloud is opt-in. Add the section to `~/.config/cliamp/config.toml` to register the provider:

```toml
[soundcloud]
enabled = true
```

Once enabled, search works via `Ctrl+F`, pasted SoundCloud URLs play through yt-dlp, and the empty browse view is seeded with a curated set of search-backed genre playlists (**Trending**, **Hip-Hop**, **Electronic**, **House**, **Lo-Fi**, **Indie**, **Pop**) so there's something to explore on first launch.

> SoundCloud's official charts/discover endpoints all 404 through yt-dlp at present, so cliamp can't surface real chart data anonymously. The genre playlists are search-backed (results vary in quality but reflect current uploads).

### Browse a profile

Set a username to expose that profile's tracks, likes, and reposts in the browse view:

```toml
[soundcloud]
enabled = true
user = "yourname"
```

Three playlists appear: **Tracks**, **Likes**, and **Reposts** for `soundcloud.com/yourname`. Works for any public profile.

### Sign in via browser cookies

SoundCloud closed its OAuth program in 2014, so the bring-your-own-client_id pattern Spotify uses isn't available. Instead, point yt-dlp at your existing browser session — it picks up your SoundCloud login from the browser cookie jar:

```toml
[soundcloud]
enabled = true
user = "yourname"
cookies_from = "firefox"   # or chrome, chromium, brave, edge, opera, safari, vivaldi
```

With cookies set, yt-dlp can stream subscriber-gated tracks (SoundCloud Go+) and access private likes/playlists your account is authorized for. The same cookies also apply to the player's yt-dlp invocations, so playback uses your signed-in session.

Requires `yt-dlp` on `PATH`.

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
