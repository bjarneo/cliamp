# SSH Streaming

Play music from a remote machine over SSH without mounting filesystems.

## How It Works

When a track path starts with `ssh://`, cliamp pipes the audio over SSH using the system `ssh` binary:

```
ssh://hostname/absolute/path/to/file.mp3
```

The player runs `ssh hostname cat /path/to/file.mp3` and feeds the output to the audio decoder. No temporary files, no filesystem mounts.

## Creating SSH Playlists

Use `--ssh HOST` with `playlist create` to walk a remote directory:

```sh
cliamp playlist create "Blade Runner" --ssh nas "/Volumes/Music/Blade Runner/"
# Created playlist "Blade Runner" (31 tracks, ssh://nas)
```

This runs `ssh nas find /path -type f -name '*.mp3' ...` to discover audio files, then creates a TOML playlist with `ssh://` prefixed paths.

## TOML Format

SSH playlists look like regular playlists with `ssh://` paths:

```toml
name = "Blade Runner"

[[track]]
path = "ssh://nas/Volumes/Music/Blade Runner/01 - Prologue.mp3"
title = "Prologue And Main Titles"

[[track]]
path = "ssh://nas/Volumes/Music/Blade Runner/02 - Voight Kampff.mp3"
title = "Voight Kampff Test"
```

## SSH Configuration

cliamp uses the system `ssh` binary, which reads `~/.ssh/config`. Host aliases, keys, ports, and ProxyJump all work automatically:

```
# ~/.ssh/config
Host nas
    HostName 192.168.1.50
    User music
    IdentityFile ~/.ssh/nas_key

Host mac-mini-ts
    HostName 100.64.0.5
```

## Supported Formats

SSH streaming works with all formats supported by the native decoders:

- `.mp3` (native decoder)
- `.flac` (native decoder)
- `.ogg` / `.opus` (native decoder)
- `.wav` (native decoder)

Formats requiring ffmpeg (`.m4a`, `.wma`) may not work over SSH since the ffmpeg decoder expects a seekable file.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Host unreachable | Player shows error, advances to next track |
| Auth failure | SSH uses `BatchMode=yes` and never hangs on password prompts |
| Connection drops mid-stream | Player detects EOF, advances to next track |
| Unknown host key | Rejected. Add the host to `~/.ssh/known_hosts` first, or configure in `~/.ssh/config` |

## Mixing Local and SSH Tracks

A single playlist can mix local and SSH paths:

```toml
name = "Mixed"

[[track]]
path = "/local/path/track1.mp3"
title = "Local Track"

[[track]]
path = "ssh://nas/remote/path/track2.mp3"
title = "Remote Track"
```
