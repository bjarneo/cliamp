# W4G-1 Handoff

## Status

Complete. All 13 findings from the Spotify library-parity integration review are
fixed; full verification passes (gofmt, vet, tests incl. `-race` on
external/spotify, `GOOS=windows` build).

## What Changed

- **[P0-1] fixed.** New `Model.providerQueueLastPath` (tail of the mirrored
  queue) + `resetProviderQueueMirror()` helper in `ui/model/keys_spotify_write.go`.
  The reset is now applied on every queue-replacement path: `replacePlaylistFromNav`
  (keys_nav.go), `feedTrackResolvedMsg` and the fbTracksResolvedMsg replace branch
  (update.go), `ipc.LoadMsg` (update.go), `handleIPCProviderLoad` and `queue.clear`
  (ipc_extended.go — both Replace the queue, same bug class as the listed :441),
  and `undoPlaylistMutation` (playback.go). `removeSelectedRemote` verifies the
  queue's last mirrored row still holds `providerQueueLastPath` before issuing a
  remote remove; on mismatch it falls back to local removal. The
  reorder/refresh spots that already zeroed `providerQueueLen` (keys.go) clear
  the tail path too. Test: `TestRemoteXAfterNavQueueReplacement`,
  `TestRemoteXTailMismatchFallsBackToLocal`.
- **[P1-2] fixed.** `SpotifyProvider.pagerCur map[string]pagerPos{servedOffset,
  nextAPIPos}` (guarded by `p.mu`). `TracksPage` now consumes `fetchTracksPage`'s
  next-API-position return: when `offset == servedOffset` for that id it
  continues from `nextAPIPos` and updates both; otherwise offset is treated as
  an API position (unchanged for non-sequential callers like the search
  drill-down). Cursor invalidated wherever `trackCache[id]` is deleted
  (Playlists snapshot change, AddTrackToPlaylist, AddTracksToPlaylist
  no-snapshot branch, RemoveTrackFromPlaylist, ToggleTrackLike/`yourMusicID`)
  and in `resetSessionScopedStateLocked`. Note: `AddTracksToPlaylist`'s
  snapshot-update branch does NOT invalidate — Spotify appends at the end, so
  existing positions don't shift. Test: `TestTracksPageFilteredItemSpansPageBoundary`
  (mutation-checked: without the cursor the second page returns `[t2 t3]`,
  duplicating t2).
- **[P1-3] fixed.** `playlistUnfollowedMsg` / `playlistRenamedMsg` /
  `playlistFollowedMsg` / `pickerRemoteWriteMsg` now return
  `m.fetchProviderPlaylists()` instead of calling `m.provider.Playlists()`
  inline. Cursor fixups moved into the `playlistsLoadedMsg` handler via
  `Model.provListFixup` (`provListFixupState{clampCursor, selectID}` +
  `applyProvListFixup` in providers.go); the fixup is cleared on both error
  paths and on provider switch. Unfollow/rename completions keep their
  generation guards; only the list refresh became async.
- **[P2-4] fixed.** Both remote picker writes (`addRemotePickerTracksCmd`,
  `createRemotePickerPlaylistCmd` call sites in pl_picker.go) use
  `m.newLikeContext()` (15s) instead of `context.Background()`.
- **[P2-5] fixed.** `len(result.Items) == 0 ||` break guards added to Artists,
  ArtistAlbums, savedAlbums, and AlbumTracks loops in browse.go (same style as
  pager.go).
- **[P2-6] fixed.** New `isSyntheticProviderRow(id)` helper in pl_picker.go
  (UI-side, no external/spotify import) used at all three sites:
  `filterRemotePickerPlaylists`, the keys_spotify_search.go:190 heuristic, and
  the new gates — `startProviderUnfollow` returns early on synthetic rows and
  the `D` registry Enabled func now requires a real playlist row under the
  cursor. Test: `TestProviderUnfollowSkipsSyntheticRows` (D is a no-op and
  registry-disabled on "YOUR MUSIC", still enabled on real rows).
- **[P2-7] fixed.** Esc from the new-name input restores the originating row:
  `len(m.plPicker.playlists)` for the local "+ New" row (it directly follows the
  local playlists), `plPickerCount()-1` for the remote one. No new state field
  needed — the row index is derivable from `newNameRemote`. Test:
  `TestPlaylistPickerEscReturnsToOriginatingNewRow`.
- **[P2-8] fixed.** `saveProviderSortKey(section, key, value)` shared helper in
  config/config.go; `SaveNavidromeSort` / `SaveSpotifySort` are one-line
  wrappers, public signatures and file-format behavior unchanged. Existing
  navidrome saver tests pass untouched; added
  `TestSaveSpotifySortWritesSpotifySection` to cover the [spotify] section-name
  path of the parameterized helper.
