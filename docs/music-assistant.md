# Music Assistant Integration

Use Cliamp with a [Music Assistant](https://music-assistant.io/) server. Cliamp browses and searches the server's library, and it joins the server as a speaker. Music Assistant streams to Cliamp over [Sendspin](https://www.sendspin-audio.com/), and Cliamp plays that stream through its own audio engine. The equalizer, visualizers and output device all apply. Cliamp can also play in sync with the other speakers that Music Assistant controls.

## Setup

Create a token in the Music Assistant web UI under **Settings → Profile → Long-lived access tokens**. Then add a `[mass]` block to `~/.config/cliamp/config.toml`:

```toml
[mass]
url   = "http://homeassistant.local:8095"
token = "${MUSIC_ASSISTANT_TOKEN}"
```

`url` is the address of the Music Assistant web UI. The token can be written in the file, or read from the environment with `$VAR` interpolation as above.

Two optional keys:

```toml
[mass]
player_name   = "Office cliamp"  # name of the speaker in Music Assistant (default "cliamp")
sendspin_port = 8927             # Sendspin port on the Music Assistant host (default 8927)
```

### Environment variables

If there is no `[mass]` block with both keys, Cliamp uses environment variables:

```sh
export MUSIC_ASSISTANT_URL="http://homeassistant.local:8095"
export MUSIC_ASSISTANT_TOKEN="..."
```

## How It Works

Cliamp reads the library through the Music Assistant API (`POST /api`). At startup it also connects to the Sendspin server on the same host (port 8927) and registers a speaker named after `player_name`. Startup does not wait for the server: the speaker connects in the background and reconnects if the server restarts.

Music Assistant serves full tracks only to its speakers, so **Music Assistant rows play only while the Sendspin speaker is connected**. Its API offers clients nothing but 30-second previews, and Cliamp does not use them.

Press `H` to open Music Assistant, or set it as the startup provider with `cliamp --provider mass` or `provider = "mass"`. The list starts with the library folders (Recently played, Artists, Albums, Tracks, Genres, Radio stations), followed by your playlists. A folder loads in full, 500 rows at a time, however large the library is.

- **Enter** plays a row. Music Assistant queues it and the rows after it on the Cliamp speaker, so playback continues past the selected row. A folder row can be an artist, album, playlist or radio station; Enter plays all of it.
- **`/`** searches the whole library on the server, not just the rows loaded in Cliamp. Type a query and press Enter: the list shows one row with the matching tracks, then the matching artists, albums and playlists (up to 100 of each). Enter loads one; `Esc` returns to the library.
- **`N`** opens the album browser. **`Ctrl+F`** searches the server, including the music services connected to it.
- While the Music Assistant stream plays, play/pause, next and previous (keys, media keys, remote control) drive the Music Assistant queue. The track title, artist, album, artwork and length come from Music Assistant.
- Playing anything else in Cliamp pauses the Music Assistant queue.
- When Cliamp is idle, a stream that another Music Assistant app starts on the Cliamp speaker plays right away.

### Speakers

Press **`D`** to list the Music Assistant speakers. `d` is still the audio output picker of this computer.

| Key | Action |
|---|---|
| `Space` | Add the highlighted speaker to this computer's group, or take it out |
| `u` | Take this computer out of a group that another speaker leads |
| `Esc`, `q`, `D` | Close |

Speakers in the group play the same stream in sync. `★` marks this computer and `[x]` marks the speakers in its group. A speaker you add starts a few seconds later, when Music Assistant restarts the stream for the group; the others keep playing.

## Limitations

- **No seeking.** Seek in the Music Assistant app; Cliamp follows.
- **Two volumes.** The volume that Music Assistant sets for the Cliamp speaker scales the stream, and Cliamp's own volume applies after it.
- **PCM over the network.** The stream is uncompressed 48 kHz / 16-bit PCM, about 1.5 Mbit/s. Music Assistant resamples to it. Use it on a LAN.

## Security

The token gives full access to the Music Assistant server. Cliamp sends it only in the `Authorization` header of API requests and never logs it. Plain HTTP sends it with every request. Use an `https://` URL if the server is reachable from outside your network.

## Troubleshooting

**The provider does not appear.** Cliamp creates the provider only when both `url` and `token` are set, in `[mass]` or in the environment.

**"the Sendspin player is not connected to Music Assistant".** Cliamp cannot reach the Sendspin server. Check that port 8927 (or `sendspin_port`) on the Music Assistant host is reachable from this computer. Sendspin is built into Music Assistant and on by default. `~/.config/cliamp/cliamp.log` lists each failed connection attempt.

**The first play after startup takes a while.** Music Assistant lists a new Sendspin speaker about 15 seconds after it connects. Cliamp waits for it, up to 25 seconds, and then plays. If the wait runs out, the status line shows "Sendspin player not registered yet"; play the row again.

**The speaker shows up twice in Music Assistant.** Cliamp keeps its Sendspin id in `~/.config/cliamp/sendspin_id`. Deleting that file makes Music Assistant see a new speaker; remove the old one in the Music Assistant settings.
