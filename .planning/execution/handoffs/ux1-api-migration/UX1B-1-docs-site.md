# UX1B-1 — Docs + site sync for the Feb-2026 API migration

Docs-only thread of wave ux1. No code files touched. All edits applied on
top of the working tree, which already carried earlier-wave diffs in both
docs files (the `f` follow/unfollow wording and `x` removal notes) — those
were preserved untouched.

## Files changed

1. `docs/spotify.md` — three edits
2. `docs/keybindings.md` — one edit
3. `site/index.html` — reviewed, NO changes needed (see below)
4. This handoff file

## Factual claims and sources (UX1R-1-api-shapes.md)

| Claim in docs | Where | Source (UX1R-1 section) |
|---|---|---|
| Search results capped at 10 per type by Spotify's current API (was "up to 20 results") | `docs/spotify.md` Search section | §8 Search — `limit` max 10 (was 50 pre-Feb-2026) |
| Artist drill-down loads their album list (was "top tracks, falling back to album list") | `docs/spotify.md` Search drill paragraph | Task brief: top-tracks endpoint removed Feb 2026, no replacement |
| Artist drill shows album list, not top tracks | `docs/keybindings.md` search-overlay `Enter` row | Same |
| Playlist items only returned for playlists the user owns or collaborates on; others (followed, from search) **may fail** with an error | `docs/spotify.md` Playlists section (new short paragraph) | §6 Get Playlist Items — `playlist-read-private` required, 403 on non-owned/non-collab; "items are only returned for own/collab playlists". Phrased "may fail" because enforcement details are not fully documented |

## Deliberate non-changes (verified, with reasons)

- **Library "Top Tracks" mentions kept** (`docs/spotify.md` provider-panel
  list, `docs/keybindings.md` playlist-list note, `site/index.html` Spotify
  blurb "liked songs, top tracks, followed artists and playlists"). These
  describe the user's own top items via `/v1/me/top/{type}`, which still
  exists (UX1R-1 §12, limit max 50 → "up to 200 tracks" via paging stays
  valid). They are NOT the removed artist top-tracks endpoint.
- **`site/index.html` untouched.** The Spotify feature blurb makes no
  result-count claim (grep over "results"/search wording confirms; the only
  "results list" phrase is the YouTube blurb) and its "top tracks" is the
  library feature above. Nothing stale to update; no new features promised.
- **Internal endpoint moves NOT documented** (create-playlist move to
  `/v1/me/playlists`, library follow/like endpoints). Per task brief, user
  docs keep mentioning endpoint paths only where they already did (dev-mode
  `/v1/search` caveat, `/v1/me` auth troubleshooting) — pattern preserved.
- **Nothing promised about Smart Shuffle, artist pages, or a Home view.**
- **`docs/spotify.md` "By Artist" library-browser description kept**
  ("loads every track across all their releases"): artist-albums endpoint
  still exists (UX1R-1 §11, max 10 per page, offset paging for more).
- **The dev-mode `400 "Invalid limit"` caveat kept as-is** (search section,
  troubleshooting): it describes Spotify's error for dev-mode apps hitting
  catalog endpoints, independent of cliamp's now-within-cap request size.

## Consistency check performed

Re-read both edited docs end-to-end plus targeted greps across
`docs/spotify.md`, `docs/keybindings.md`, and `site/index.html`:

- `grep -i "top tracks|top-tracks|20 results|50 results|up to 20|up to 50"`
  → only the three legitimate library-feature mentions remain (listed above).
- No invented keybindings: keybindings.md change was wording-only inside an
  existing row (`Enter` drill description); key tables untouched otherwise.
- No stale ">10 results" claims; the only search-count claim anywhere is now
  the corrected "up to 10 results … caps search at 10 results per type".
- Earlier-wave working-tree diffs in both docs files left byte-identical.

## Flag for other threads

- `docs/provider-development.md` (outside this thread's write scope) still
  documents `ArtistTopTracksLoader` / `ArtistTopTracks(artistID)` at lines 43
  and 60, matching `provider/interfaces.go:157-160`. If the code thread
  removes the artist top-tracks path from the provider contract, that file
  needs a matching update.
