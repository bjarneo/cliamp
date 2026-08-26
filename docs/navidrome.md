# Navidrome Integration

Use Cliamp to connect to a [Navidrome](https://www.navidrome.org/) server and stream music from your library. Navidrome is a self-hosted music server that supports the Subsonic API.

> **Quick start:** Run `cliamp setup`. Enter the server URL, username, and password. The TUI validates the connection and writes the `[navidrome]` block. Manual steps follow.

## Setup

Set these three environment variables before you start Cliamp:

```sh
export NAVIDROME_URL="http://your-server:4533"
export NAVIDROME_USER="your-username"
export NAVIDROME_PASS="your-password"
```

Then start Cliamp without file arguments:

```sh
cliamp
```

You can combine local files with a Navidrome session:

```sh
NAVIDROME_URL=http://localhost:4533 NAVIDROME_USER=admin NAVIDROME_PASS=secret cliamp ~/Music/extra.mp3
```

## How It Works

When these environment variables are set, Cliamp authenticates with the Navidrome server through the Subsonic API. At startup, it gets playlists and shows them in the TUI.

Use the arrow keys to browse playlists. Press Enter to load one. Cliamp adds its tracks to the local playlist and starts playback. The server streams audio as MP3.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate playlists |
| `Enter` | Load the selected playlist |
| `Tab` | Switch between provider and playlist focus |
| `N` | Open the Navidrome browser |

After you load a playlist, Cliamp returns to the standard playlist view. Use the usual controls for seek, volume, EQ, shuffle, repeat, queue, and search.

## Navidrome Browser

Press `N` at any time, or from the provider panel, to open the full-screen Navidrome browser. Browse the library in three modes:

- **By Album**: Browse a paginated list of albums. Open an album to view its tracks.
- **By Artist**: Browse artists. Select an artist to load tracks from all albums, grouped by album with separator headers.
- **By Artist / Album**: Browse artist, album list, then track list.

### Browser controls

**Mode menu:**

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Navigate |
| `Enter` | Select mode |
| `Esc` / `N` | Close browser |

**Artist or album list:**

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Navigate |
| `Enter` / `→` | Drill in |
| `s` | Cycle album sort order (album list only) |
| `Esc` / `←` | Back |

**Track list:**

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Navigate |
| `Enter` | Append selected track to playlist |
| `a` | Append all tracks to playlist |
| `R` | Replace playlist with all tracks and start playing |
| `Esc` / `←` | Back |

### Album sort order

In the global album list, press `s` to cycle through sort modes:

| Value | Description |
|---|---|
| `alphabeticalByName` | A to Z by album title (default) |
| `alphabeticalByArtist` | A to Z by artist name |
| `newest` | Most recently added |
| `recent` | Most recently played |
| `frequent` | Most frequently played |
| `starred` | Starred / favourited |
| `byYear` | Chronological by release year |
| `byGenre` | Grouped by genre |

Cliamp saves the selected sort as `browse_sort` in the `[navidrome]` section of `~/.config/cliamp/config.toml`. It restores the sort at the next start.

## Architecture

The integration uses a `Provider` interface in the `playlist` package:

```go
type Provider interface {
    Name() string
    Playlists() ([]PlaylistInfo, error)
    Tracks(playlistID string) ([]Track, error)
}
```

The Navidrome client (`external/navidrome/client.go`) implements this interface. It creates authenticated Subsonic API requests with MD5 token authentication (password + random salt). It parses JSON responses into playlist and track structs.

Bubbletea commands get playlists and tracks asynchronously. The UI remains responsive while the server responds.

To support another Subsonic-compatible server, such as Airsonic or Gonic, implement the same `Provider` interface for that server API.

## Requirements

You need only a running Navidrome instance. The client uses the Go standard `net/http` and `crypto/md5` packages. The Navidrome server must have the Subsonic API enabled. This is the default.
