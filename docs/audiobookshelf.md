# Audiobookshelf

cliamp can play audiobooks and podcasts from a self-hosted [Audiobookshelf](https://www.audiobookshelf.org/) server. It browses your authors and their books, lists your podcast shows, and keeps listening position in sync with the server as you play.

> **Quick start:** run `cliamp setup` for a guided TUI that lets you pick API-key or username+password auth, validates the connection, and writes the `[audiobookshelf]` block for you. Manual setup steps are below.

## Prerequisites

- A reachable Audiobookshelf server
- An Audiobookshelf API key, or a username and password

## Configuration

Add an `[audiobookshelf]` section to `~/.config/cliamp/config.toml`:

```toml
[audiobookshelf]
url = "https://abs.example.com"
token = "your-api-key"
# or authenticate with credentials instead of a key:
# user = "listener"
# password = "secret"
# Restrict cliamp to these libraries (default: all):
# libraries = ["Audiobooks", "Podcasts"]
```

| Key | Description |
|-----|-------------|
| `url` | Base URL of your Audiobookshelf server |
| `token` | API key — create one in the Audiobookshelf web UI under Settings → Users → API Keys. Preferred over username/password. |
| `user` | Username for password-based login, used when `token` is not set |
| `password` | Password for password-based login |
| `libraries` | Optional list of library names to restrict cliamp to (default: all) |

With `token` set, cliamp authenticates immediately. With `user` and `password` instead, it logs in lazily on first use.

## Usage

Once configured, **Audiobookshelf** appears as a provider alongside Radio, Navidrome, Plex, Jellyfin, Emby, and the rest. Press `B` anywhere in the UI to switch to it, or start cliamp with it selected:

```bash
cliamp --provider audiobookshelf
# or the shorter alias
cliamp --provider abs
```

Or set it in config:

```toml
provider = "audiobookshelf"
```

The provider pane lists two sections:

```text
── Audiobooks ──
...every book...
── Podcasts ──
...every podcast show...
```

Selecting a book queues one track per audio file. Selecting a podcast show queues one track per episode, newest first.

Press `N` to open the browse overlay: Authors → Books → tracks (this provider labels the two levels "Authors" and "Books" instead of the usual Artists/Albums). The album-list view supports the usual sort cycle: By Title (default), Recently Added, By Author.

Press `Ctrl+F` to search the configured libraries. Each matching book or podcast show is expanded into its tracks, capped at the search result limit. Search is best-effort: if the server can't return a matched item's tracks, that item is skipped and the results are simply shorter — a partial result is not an error.

## Track titles

A file's title is the chapter name when that file covers exactly one chapter. Otherwise it falls back to the file's embedded title, then its filename. Artist is set to the book's author and album to the book's title. Podcast episodes use the episode title.

## Progress and resume

cliamp reports listening position back to the server: when a track starts, roughly every 15 seconds while playing, and when a track finishes. A book reports its position on the whole-book timeline; a podcast episode reports its own. Finishing a file marks the underlying item (book or episode) as finished only once the reported position lands within 5 seconds of that item's total duration — so finishing the last file of a multi-file book marks the book finished, while finishing an earlier file does not.

Loading a book or podcast show from the provider pane (`B`) places the cursor on the in-progress file or episode and starts playback at the stored position. A podcast show resumes the newest in-progress episode. An item with no stored progress, or one already marked finished, starts from the beginning. If the server reports a stored position past the end of the target file, cliamp starts that file from the beginning instead of seeking out of range. Playing a file again later in the session resumes it from the position the server has by then, so returning to a partly played episode continues where it left off. This resume behavior applies to the provider pane; queuing a book or show through the `N` browse overlay does not.

## How it works

cliamp authenticates with the configured token or with username/password, lists your libraries (optionally filtered by `libraries`), and enumerates books and podcast shows from them. Playback streams through Audiobookshelf's item-file endpoint, which goes through cliamp's buffered download pipeline — the same treatment Jellyfin, Emby, Plex, and Navidrome stream URLs get, so `ffmpeg`-backed formats work the same way here.

That buffered pipeline downloads the whole stream into memory as it plays, with no partial-range requests. For a book stored as one large file (a single `.m4b` covering the whole book), that means memory use proportional to the file size, and resuming far into the book waits for the download to reach that point before playback continues. Books split into per-chapter files avoid both, since each file is comparatively small.

## Troubleshooting

- **Empty provider pane** — check `url` and either `token` or `user`/`password` in `[audiobookshelf]`.
- **No audio** — confirm the server actually serves the item's file endpoint; a proxy or reverse-proxy misconfiguration in front of Audiobookshelf is a common cause.
- **Missing chapter titles** — expected when a file spans several chapters; cliamp falls back to the file's embedded title or filename in that case.
