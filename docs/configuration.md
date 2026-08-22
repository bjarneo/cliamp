# Configuration

For remote providers (Navidrome, Plex, Jellyfin, Emby, Spotify, Qobuz, Tidal, Mixcloud, NetEase, Audiobookshelf, YouTube Music), the fastest path is the interactive wizard:

```sh
cliamp setup
```

It writes the right TOML block without touching the rest of your config and validates server credentials live where the provider supports it (Navidrome, Plex, Jellyfin, Emby). OAuth providers such as Spotify, Qobuz, and Tidal sign in later in the player — Tidal via a `link.tidal.com` device code. Mixcloud's optional browser-session or OAuth credentials are checked when used. See [cli.md](cli.md#setup-wizard) for details.

## Config directory

cliamp resolves its config directory in this order:

- `CLIAMP_CONFIG_DIR`
- `XDG_CONFIG_HOME/cliamp`
- `HOME/.config/cliamp`
- on Windows, `%APPDATA%\cliamp` when `HOME` is not set

The examples below use `~/.config/cliamp` for brevity. On Windows without `HOME`, replace that path with `%APPDATA%\cliamp`.

For everything else, copy the example config and edit by hand:

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

# UI theme name (see available themes in ~/.config/cliamp/themes/)
theme = "Tokyo Night"

# Log level: "debug", "info", "warn", or "error" (default "info")
# Logs are written to ~/.config/cliamp/cliamp.log
log_level = "info"

```

`Stereo` shows true left/right horizontal LED meters with held peak markers.

## Terminal Layout

cliamp adapts its playback screen to the available terminal rectangle:

| Terminal size | Layout |
| --- | --- |
| At least `80x24` | Full controls, five-row visualizer, and detailed source controls |
| At least `56x16` | Compact controls and a three-row visualizer |
| At least `40x10` | Minimal playback, list, seek bar, and help layout |
| Smaller than `40x10` | A resize message only |

`simplified = true` replaces the main playback view with the current track's
artist/title and time and seek-progress strip. It hides the visualizer,
playback controls, and playlist; provider browsing and overlays keep their
normal list-focused layout. Start one session with `cliamp --simplified`.

List-heavy views such as provider browsing, file selection, queues, playlists,
search results, themes, and keybindings use a content-first layout. It replaces
the visualizer and detailed controls with a compact now-playing summary so more
rows remain available for navigation. The visualizer picker keeps its live
preview instead.

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

- Interpolation only happens when the **entire** value is `$NAME` or `${NAME}`. Mixed values like `"p@$$word"` are kept literally — no escaping needed.
- Variable names match `[A-Za-z_][A-Za-z0-9_]*`.
- If the variable is unset, the value is empty (the same as if you had left it blank).
- Works for any string field, including plugin config under `[plugins.<name>]`.

## Default Provider

Set which provider to start with:

```toml
provider = "radio"
```

Valid values: `radio` (default), `navidrome`, `spotify`, `plex`, `jellyfin`, `emby`, `qobuz`, `tidal`, `soundcloud`, `mixcloud`, `netease`, `audiobookshelf`, `yt`, `youtube`, `ytmusic`.

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

## Mixcloud

Mixcloud is opt-in. Public recent releases, popular shows, global show browsing,
the live category catalogue, Latest/Popular genre charts, genre/tag search,
native show search, direct creator jumps, and playback need no account:

```toml
[mixcloud]
enabled = true
```

Add `username` for your following stream, activity, uploads, read-only show
favorites, listening history, collections, and followed-creator browsing. An
optional developer `access_token` makes `/me/` the account identity and adds
Listen Later; `cookies_from` gives yt-dlp your signed-in browser session for
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

The `styles` list is also the provider's local genre-favorites list. In the
**Genres** browser, `/` filters and searches the full tag catalogue, while `f`
atomically adds or removes a style and refreshes its Latest/Popular provider
rows. These favorites do not modify the Mixcloud website account.

See [mixcloud.md](mixcloud.md) for the complete feature matrix, provider-pane
inventory, navigation and keybindings, favorite terminology, OAuth-token setup,
signed-in playback, resume/seeking, and upstream limitations.

## NetEase Cloud Music

NetEase is opt-in and uses your existing browser session. Sign in at `music.163.com`, then run:

```sh
cliamp setup
```

Pick **NetEase Cloud Music** and choose the browser you used to sign in. Common browsers are shown as menu choices; select the custom option only for profile-specific values. The setup wizard validates the session and writes:

```toml
[netease]
enabled = true
cookies_from = "chrome"
user_id = "your-account-user-id"
```

Once enabled, the provider shows your liked songs, created playlists, saved playlists, and public charts. Search works with `Ctrl+F`, and playback uses `yt-dlp` with the same browser cookie source.

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
