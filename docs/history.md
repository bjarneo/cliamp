# Recently Played

cliamp stores local listening history in `~/.config/cliamp/history.toml`. It
records a play after you listen to at least 50% of a track. This is the same
threshold that Last.fm and the Navidrome scrobbler use. Skipped tracks do not
enter the list.

## Browsing in the TUI

Open the **Local Playlists** provider. After cliamp records at least one play,
a virtual `Recently Played` entry appears at the top. Open it like any other
playlist. Tracks are newest first. The list is read-only. cliamp rejects
bookmark, track removal, and playlist deletion requests with an error.

To clear the list, run `cliamp history clear`.

## CLI

```sh
cliamp history                # show the 50 most recent plays
cliamp history --limit 200    # show the 200 most recent
cliamp history --limit 0      # show all (capped at 200 entries on disk)
cliamp history --json         # machine-readable output
cliamp history clear          # wipe the history file
```

The relative timestamp, such as `3m ago` or `yesterday`, uses local time. The
JSON output uses `played_at` in RFC 3339 UTC for portability.

## File format

`history.toml` uses the same minimal TOML format as cliamp local playlists:

```toml
[[entry]]
played_at = "2026-05-06T22:09:11Z"
path = "/home/me/Music/AC-DC/Highway to Hell.flac"
title = "Highway to Hell"
artist = "AC/DC"
album = "Highway to Hell"
year = 1979
duration_secs = 208
```

The default limit is 200 entries. cliamp removes older plays in FIFO order.
Consecutive plays of the same track within 5 minutes update the top entry time
instead of adding another entry.

## What is not recorded

- Tracks skipped before the 50% threshold.
- Live streams without a known duration, such as radio stations and ICY streams.
  cliamp cannot detect their 50% point.
- Tracks with empty paths. This is a defensive check.
