# Bandcamp Integration

cliamp can stream your purchased [Bandcamp](https://bandcamp.com/) collection directly through its audio pipeline. EQ, visualizer, and all effects apply. Requires a Bandcamp account with purchases — there is no subscription; you stream what you own.

Playback uses the same buffer-while-playing + ffmpeg pipeline as the other server providers, with real seeking and gapless advance. `ffmpeg` must be on `PATH`.

This is built on Bandcamp's **official Subsonic API**, released as an open beta in July 2026 — a sanctioned integration, not scraping. See the note on the API below.

## Setup

First, generate dedicated Subsonic credentials on bandcamp.com: open **Fan Settings**, scroll down to the **Subsonic** section, and generate a username and password. These are separate, revocable credentials — **not** your Bandcamp login.

Then the fastest path is the interactive wizard: run `cliamp setup`, pick **Bandcamp**, paste the two values, and it validates them live and writes the `[bandcamp]` block for you.

Or configure it manually in `~/.config/cliamp/config.toml`:

```toml
[bandcamp]
user = "your-subsonic-username"
password = "your-subsonic-password"
```

Both values support `$ENV_VAR` indirection if you prefer to keep secrets out of the file. No developer or API registration is needed — any fan can generate credentials.

Optional keys:

```toml
# API endpoint override (escape hatch while the beta moves; default shown).
# Must be https: every request carries your Subsonic token in its query
# string. Plain http is accepted only for localhost (a local debugging proxy);
# anything else falls back to the default endpoint with a warning.
url = "https://bandcamp.com/api/subsonic"
# Default album browse sort, persisted when you cycle sort with `s`
browse_sort = "newest"
```

### Quality

Streams are served at Bandcamp's standard stream quality — 128 kbps MP3, delivered from Bandcamp's CDN (verified against the live API, August 2026; a `format=raw` request is accepted but changes nothing). Lossless remains download-only on the Bandcamp website; the API does not expose purchase downloads. There is no quality config key.

## Usage

Start directly on Bandcamp:

```sh
cliamp --provider bandcamp
```

Bandcamp appears as a provider alongside the others. Press `K` to jump straight to Bandcamp, or `Esc`/`b` to open the provider browser and select it.

The provider surfaces your purchased collection:

- **Recent purchases**: your ten newest albums, ready to play from the provider pane.
- **All purchases**: your entire collection as one queue, sorted by artist, album, and track number. Loads in a few paged requests and is cached until you refresh.
- **Random purchases**: a fresh shuffle of up to 100 tracks from your collection every time you open it.
- **Your playlists**: Bandcamp playlists, synced two-way — playlists you create or extend in cliamp appear in your Bandcamp collection on the web and app.
- **Album and artist browsing**: press `N` for the full collection browser (By Album with sort cycling via `s`, By Artist, By Artist/Album).

Press `Ctrl+F` while Bandcamp is active to search — note that search is scoped to your own collection (that is all the API searches), and results are tracks.

Pasting a public `bandcamp.com` URL still plays through yt-dlp as before — that path is unrelated to this provider and streams at public quality. See [yt-dlp.md](yt-dlp.md).

## Controls

When focused on the provider panel:

| Key | Action |
|---|---|
| `Up` `Down` / `j` `k` | Navigate |
| `Enter` | Load the selected playlist/album or play the selected track |
| `Ctrl+F` | Search your Bandcamp collection |
| `Ctrl+R` | Refresh (re-fetches collection and clears beta-endpoint memos) |
| `Tab` | Switch between provider and playlist focus |
| `Esc` / `b` | Open provider browser |

After loading a playlist or album you return to the standard playlist view with all the usual controls (seek, volume, EQ, shuffle, repeat, queue, search, lyrics).

## Troubleshooting

- **"Wrong Subsonic credentials" / errors mentioning Fan Settings**: regenerate the credentials in Bandcamp Fan Settings → Subsonic and update the `[bandcamp]` section. Note Bandcamp's settings page sometimes copies credentials with a stray leading space; cliamp trims pasted values, but double-check if you edited the file by hand elsewhere.
- **A browse screen says the beta "doesn't support" something**: Bandcamp's Subsonic implementation is an open beta and not every endpoint is implemented yet. cliamp remembers what failed so it doesn't hammer the server; press `Ctrl+R` to retry after Bandcamp ships updates.
- **Slow loading on large collections**: a known beta caveat on Bandcamp's side. The collection browser paginates and cliamp caches what it fetched; `Ctrl+R` re-fetches.
- **Diagnostics**: run `cliamp bandcamp probe` and attach the output to a bug report. It checks connectivity, credentials, sort types, search, and the stream endpoint — tokens and signed URLs are never printed, so the output is safe to share.

## Requirements

- A Bandcamp account with purchases, and Subsonic credentials generated in Fan Settings
- `ffmpeg` on `PATH` for stream decoding
- No developer/API registration

## A note on the API

Bandcamp's Subsonic API is an official open beta (announced July 16, 2026). Endpoints and behavior may change; cliamp degrades gracefully when something is missing and the `url` config key exists as an escape hatch. The unofficial browser-cookie collection APIs some tools use were deliberately not used: they violate Bandcamp's acceptable-use policy and have a history of being shut off without notice. cliamp streams only — it never writes decoded audio to disk.
