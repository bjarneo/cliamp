# W6G-1: post-commit correctness fixes (URI-based remote remove + guards)

## Status

Done. Implementation, tests, and docs are complete and coherent in the
working tree; full-tree gate green (`make check` = gofmt + vet + all tests,
41 packages ok, 0 fail, 2026-09-12). Uncommitted by design — the Mimosa
PreToolUse hook blocks in-session `git commit`; the user commits from their
own terminal.

Origin: post-commit review round following 6fd4915 (feature) and 117f4be
(security). The fixes below landed in the working tree in the prior session
but were interrupted before this handoff and the registry update were written.

## What Changed

### 1. Remote remove is URI-based, not position-based (correctness fix)

- `provider.PlaylistTrackRemover.RemoveTrackFromPlaylist` widened to
  `(ctx, playlistID, position, track playlist.Track)`. Rationale: the caller's
  position indexes a filtered (playable-only) view while the API's positions
  count raw items — a mismatched position could target the wrong item.
- `external/spotify/writer.go`: URI resolved from the track (Path, falling
  back to ProviderMeta id); DELETE body sends `{"tracks":[{"uri":...}]}`
  with **no** positions, so all occurrences of the track are removed
  (documented in docs/spotify.md). The one-item `/items` fallback fetch and
  `cachedTrackURI`/`fetchTrackURIAt` helpers are deleted (~60 lines).
  Snapshot-cache invalidation retained.
- UI (`commands.go`, `keys_spotify_write.go`, `keys.go`): `x` passes the
  selected track through; `remoteTrackRemovedMsg` success handler removes one
  local row with path-match guard, shrinks the mirror, resets trackPaging,
  decrements the pane TrackCount. Duplicate-occurrence rows may linger in the
  local queue until reload — cosmetic, documented, and safe because every
  subsequent removal is also URI-based.

### 2. Queue-mirror bookkeeping on local removes (playback.go)

- Local remove inside the mirrored range now **shrinks** the mirror
  (`providerQueueLen--`, last-path recomputed, trackPaging reset) instead of
  leaving stale counters.
- Remote remove path refuses to fire for synthetic Library rows
  (`isSyntheticProviderRow` guard — "YOUR MUSIC" etc. are not real playlist
  IDs), so removal stays local there.

### 3. providerOwnsPath colon normalization (keys_spotify_write.go)

- The real SpotifyProvider's `URISchemes()` returns `"spotify:"` (with
  colon); tests' fakes use bare `"spotify"`. Ownership detection normalized
  both ways, and `TestProviderOwnsPathColonScheme` pins the real convention
  end-to-end (like via `*` reaches a colon-scheme provider; a
  `spotifyevil:`-style prefix must not match).

### 4. Search playlist drill-down uses Tracks(), not TrackPager (commands.go)

- A pager read at offset 0 would rewrite the provider's page cursor for that
  playlist ID and corrupt an in-flight incremental queue load of the same
  playlist. Drill-down deliberately calls blocking `Tracks()` now
  (IPC/daemon callers already did). Test renamed/rewritten to pin this.

### 5. `f` on owned playlists refused in search tab (spot_search_tabs.go)

- Unfollowing an owned playlist deletes it server-side; from the search
  Playlists tab `f` now refuses with a pointer to the provider pane's `D`
  confirm flow. command_registry availability matches (owned rows excluded).
- docs/keybindings.md + docs/spotify.md updated for both `f` and the
  URI-removal note in `x`.

### 6. netease validateCookieBrowser accepts full yt-dlp browser specs

- `BROWSER[+KEYRING][:PROFILE][::CONTAINER]` specs (e.g. `chrome:Profile 1`,
  `firefox:default::Personal`) now validate: only the browser part is checked
  against the allowlist. Option injection remains blocked (leading-dash specs
  rejected; the value is a single argv element, no shell). This un-breaks
  real profile specs that W5B-1's stricter check rejected (`chrome:./x`).

### 7. Test backfill + config example

- config/saver_test.go: `TestSaveSpotifySortWritesSpotifySection` (was
  untested since wave 1). config/config_test.go: qobuz `private_key` cases.
- config.toml.example: `[spotify] album_sort` documented (missed in 6fd4915).
- external/spotify/writer_test.go rewritten for the URI contract; ui/model
  tests for synthetic-row fallback, owned-playlist refusal, Tracks() drill.

## Verified

- Full tree: `make check` (gofmt, go vet, all tests) — 41 packages ok, 0 fail.
- Site sync check: site/index.html needs **no** change — its Spotify blurb is
  feature-level ("like tracks, follow artists, and create, rename, or delete
  playlists in-app") and none of these refinements change what the site
  advertises; the site does not enumerate config keys (`album_sort`) or
  per-row key semantics.
- Plan cross-check: all phases delivered; the Phase-6 stretch item (♥
  liked-state prefix via batched `/v1/me/tracks/contains`, 50 ids/call) was
  not implemented — only the per-track contains check inside ToggleTrackLike
  exists. Open follow-up, not a regression.

## Blockers Or Risks

- None in code. Uncommitted state is the only exposure: 21 modified files in
  the working tree. Suggested single commit (one bundled fix wave, per
  maintainer preference):
  `fix(spotify): uri-based remote remove, mirror bookkeeping, search-tab guards`
  covering provider/ external/spotify/ external/netease/ config.toml.example
  config/ ui/model/ docs/{spotify,keybindings,provider-development}.md —
  note this includes config_test.go's qobuz private_key cases, which belong
  with 117f4be's theme but are harmless here.

## Next Thread Should Know

- The remove contract change is breaking for any out-of-tree provider
  implementing `PlaylistTrackRemover` — in-tree only Spotify implements it.
- Build env for this sandbox is now persistent at `~/cliamp-libs` (see memory
  `cliamp-sandbox-build-env`); the old /tmp prefix was wiped.
