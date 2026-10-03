# Recently Played

cliamp stores local listening history in `~/.config/cliamp/history.toml`. It
records a track when the track starts to play, so the track that plays now is
at the top of the list. A track that you skip stays in the list.

## Browsing in the TUI

Open the **Local Playlists** provider. After cliamp records at least one play,
a virtual `Recently Played` entry appears at the top. Open it like any other
playlist. Tracks are newest first. The list is read-only. cliamp rejects
track removal and playlist deletion requests with an error. `f` still toggles
the favorite of a track.

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
JSON output uses `played_at` in RFC 3339 UTC for portability. A JSON entry also
holds a `provider_meta` object when the track has provider keys, such as
`navidrome.id`. An entry holds `"restricted": true` when its provider can
refuse to play the track, such as an exclusive Mixcloud show.

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

An entry can also hold `stream = true`, `feed = true`, `realtime = true`,
`restricted = true`, `album_art_url` and `provider_meta.<key>` lines, as
`favorites.toml` does.
With these keys, a Navidrome or Jellyfin track that you replay from Recently
Played still scrobbles and shows its cover, and cliamp still recognizes a
radio station or a podcast episode.

The default limit is 200 entries. cliamp removes the oldest entries first.

The list holds each path one time only. When a track that is already in the
list plays again, cliamp moves its entry to the top and sets the new time. The
time since the last play has no effect. The entry keeps its stored tags,
cover URL and `provider_meta` keys when the new play does not have them.
It also keeps a stored `realtime` or `restricted` flag when the new play has
no `provider_meta` keys. Otherwise, the provider of the new play sets these
flags. For example, a Mixcloud show loses its `[E]` suffix when Mixcloud no
longer marks it as exclusive.

## What is recorded

cliamp records every track that starts to play. This includes live streams,
such as radio stations and ICY streams, and tracks with no known duration. The
only exception is a track with an empty path.
