# cliamp:// Links

Start a song from a browser, a chat message, a script, or anything else that
can open a URL. Clicking a `cliamp://` link plays or queues its target in your
running cliamp instance, or starts cliamp if it is not running.

## Quick Start

The installer registers the scheme, so links work from a browser right away:

```sh
cliamp open 'cliamp://play?url=https://example.com/stream.mp3'
```

Remove the registration any time with `cliamp protocol unregister`, and put it
back with `cliamp protocol register`.

## URI Format

```
cliamp://<verb>?<target>
```

There are two verbs:

| Verb | Meaning |
| --- | --- |
| `play` | Make the target the thing that is playing now |
| `queue` | Add the target without interrupting playback |

And four targets. Give exactly one:

| Target | Example |
| --- | --- |
| `url` | `cliamp://play?url=https://example.com/stream.mp3` |
| `provider` + `album` | `cliamp://play?provider=navidrome&album=a1b2c3` |
| `provider` + `playlist` | `cliamp://play?provider=jellyfin&playlist=xyz789` |
| `provider` + `q` | `cliamp://play?provider=ytmusic&q=aphex+twin` |

`q` plays the provider's top match for the query.

Percent-encode the target value. A URL with its own query string needs it:

```
cliamp://play?url=https%3A%2F%2Fexample.com%2Fs.mp3%3Ft%3D30
```

## Commands

| Command | Purpose |
| --- | --- |
| `cliamp open <uri>` | Perform a `cliamp://` URI |
| `cliamp protocol register` | Make cliamp the system handler for the scheme |
| `cliamp protocol unregister` | Remove the registration |
| `cliamp protocol status` | Report whether the scheme is registered |

## Running vs Not Running

When cliamp is already running, `cliamp open` sends the action over the IPC
socket and exits immediately.

When nothing is running, it starts the player in the current terminal and
performs the action once the player is up. The desktop entry sets
`Terminal=true` so a link clicked from a browser has somewhere to draw.

## Registration

`install.sh` registers the scheme, writing `cliamp-url-handler.desktop` next
to the `cliamp.desktop` launcher it already installs. The entry is
`NoDisplay=true`, so it handles links without adding a second cliamp icon to
your application menu.

Where it lands follows the install location:

| Install directory | Handler entry |
| --- | --- |
| Under `$HOME` | `~/.local/share/applications/` |
| Anywhere else | `/usr/local/share/applications/` |

`cliamp protocol register` writes the same entry to your user directory. Use
it after a `go install` build, or to point the scheme at a different binary.

`cliamp protocol status` lists every entry it finds, user directory first.
`cliamp protocol unregister` removes the ones you own; a system-wide entry
from a root install is reported with the `sudo rm` needed to remove it, rather
than silently left in place.

Registration is implemented for Linux only. On macOS and Windows,
`cliamp protocol register` reports that it is not implemented yet.
`cliamp open <uri>` works everywhere, so you can still wire the scheme up
through your platform's own handler settings.

A registered handler means any web page can hand cliamp a target with one
click. [What Links Cannot Do](#what-links-cannot-do) is the list of what that
does and does not let a page reach.

## What Links Cannot Do

A `cliamp://` URI is untrusted input: anyone who can get you to click a link
chooses its contents. The handler is built around that.

- **Only `http` and `https` targets.** `ssh://` streams by running the `ssh`
  client against a named host, and `file://` reads local paths. A web page
  does not get to pick either.
- **Two verbs, both about playback.** Plugins, downloads, playlist deletion,
  and history are not reachable from a link. The verb is matched against a
  fixed list, never forwarded as an operation name.
- **Unknown parameters are refused.** A URI with a parameter cliamp does not
  recognize is an error, not something quietly ignored.
- **No local file target.** Naming a local path would let the sending page
  probe your filesystem through error behavior. Use the CLI for local files.
- **Flag-shaped values are refused.** A value starting with `-` is rejected
  before it can reach an external program as an option.
- **Entries from remote playlists are filtered.** When a link points at an
  `.m3u` or `.pls`, entries naming a scheme other than `http` or `https` are
  dropped, so the playlist's author cannot pick which program cliamp runs.
- **Third-party directory entries are filtered.** The radio catalog comes from
  Radio Browser, a public directory that accepts submissions from anyone.
  Station URLs that are not `http` or `https` are dropped when they are
  fetched, so a submitted station cannot name a transport.
- **Provider results are re-checked.** A `q` search plays what the provider
  returns, so that path is checked again before playback. A provider's own
  URIs are fine (Spotify returns `spotify:track:...`), but a result naming
  the `ssh` transport or a local file is refused.

Private and loopback addresses are allowed on purpose: streaming from a
Navidrome or Jellyfin server on your own network is the common case.

## Examples

```sh
# A radio stream
cliamp open 'cliamp://play?url=https://stream.example/live.mp3'

# Queue a podcast episode without interrupting what is playing
cliamp open 'cliamp://queue?url=https://example.com/ep42.mp3'

# An album from a configured provider
cliamp open 'cliamp://play?provider=navidrome&album=a1b2c3'

# Top search result from YouTube Music
cliamp open 'cliamp://play?provider=ytmusic&q=boards+of+canada'
```

In HTML:

```html
<a href="cliamp://play?url=https://stream.example/live.mp3">Play in cliamp</a>
```

## Limitations

`queue` works with `url` and `q` targets. Queueing a provider album or
playlist is not supported yet; use `cliamp://play` for those.

`cliamp protocol register` is Linux only for now. macOS needs an app bundle,
because the OS delivers a URL as an Apple Event rather than as an argument,
and Windows needs registry keys.

## See Also

- [CLI Flags](cli.md) for the full command list
- [Remote Control (IPC)](remote-control.md) for the socket protocol underneath
