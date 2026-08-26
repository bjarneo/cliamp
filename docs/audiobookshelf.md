# Audiobookshelf

Use cliamp to play audiobooks and podcasts from a self-hosted [Audiobookshelf](https://www.audiobookshelf.org/) server. Browse authors and books, list podcast shows, and sync listening positions with the server.

> **Quick start:** Run `cliamp setup`. Select API-key or username+password authentication. The TUI validates the connection and writes the `[audiobookshelf]` block. Manual steps follow.

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
| `token` | API key. Create one in the Audiobookshelf web UI under Settings → Users → API Keys. Use this instead of a username and password. |
| `user` | Username for password-based login, used when `token` is not set |
| `password` | Password for password-based login |
| `libraries` | Optional list of library names to restrict cliamp to (default: all) |

Set `token` to authenticate immediately. Set `user` and `password` to sign in on first use.

## Usage

After configuration, **Audiobookshelf** appears in the provider list. Press `B` to select it, or start cliamp with it selected:

```bash
cliamp --provider audiobookshelf
# or the shorter alias
cliamp --provider abs
```

Or set the provider in configuration:

```toml
provider = "audiobookshelf"
```

The provider pane has two sections:

```text
── Audiobooks ──
...every book...
── Podcasts ──
...every podcast show...
```

Select a book to queue one track for each audio file. Select a podcast show to queue one track for each episode, newest first.

Press `N` to open the browse overlay. Browse Authors, Books, then tracks. This provider uses "Authors" and "Books" instead of Artists and Albums. In the album list, cycle through By Title (default), Recently Added, and By Author.

Press `Ctrl+F` to search the configured libraries. cliamp expands each matching book or podcast show into tracks, up to the search-result limit. If the server cannot return tracks for an item, cliamp skips the item. Shorter partial results are not an error.

## Track titles

If a file contains one chapter, cliamp uses the chapter name as its title. Otherwise, it uses the embedded title, then the file name. It uses the book author as the artist and the book title as the album. Podcast episodes use the episode title.

## Progress and resume

cliamp reports the listening position when a track starts, about every 15 seconds during playback, and when a track ends. A book reports its position on the full book timeline. A podcast episode reports its own position. cliamp marks a book or episode finished only when the reported position is within 5 seconds of its total duration. Thus, finishing the last file of a multi-file book finishes the book. Finishing an earlier file does not.

Load a book or podcast show from the provider pane (`B`). cliamp selects the in-progress file or episode and starts at its saved position. For a podcast show, it resumes the newest in-progress episode. An item with no saved progress, or one marked finished, starts at the beginning. If the saved position is past the end of the target file, cliamp starts the file at the beginning. When you play a file again later in the session, cliamp uses the position then reported by the server. This resume behavior applies only to the provider pane. It does not apply when you queue a book or show from the `N` browse overlay.

## How it works

cliamp authenticates with the configured token or username and password. It lists libraries, optionally filters them with `libraries`, then lists their books and podcast shows. Playback uses the Audiobookshelf item-file endpoint and cliamp's buffered download pipeline. Jellyfin, Emby, Plex, and Navidrome stream URLs use the same pipeline. `ffmpeg`-backed formats work the same way.

The buffered pipeline downloads the full stream into memory during playback. It makes no partial-range requests. A book stored as one large file, such as one `.m4b` for the full book, uses memory proportional to its size. Resuming far into that book waits until the download reaches the saved position. Per-chapter files avoid both limits because each file is smaller.

## Troubleshooting

- **Empty provider pane**: Check `url` and either `token` or `user`/`password` in `[audiobookshelf]`.
- **No audio**: Confirm that the server serves the item file endpoint. A proxy or reverse-proxy configuration error is a common cause.
- **Missing chapter titles**: This is expected when a file covers several chapters. cliamp then uses the embedded title or file name.
