# Navidrome Integration

Cliamp can connect to a [Navidrome](https://www.navidrome.org/) server and stream music directly from your library. Navidrome is a self-hosted music server compatible with the Subsonic API.

## Setup

Set three environment variables before launching Cliamp:

```sh
export NAVIDROME_URL="http://your-server:4533"
export NAVIDROME_USER="your-username"
export NAVIDROME_PASS="your-password"
```

Then run Cliamp without any file arguments:

```sh
cliamp
```

You can also combine local files with a Navidrome session:

```sh
NAVIDROME_URL=http://localhost:4533 NAVIDROME_USER=admin NAVIDROME_PASS=secret cliamp ~/Music/extra.mp3
```

## How It Works

When the environment variables are set, Cliamp authenticates with your Navidrome server using the Subsonic API. On launch it fetches your playlists and presents them in the TUI.

Browse your playlists with the arrow keys and press Enter to load one. The tracks are added to the local playlist and playback starts immediately. Audio is streamed as MP3 from the server.

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate playlists |
| `Enter` | Load the selected playlist |
| `Tab` | Switch between provider and playlist focus |
| `N` | Open the Navidrome browser |

After loading a playlist you return to the standard playlist view with all the usual controls (seek, volume, EQ, shuffle, repeat, queue, search).

## Navidrome Browser

Press `N` at any time (or from the provider panel) to open the full-screen Navidrome browser. It lets you explore your library in three modes:

- **By Album**: browse a paginated list of all albums, then open any album to see its tracks.
- **By Artist**: browse all artists; selecting one loads every track across all their albums, grouped by album with separator headers.
- **By Artist / Album**: three-level drill-down: artist → album list → track list.

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

While viewing the global album list, press `s` to cycle through sort modes:

| Value | Description |
|---|---|
| `alphabeticalByName` | A → Z by album title (default) |
| `alphabeticalByArtist` | A → Z by artist name |
| `newest` | Most recently added |
| `recent` | Most recently played |
| `frequent` | Most frequently played |
| `starred` | Starred / favourited |
| `byYear` | Chronological by release year |
| `byGenre` | Grouped by genre |

The chosen sort is saved automatically to `~/.config/cliamp/config.toml` under the `[navidrome]` section as `browse_sort` and is restored on the next launch.

## Architecture

The integration is built around a `Provider` interface defined in the `playlist` package:

```go
type Provider interface {
    Name() string
    Playlists() ([]PlaylistInfo, error)
    Tracks(playlistID string) ([]Track, error)
}
```

The Navidrome client (`external/navidrome/client.go`) implements this interface. It builds authenticated Subsonic API requests using MD5 token auth (password + random salt) and parses the JSON responses into playlist and track structs.

Playlist and track fetching runs asynchronously through Bubbletea commands so the UI stays responsive while the server responds.

Adding support for another Subsonic-compatible server (Airsonic, Gonic, etc.) would mean implementing the same `Provider` interface against that server's API.

## Requirements

No additional dependencies are needed beyond a running Navidrome instance. The client uses Go's standard `net/http` and `crypto/md5` packages. Your Navidrome server must have the Subsonic API enabled, which is the default.
