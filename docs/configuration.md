# Configuration

Use the interactive wizard to configure remote providers. Supported providers are Navidrome, Lyrion, Plex, Jellyfin, Emby, Spotify, Qobuz, Tidal, Mixcloud, NetEase, Audiobookshelf, and YouTube Music:

```sh
cliamp setup
```

The wizard writes the required TOML block and leaves the rest of your config unchanged. It validates server credentials during setup when the provider supports it: Navidrome, Lyrion, Plex, Jellyfin, and Emby. OAuth providers such as Spotify, Qobuz, and Tidal sign in later in the player. Tidal uses a `link.tidal.com` device code. Mixcloud checks optional browser-session or OAuth credentials when you use them. See [cli.md](cli.md#setup-wizard) for details.

## Config directory

cliamp searches for its config directory in this order:

- `CLIAMP_CONFIG_DIR`
- `XDG_CONFIG_HOME/cliamp`
- `HOME/.config/cliamp`
- on Windows, `%APPDATA%\cliamp` when `HOME` is not set

The examples below use `~/.config/cliamp`. On Windows without `HOME`, use `%APPDATA%\cliamp` instead.

For other settings, copy and edit the example config:

```sh
mkdir -p ~/.config/cliamp
cp config.toml.example ~/.config/cliamp/config.toml
```

## Options

```toml
# Default volume in dB (range: volume_min to 6)
volume = 0

# Minimum volume floor in dB (range: -90 to 0, default: -50)
# Controls how low the volume control can go.
volume_min = -50

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
# Saved Custom curve; applied when eq_preset is "Custom" or empty
eq = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0]

# Manual EQ changes update this curve automatically. Cycling presets with e
# keeps it available, and both values are restored after restart.

# Visualizer mode (leave empty for default Bars)
# Options: Bars, BarsDot, Rain, BarsOutline, Bricks, Columns, ClassicPeak, Wave, Scatter, Flame, Retro, Pulse, Matrix, Binary, Sakura, Firework, Bubbles, Logo, Terrain, Scope, Heartbeat, Butterfly, Ascii, Firefly, Mosaic, Sand, Geyser, ClassicLED, Stereo, Mirror, None
# Mirror draws tapered Braille bars around a persistent horizontal center axis.
visualizer = "Bars"

# Visualizer volume linking (default: true)
# When true, bar height follows the current volume level (classic behavior).
# Set to false to decouple the visualizer from volume — bars stay visible
# even at very low volume levels.
vis_volume_linked = true

# Reduce CPU usage by lowering UI cadence and disabling visualization.
# This has the same effect as starting with --low-power.
low_power = false

# Simplified mode: artist/title and time strip without a visualizer or playlist.
# No visualizer or playback controls are shown.
simplified = false

# Hide the key-binding hint bar above the status line.
hide_help_bar = false

# UI theme name (see available themes in ~/.config/cliamp/themes/)
theme = "Tokyo Night"

# Log level: "debug", "info", "warn", or "error" (default "info")
# Logs are written to ~/.config/cliamp/cliamp.log
log_level = "info"

```

`Stereo` shows separate left and right horizontal LED meters with held peak markers.

## Terminal Layout

cliamp adapts its playback screen to the terminal size:

| Terminal size | Layout |
| --- | --- |
| At least `80x24` | Full controls, five visualizer rows, and detailed source controls |
| At least `56x16` | Compact controls and three visualizer rows |
| At least `40x10` | Minimal playback, list, seek bar, and help layout |
| Smaller than `40x10` | Resize message only |

`simplified = true` replaces the main playback view with the current track
artist/title, time, and seek-progress strip. It hides the visualizer, playback
controls, and playlist. Provider browsing and overlays keep their list-focused
layout. Start one session with `cliamp --simplified`.

`hide_help_bar = true` removes the key-binding hint bar above the status line
and gives that row back to the playlist. The full keymap stays available with
`?`, and `Ctrl+G` toggles the bar for the current session. Start one session
with `cliamp --no-help-bar`. Simplified mode draws neither the hint bar nor a
playlist, so it is unaffected by this setting.

List views such as provider browsing, file selection, queues, playlists, search
results, themes, and keybindings use a content-first layout. This layout replaces
the visualizer and detailed controls with a compact now-playing summary. It leaves
more rows for navigation. The visualizer picker keeps its live preview.

## Secrets from Environment Variables

Set a string value in `config.toml` to `$VAR_NAME` or `${VAR_NAME}` to read it from an environment variable. This keeps passwords, tokens, and client secrets out of the file.

```toml
[navidrome]
url = "https://music.example.com"
user = "alice"
password = "${NAVIDROME_PASSWORD}"

[lyrion]
url = "http://nas.local:9000"
user = "alice"
password = "${LYRION_PASSWORD}"
# show_unplayable = true  # include plugin-contributed tracks and playlists

[plex]
url = "http://plex.local:32400"
token = "$PLEX_TOKEN"

[jellyfin]
url = "https://jelly.example.com"
token = "${JELLYFIN_TOKEN}"

[emby]
url = "https://emby.example.com"
token = "${EMBY_TOKEN}"

[audiobookshelf]
url = "https://abs.example.com"
token = "${AUDIOBOOKSHELF_TOKEN}"

[ytmusic]
client_id = "${YTMUSIC_CLIENT_ID}"
client_secret = "${YTMUSIC_CLIENT_SECRET}"
# Optional: resolve full playlists from list= URLs (default true). Set to false to strip playlist params.
# expand_playlist = true

[mixcloud]
access_token = "${MIXCLOUD_ACCESS_TOKEN}"
```

Rules:

- Interpolation occurs only when the **entire** value is `$NAME` or `${NAME}`. cliamp keeps mixed values such as `"p@$$word"` literally. No escaping is required.
- Variable names match `[A-Za-z_][A-Za-z0-9_]*`.
- If the variable is unset, the value is empty (the same as if you had left it blank).
- Works for any string field, including plugin config under `[plugins.<name>]`.

## Default Provider

Set the provider that cliamp opens at start:

```toml
provider = "radio"
```

Valid values: `radio` (default), `navidrome`, `lyrion`, `spotify`, `plex`, `jellyfin`, `emby`, `qobuz`, `tidal`, `soundcloud`, `mixcloud`, `netease`, `audiobookshelf`, `yt`, `youtube`, `ytmusic`.

You can also override this setting on the CLI: `cliamp --provider jellyfin`.

## SoundCloud

SoundCloud is optional. Add this section to `~/.config/cliamp/config.toml` to register the provider:

```toml
[soundcloud]
enabled = true
```

After you enable SoundCloud, use `Ctrl+F` to search. Pasted SoundCloud URLs play through yt-dlp. The empty browse view contains search-backed genre playlists: **Trending**, **Hip-Hop**, **Electronic**, **House**, **Lo-Fi**, **Indie**, and **Pop**.

> SoundCloud official chart and discover endpoints return 404 through yt-dlp. cliamp cannot show anonymous real chart data. The genre playlists use search results. Result quality varies but reflects current uploads.

### Browse a profile

Set a username to show that profile tracks, likes, and reposts in the browse view:

```toml
[soundcloud]
enabled = true
user = "yourname"
```

Three playlists appear for `soundcloud.com/yourname`: **Tracks**, **Likes**, and **Reposts**. This works for any public profile.

### Sign in via browser cookies

SoundCloud closed its OAuth program in 2014. The bring-your-own-client_id method that Spotify uses is not available. Instead, point yt-dlp to an existing browser session. It reads your SoundCloud login from the browser cookie jar:

```toml
[soundcloud]
enabled = true
user = "yourname"
cookies_from = "firefox"   # or chrome, chromium, brave, edge, opera, safari, vivaldi
```

With cookies set, yt-dlp can stream subscriber-gated tracks (SoundCloud Go+) and access private likes and playlists that your account can access. The same cookies apply to player yt-dlp calls. Playback uses your signed-in session.

Requires `yt-dlp` on `PATH`.

## Mixcloud

Mixcloud is optional. Public recent releases, popular shows, global show browsing,
the live category catalog, Latest/Popular genre charts, genre and tag search,
native show search, direct creator jumps, and playback need no account:

```toml
[mixcloud]
enabled = true
```

Add `username` for your following stream, activity, uploads, read-only show
favorites, listening history, collections, and followed-creator browsing. An
optional developer `access_token` sets `/me/` as the account identity and adds
Listen Later. `cookies_from` gives yt-dlp your signed-in browser session for
playback that requires it.

```toml
[mixcloud]
enabled = true
username = "yourname"
access_token = "${MIXCLOUD_ACCESS_TOKEN}"
cookies_from = "firefox"
styles = ["ambient", "deep-house", "jazz", "techno"]
max_items = 100
stream_creators = 20
```

The `styles` list is also the local genre-favorites list for the provider. In
the **Genres** browser, `/` filters and searches the complete tag catalog. `f`
adds or removes a style as one action and refreshes its Latest/Popular provider
rows. These favorites do not change the Mixcloud website account.

See [mixcloud.md](mixcloud.md) for the feature matrix, provider-pane inventory,
navigation and keybindings, favorite terminology, OAuth-token setup, signed-in
playback, resume, seeking, and upstream limitations.

## NetEase Cloud Music

NetEase is optional and uses an existing browser session. Sign in at `music.163.com`, then run:

```sh
cliamp setup
```

Select **NetEase Cloud Music** and the browser that you used to sign in. The menu lists common browsers. Select the custom option only for profile-specific values. The setup wizard validates the session and writes:

```toml
[netease]
enabled = true
cookies_from = "chrome"
user_id = "your-account-user-id"
```

After you enable NetEase, the provider shows liked songs, created playlists, saved playlists, and public charts. Use `Ctrl+F` to search. Playback uses `yt-dlp` with the same browser cookie source.

## Radio

The Radio provider is always on. The `[radio]` block only tunes it:

```toml
[radio]
country = "NO"
```

`country` is your home country as an ISO 3166-1 alpha-2 code. It puts a "near you" row at the top of the radio pane and offers that country's regions in the country browser.

Leave it unset and cliamp does not work out where you are. It offers instead: the radio pane shows a "Use my location" row, and only if you accept does it read your country from the system timezone, then the locale, with no network call. Your answer is written here either way, so you are asked once. Set it to `"none"` to decline up front. See [radio.md](radio.md#using-your-location).

Pinned countries live in `~/.config/cliamp/radio_countries.toml` and are written by pressing `f` in the country browser, so you do not normally edit that file by hand.

## Custom Radio Stations

Add stations to `~/.config/cliamp/radios.toml`:

```toml
[[station]]
name = "Jazz FM"
url = "https://jazz.example.com/stream"

[[station]]
name = "Ambient Radio"
url = "https://ambient.example.com/stream.m3u"
```

These stations appear with the built-in cliamp radio in the Radio provider.

See [audio-quality.md](audio-quality.md) for sample rate, buffer, bit depth, and resample quality settings.

## WSL2 (Windows Subsystem for Linux)

cliamp uses ALSA for audio on Linux. WSL2 does not expose ALSA hardware directly. WSLg provides a PulseAudio server that ALSA can use.

If you see `ALSA lib pcm.c: Unknown PCM default`, use these two steps:

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

WSLg must be active. `echo $PULSE_SERVER` should print a path. If it is empty, use Windows 11 with WSLg enabled. Run `wsl --shutdown`, then reopen the terminal.

## ffmpeg (optional)

AAC, ALAC (`.m4a`), Opus, and WMA playback require [ffmpeg](https://ffmpeg.org/):

```sh
# Arch
sudo pacman -S ffmpeg
# Debian/Ubuntu
sudo apt install ffmpeg
# macOS
brew install ffmpeg
```

MP3, WAV, FLAC, and OGG work without ffmpeg.
