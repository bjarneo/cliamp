# UX1R-1 — Spotify Web API shapes (verified September 2026)

Researched from live pages on developer.spotify.com. All content reflects what
the live pages say; anything not confirmable is marked UNCONFIRMED. Changelog
chain: Feb 2026 (restructure) → Mar 2026 (reverts) → May 2026 (adds
`User.account_id`) → Jul 2026 (quota/429 changes only).

Changelog: https://developer.spotify.com/documentation/web-api/references/changes/february-2026
Migration guide: https://developer.spotify.com/documentation/web-api/tutorials/february-2026-migration-guide

## 1. Save Library Items — PUT /v1/me/library

- Source: https://developer.spotify.com/documentation/web-api/reference/save-library-items
- Query params: `uris` (string, required) — comma-separated Spotify URIs, max 40.
  Documented URI types: track, album, episode, show, audiobook, user, playlist.
  (`spotify:artist:{id}` is NOT in the save page's list, though it IS listed on
  contains — see UNCONFIRMED note below.)
- Request body: none — input is query-string only.
- Response: `200` with empty body.
- Scopes: `user-library-modify`, `user-follow-modify`, `playlist-modify-public`.

## 2. Remove Library Items — DELETE /v1/me/library

- Source: https://developer.spotify.com/documentation/web-api/reference/remove-library-items
- Query params: `uris` (string, required), comma-separated, max 40; same type
  list as save (no artist).
- Request body: none. Response: `200` empty body. Scopes as above.

## 3. Check Library Contains — GET /v1/me/library/contains

- Source: https://developer.spotify.com/documentation/web-api/reference/check-library-contains
- Query params: `uris` (string, required), max 40. URI types include
  `spotify:artist:{id}` in addition to track/album/episode/show/audiobook/user/playlist.
- Request body: none.
- Response: `200` JSON array of booleans, positionally matching input URIs.
- Scopes: `user-library-read`, `user-follow-read`, `playlist-read-private`.
- Absorbs the old `/me/*/contains`, `/me/following/contains`, and
  `/playlists/{id}/followers/contains` endpoints (all removed Feb 2026).

## 4. Add Items to Playlist — POST /v1/playlists/{playlist_id}/items

- Source: https://developer.spotify.com/documentation/web-api/reference/add-items-to-playlist
- Query params: `position` (integer, optional; zero-based; omitted = append),
  `uris` (string, optional comma-separated; if present in query, body URIs are
  ignored).
- Request body (`application/json`), verbatim: `{"uris": ["string"], "position": 0}`
- Response: `201` → `{"snapshot_id": "string"}`.
- Limits: max 100 items per request; large batches should use the body.
- Scopes: `playlist-modify-public` or `playlist-modify-private`.

```go
type AddItemsRequest struct {
    Uris     []string `json:"uris"`
    Position *int     `json:"position,omitempty"` // zero-based; omit = append
}
```

## 5. Remove Playlist Items — DELETE /v1/playlists/{playlist_id}/items

- Source: https://developer.spotify.com/documentation/web-api/reference/remove-items-playlist
- Query params: none.
- Request body schema verbatim: `{"items": [{"uri": "string"}], "snapshot_id": "string"}`
  — the array field is `items` (each element has only `uri`); there is NO
  `tracks` field and NO `positions` field.
- Response: `200` → `{"snapshot_id": "string"}`. Max 100 objects per request.
- Scopes: `playlist-modify-public` or `playlist-modify-private`.

```go
type RemoveItemsRequest struct {
    Items       []RemoveItem `json:"items"`                 // required
    SnapshotID  string       `json:"snapshot_id,omitempty"` // optional
}
type RemoveItem struct {
    URI string `json:"uri"`
}
```

## 6. Get Playlist Items — GET /v1/playlists/{playlist_id}/items

- Source: https://developer.spotify.com/documentation/web-api/reference/get-playlists-items
- Query params: `market`, `fields` (JSON filters), `limit` (default 20; min 1,
  max 50), `offset` (default 0), `additional_types` (`track`, `episode`;
  flagged possibly deprecated).
