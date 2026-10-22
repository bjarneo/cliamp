# Telegram

Play the audio of your own Telegram chats. The provider talks MTProto directly
through [gotd/td](https://github.com/gotd/td): no bot, no daemon and no extra
process.

## Setup

1. Create an application at [my.telegram.org/apps](https://my.telegram.org/apps)
   and copy its `api_id` and `api_hash`. These identify the client; they are not
   a bot token.
2. Add the section to `config.toml`:

```toml
[telegram]
api_id   = 123456
api_hash = "0123456789abcdef0123456789abcdef"
phone    = "+5491100000000"
```

3. Start cliamp. The first run asks for the login code **before** the interface
   takes over the terminal. The code arrives inside the Telegram app, not by
   SMS. Set `password` as well when the account has 2FA.

The session is stored in `~/.local/share/cliamp/telegram.session` and reused
afterwards, so the code is only asked once.

## What it lists

`Telegram` appears in the provider pane with every dialog of the account,
grouped into Bots, Channels, Groups and Chats. A chat loads its history in pages
of 100 messages as you scroll; nothing is fetched ahead of time.

## Playback

Audio documents stream with a small buffer: playback starts once `buffer_mb`
megabytes are on disk and the rest keeps downloading in the background, so a
30 MB FLAC starts in a few seconds instead of waiting for the whole file.

- `buffer_mb` (default `1`) sets how much is buffered before playback.
- `Esc` or `s` cancels the download in flight, removes the partial file and
  resets the player.
- Files are cached under `~/.cache/cliamp/telegram/<chat>/`.

## Cover art

The cover is read from the FLAC's own `PICTURE` block while the audio
downloads, so it costs no extra request, and it is published to MPRIS (lock
screen, desktop widgets) as a `data:` URL.

## Settings

| Key | Required | Meaning |
| --- | --- | --- |
| `api_id` | yes | Application id from my.telegram.org |
| `api_hash` | yes | Application hash from my.telegram.org |
| `phone` | for the first sign-in | International format |
| `password` | no | 2FA password |
| `chat` | no | Show only this chat instead of every dialog |
| `buffer_mb` | no | Megabytes buffered before playback (default 1) |
| `session_file` | no | Session path (default `~/.local/share/cliamp/telegram.session`) |

## Limits

- One account: the provider browses the dialogs of the signed-in user.
- Telegram throttles a single connection to roughly 0.75 MB/s, so large files
  take a while; the buffer hides most of the wait.
- The login code is requested in the terminal before the TUI starts, so the
  first run needs a real terminal.
