# SSH Streaming

Play music from a remote machine through SSH. You do not need to mount a filesystem.

## How It Works

When a track path starts with `ssh://`, cliamp pipes audio through SSH with the system `ssh` binary:

```
ssh://hostname/absolute/path/to/file.mp3
```

The player runs `ssh hostname cat /path/to/file.mp3` and sends the output to the audio decoder. It uses no temporary files or filesystem mounts.

## Creating SSH Playlists

Use `--ssh HOST` with `playlist create` to scan a remote directory:

```sh
cliamp playlist create "Blade Runner" --ssh nas "/Volumes/Music/Blade Runner/"
# Created playlist "Blade Runner" (31 tracks, ssh://nas)
```

This runs `ssh nas find /path -type f -name '*.mp3' ...` to find audio files. It then creates a TOML playlist with `ssh://` path prefixes.

## TOML Format

SSH playlists use regular playlist format with `ssh://` paths:

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

cliamp uses the system `ssh` binary. It reads `~/.ssh/config`. Host aliases, keys, ports, and ProxyJump work automatically:

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

SSH streaming supports formats that native decoders support:

- `.mp3` (native decoder)
- `.flac` (native decoder)
- `.ogg` / `.opus` (native decoder)
- `.wav` (native decoder)

Formats that require ffmpeg (`.m4a`, `.wma`) might not work over SSH because the ffmpeg decoder expects a seekable file.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Host unreachable | The player shows an error and advances to the next track. |
| Auth failure | SSH uses `BatchMode=yes` and does not wait for password prompts. |
| Connection drops mid-stream | The player detects EOF and advances to the next track. |
| Unknown host key | The connection is rejected. Add the host to `~/.ssh/known_hosts` first, or configure it in `~/.ssh/config`. |

## Mixing Local and SSH Tracks

A playlist can contain local and SSH paths:

```toml
name = "Mixed"

[[track]]
path = "/local/path/track1.mp3"
title = "Local Track"

[[track]]
path = "ssh://nas/remote/path/track2.mp3"
title = "Remote Track"
```