- Response nesting CONFIRMED: paging object whose array is `items[]`; each
  element carries the payload under `item` (with a deprecated duplicate
  `track`). Endpoint-level path: `response.items[].item`.
- Scope: `playlist-read-private` — and access is restricted to playlists owned
  by / collaborated on by the current user, else 403. Behavioral note from the
  Feb 2026 changelog: items are only returned for the user's own playlists;
  others provide metadata only.
- Track object in items still carries `popularity` [deprecated], plus
  `album`, `artists`, `duration_ms`, `id`, `is_playable`, `restrictions`,
  `name`, `track_number`, `type`, `uri`, `is_local`, `release_date` (episode).

## 7. Create Playlist — POST /v1/me/playlists

- Source: https://developer.spotify.com/documentation/web-api/reference/create-playlist
- Moved from `POST /users/{user_id}/playlists` (removed Feb 2026).
- Request body verbatim example:
  `{"name": "New Playlist", "description": "...", "public": false}`;
  `name` required; `public` defaults true (false requires
  `playlist-modify-private`); `collaborative` defaults false; `description`
  optional.
- Response: `201` Playlist object. Limits: max 11000 playlists per user.
- Scopes: `playlist-modify-public`, `playlist-modify-private`.
- The returned Playlist object has `items` (paging, own/collab playlists only)
  and a deprecated `tracks` duplicate.

## 8. Search — GET /v1/search

- Source: https://developer.spotify.com/documentation/web-api/reference/search
- Query params: `q` (required; filters `album`, `artist`, `track`, `year`,
  `upc`, `tag:hipster`, `tag:new`, `isrc`, `genre`), `type` (required,
  comma-separated; valid: `album`, `artist`, `playlist`, `track`, `show`,
  `episode`, `audiobook`), `market`, `limit` (default 5; MAX 10), `offset`
  (default 0; max 1000), `include_external` (only `audio`).
- CONFIRMED: limit max is 10, default 5 (was 50/20 pre-Feb-2026).
- Response: one top-level paging object per requested type — keys `tracks`,
  `artists`, `albums`, `playlists`, `shows`, `episodes`, `audiobooks`, each
  `{href, limit, next*, offset, previous*, total, items[]}`.
- Scope: none listed (any valid token).
- In search results: ArtistObject still shows `followers`, `genres`
  (deprecated), `popularity` (deprecated); track `popularity` deprecated;
  SimplifiedPlaylistObject `tracks` deprecated in favor of `items.total`.

## 9. Get Artist — GET /v1/artists/{id}

- Source: https://developer.spotify.com/documentation/web-api/reference/get-an-artist
  (old slug `/reference/get-artist` is a genuine 404)
- Query params: none documented. Scope: none listed.
- CRITICAL: `followers`, `genres`, and `popularity` ALL still exist on the
  Artist object, but ALL THREE are explicitly marked **Deprecated** on the
  live page. They deserialize today; do not assume longevity.

```go
type Artist struct {
    ExternalUrls ExternalUrls `json:"external_urls"`
    Followers    Followers    `json:"followers"`  // DEPRECATED; href always null
    Genres       []string     `json:"genres"`     // DEPRECATED
    Href         string       `json:"href"`
    ID           string       `json:"id"`
    Images       []Image      `json:"images"`
    Name         string       `json:"name"`
    Popularity   int          `json:"popularity"` // DEPRECATED, 0-100
    Type         string       `json:"type"`       // "artist"
    URI          string       `json:"uri"`
}
type Followers struct {
    Href  *string `json:"href"` // always null
    Total int     `json:"total"`
}
```

## 10. Get Track / Get Album — popularity status

- GET /v1/tracks/{id} — `popularity` EXISTS on Track, marked **Deprecated**
  (not removed). `external_ids{isrc,ean,upc}` exists, NOT deprecated (March
  revert confirmed). Also deprecated on track: `available_markets`,
  `linked_from`, `preview_url`.