- **[P2-9] fixed.** `fetchRecentlyPlayedPage(ctx)` extracted in pager.go (one
  50-item page + dedupe by `itemKey`); `cachedRecentlyPlayed` and
  `probeRecentlyPlayedCount` both use it.
- **[P2-10] fixed.** `fetchTracksPageCmd` applies `resolveWrapperURLs` to
  appended pages too (both branches share one call; only the offset-0 branch
  uses the `expanded` flag for `playlistExact`).
- **[P2-11] fixed.** `remoteTrackRemovedMsg` is now matched by mirror identity
  (provider + playlistID == activeProviderPlaylistID + position + track Path)
  regardless of mutation generation; the generation still gates the error/success
  toasts and the other mutations. A freshest-generation completion that fails
  the mirror check zeroes `providerQueueLen`/`providerQueueLastPath` (the P0-1
  "path-check fails" case). Test: `TestOverlappingRemoteRemovesBothLand`
  (out-of-order completions both apply). Known residual, out of scope: the
  provider-side race where two concurrent removes of shifted remote positions
  can hit the wrong URI server-side — the UI now stays consistent regardless.
- **[P2-12] fixed.** `ToggleTrackLike` also nils `listCache` (Your Music count
  refreshes instead of waiting out the 5-min TTL).
- **[P2-13] fixed.** The append guard additionally requires
  `msg.offset == m.trackPaging.offset`; the field is now the identity check it
  was meant for (not dropped).

## Files

- `ui/model/keys_spotify_write.go` — mirror reset helper, tail check, synthetic gate on unfollow
- `ui/model/keys_nav.go`, `ui/model/playback.go`, `ui/model/ipc_extended.go`, `ui/model/keys.go` — mirror resets on queue replacement/reorder/refresh
- `ui/model/update.go` — mirror tracking on load/append/remove, async playlist refresh + fixup application, offset guard, remoteTrackRemovedMsg restructure
- `ui/model/commands.go` — wrapper resolution on appended pages
- `ui/model/pl_picker.go` — isSyntheticProviderRow, bounded contexts, esc row restore
- `ui/model/keys_spotify_search.go`, `ui/model/command_registry.go` — helper reuse, D gate
- `ui/model/model.go`, `ui/model/state.go`, `ui/model/providers.go` — new fields, fixup type/helper
- `external/spotify/pager.go` — pagerPos cursor in TracksPage, shared recently-played helper
- `external/spotify/provider.go` — pagerCur field/init/invalidation
- `external/spotify/writer.go` — cursor invalidation, listCache nil on like
- `external/spotify/browse.go` — empty-page guards
- `config/config.go` — saveProviderSortKey dedupe
- Tests: `ui/model/spotify_write_test.go`, `ui/model/pl_picker_remote_test.go`,
  `external/spotify/pager_test.go`, `config/saver_test.go`

## Verification

- `gofmt -l -w external/spotify ui config` — clean
- `go vet ./external/spotify/... ./ui/... ./config/...` — clean
- `go test ./external/spotify/... ./ui/... ./config/... ./playlist/... ./provider/...` — all pass
- `go test -race ./external/spotify/...` — pass
- `GOOS=windows CGO_ENABLED=0 go build ./external/spotify/...` — pass
- Also ran `go build ./...` and uncached `go test ./ui/... ./config/...` — pass

## Blockers Or Risks

- None blocking. Two deliberate judgment calls worth knowing:
  - `activeProviderPlaylistID` is cleared when the queue is replaced (P0-1 as
    specified), so the provider-pane highlight for the loaded playlist
    disappears after e.g. "R replaces queue from nav" or IPC loads. This is
    consistent with "the queue no longer mirrors that playlist".
  - In P1-3, if the async refresh errors, the pending fixup is discarded (cleared
    in the error paths) rather than retried; the next successful list load
    applies nothing stale.

## Next Thread Should Know

- No docs/site changes needed: none of these fixes change keybindings or
  user-visible semantics (D on synthetic Library rows was never a documented
  feature — it previously armed a confirmation that could unfollow a bogus ID).
  If docs ever enumerate which rows accept D, `docs/spotify.md` should say
  Library rows are excluded.
- `TestProviderUnfollowConfirmOwnedVsFollowed` was updated to drain the new
  async refresh command before asserting the refreshed list — that is the
  pattern any future test of write-triggered list refreshes should follow.
- The wave-3 feature work is still uncommitted; several files I edited
  (`external/spotify/pager.go`, `browse.go`, `writer.go`,
  `ui/model/keys_spotify_write.go`, `pl_picker_remote_test.go`, …) are
  untracked new files. Everything is in the working tree for the lead to commit.
- Residual (pre-existing, not a finding): `catalogBatchMsg`/`catalogSearchMsg`
  still call `m.provider.Playlists()` synchronously in Update; only catalog
  providers hit that path, not Spotify.
