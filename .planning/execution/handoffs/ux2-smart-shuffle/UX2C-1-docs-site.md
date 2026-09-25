# UX2C-1 — Smart Shuffle docs + site sync

Wave ux2 closing thread. Uncommitted on `spotify-library-parity`, layered
over the landed UX2A-1 (playlist Smart API + Spotify Recommender) and
UX2B-1 (UI) work. Scope touched: `docs/keybindings.md`, `docs/spotify.md`,
`site/index.html`. Nothing else; no code edits.

## Files changed

1. `docs/keybindings.md` — one row added to the Playlist and Queue table:
   `Z` directly after `z` (matches the `a`/`A` pairing already at the top
   of that table; the table is grouped, not strictly alphabetical).
2. `docs/spotify.md` — new `## Smart Shuffle` section between Controls and
   Library Browser (Controls' closing "usual controls … shuffle …" line
   still reads correctly ahead of it; no edits needed there).
3. `site/index.html` — one sentence added to the Spotify source-desc in
   the sources grid: "Smart Shuffle (<kbd>Z</kbd>) mixes recommendations
   into the queue." (kbd tag + one clause, matching that block's density).

## Claims and their source (all verified in code, not just handoffs)

| Doc claim | Source |
|---|---|
| `Z` main-view toggle; lowercase `z` stays plain shuffle | `ui/model/keys.go` case "Z" → `toggleSmartShuffle()`, case "z" unchanged |
| Spotify queues only; queue-owning provider resolved; others get a toast | `ui/model/smart.go` `queueOwningProvider`/`recommenderForQueue` (current track's URI scheme, then any queue row); toast "Smart Shuffle needs a supporting provider" |
| Enabling also turns shuffle on | `smart.go` `toggleSmartShuffle` (`EnableSmart` + `ToggleShuffle` when off) |
| Recommendations from top tracks + top artists' newest albums mix into the upcoming queue near its end | `external/spotify/recommend.go` sources; `playlist.AddSmart` tail insert; prefetch trigger `smart.go:145` `Remaining() <= max(2, Len()/10)` — the lead-adjusted trigger, NOT the SmartPending variant in UX2B-1's handoff |
| Rows marked ✚; `[Smart]` chip beside `[Shuffle]` | `ui/model/view.go:857` marker, `:591-592` chip |
| Off removes not-yet-played recommended rows; played + current stay | `playlist.DisableSmart` (UX2A-1 semantics table, confirmed in smart_test coverage) |
| No repeat within a session | `m.smart.injected` session set in `handleSmartRecommends`, kept across toggle-off |
| Failures back off quietly, never fatal | 30 s `smartRetryBackoff`; smart stays on; empty/all-repeat batches back off silently |
| `smart_shuffle` config key, top level beside `shuffle`, persisted like shuffle, implies shuffle on restore | `config/config.go` `ApplyPlaylist` (`Shuffle || SmartShuffle → ToggleShuffle`), `config.toml.example:20-21` |

Deliberately NOT documented (per task rules): artist pages, Home view, API
endpoint paths, prefetch thresholds/internals. The Recommender interface
itself was already documented in `docs/provider-development.md:43` by the
backend thread — left as is, consistent.

## Consistency notes

- Verified no stale claims: grepped docs/ + site for "smart"/"shuffle"/`Z`
  — the only pre-existing Smart mention is the provider-development.md
  interface row (consistent); site line 587 `[Shuffle]` is a static mock
  header, not a claim; navidrome.md/spotify.md "usual controls" lines are
  unaffected.
- The "Shift-letter keys are reserved for provider switching" note in
  keybindings.md is scoped to the playlist manager (and `A`, `V`, `N`…
  already exist in the main view), so the uppercase `Z` row does not
  contradict it.
- FLAG for lead (outside this thread's write scope):
  `docs/configuration.md` Options block lists `shuffle = false` but not
  `smart_shuffle`. config.toml.example has it; the spotify.md section
  documents it, but the configuration reference does not. One follow-up
  row there ("# Start with Smart Shuffle (implies shuffle)") would close
  the gap.