- GET /v1/albums/{id} (source: get-an-album) — `popularity` EXISTS on Album,
  marked **Deprecated**. `external_ids` exists, not deprecated. `label`
  deprecated; `genres` deprecated (always empty); `available_markets`
  deprecated. `album_group` is gone from the full Album object but present
  (deprecated) on SimplifiedAlbum in artist-albums responses.

## 11. Get Artist's Albums — GET /v1/artists/{id}/albums

- Source: https://developer.spotify.com/documentation/web-api/reference/get-an-artists-albums
- Query params: `include_groups` (comma-separated; exact values: `album`,
  `single`, `appears_on`, `compilation`; omit = all), `market`, `limit`
  (default 5, min 1, **MAX 10** — reduced from 50), `offset`.
- Response: paging object of SimplifiedAlbumObject. Scope: none listed.
- SimplifiedAlbum includes `album_group` (deprecated).

## 12. Get Current User's Top Items — GET /v1/me/top/{type}

- Source: https://developer.spotify.com/documentation/web-api/reference/get-users-top-artists-and-tracks
- Path: `type` = `artists` or `tracks`.
- Query: `time_range` (`long_term` ~1yr, `medium_term` ~6mo [DEFAULT],
  `short_term` ~4wk), `limit` (default 20; min 1; **MAX 50**), `offset`.
- Response: paging of ArtistObject or TrackObject. Scope: `user-top-read`.

## 13. March 2026 reversions

- Source: https://developer.spotify.com/documentation/web-api/references/changes/march-2026
- Exactly TWO restores, both tagged [REVERTED]: `external_ids` on Album and on
  Track. No endpoint path changes, no other fields restored.
- May 2026 added only `User.account_id`; July 2026 changed dev-mode quotas and
  the 429 body (`{"error":{"status":429,"message":"Too Many
  requests","reason":"QUOTA_EXCEEDED"}}`). Neither affects the shapes above.

## Migration-relevant surprises (vs the plan's assumptions)

1. **`PUT/DELETE /v1/me/library` take URIs as a QUERY PARAMETER (`?uris=`,
   max 40), NOT a request body.** The plan's `{"ids":[...]}`-with-types body
   assumption is wrong. The endpoints absorb follow/unfollow of artists,
   users, and playlists.
2. `GET /v1/me/library/contains` is URI-based (`?uris=`), returns a bare JSON
   array of booleans, and covers saved items AND followed artists/users/
   playlists.
3. `POST /v1/playlists/{id}/items` body is `{"uris":[...], "position":int}`;
   URIs may alternatively go in the query string.
4. **`DELETE /v1/playlists/{id}/items` body is `{"items":[{"uri":...}]}`** —
   NOT `{"tracks":[...]}` as the plan assumed; there is no `positions` field.
5. Playlist item nesting is `items[] → .item` (deprecated `.track` duplicate
   kept). `GET /playlists/{id}/items` now REQUIRES `playlist-read-private` and
   403s on playlists you don't own/collaborate on — items are only returned
   for own/collab playlists. Followed/other playlists may fail to load tracks;
   surface the error honestly.
6. Artist `followers`, `genres`, `popularity` still exist but are deprecated —
   usable for the artist page; document the deprecation.
7. Track and Album `popularity` still exist, deprecated — usable for the
   popular-pool ranking; document the deprecation.
8. Search limit default 5 / max 10; artist-albums limit default 5 / max 10
   (page with offset for more); top-items limit still max 50.
9. Create Playlist moved to `POST /v1/me/playlists` (no `{user_id}` path).
10. UNCONFIRMED: whether artist URIs are actually rejected by PUT/DELETE
    `/me/library` (the save page omits `spotify:artist` from its supported
    list while `contains` includes it) — implement with artist URIs and note
    the risk; exact 4xx error bodies; search-limit minimum (docs show both
    "Minimum: 1" and range "0–10" — clamp to [1,10] regardless).
