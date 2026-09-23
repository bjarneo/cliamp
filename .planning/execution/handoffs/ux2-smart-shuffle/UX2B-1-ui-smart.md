# UX2B-1 — Smart Shuffle UI (Z key, prefetch, chip/marker, config)

Wave ux2 thread B, part 1. Uncommitted on `spotify-library-parity`,
layered over the landed UX2A-1 backend and earlier uncommitted waves.
Scope touched: `ui/model/**` (smart.go new, keys.go, update.go,
playback.go, view.go, model.go, state.go, command_registry.go,
smart_test.go new), `config/` (config.go, config_test.go, saver_test.go),
`config.toml.example`. Nothing else.

## Key handling

- `Z` in main mode (keys.go, beside `z`) → `Model.toggleSmartShuffle()`
  (ui/model/smart.go).
- Queue-owning provider resolution mirrors the like-toggle:
  `queueOwningProvider()` resolves via `providerForTrack` (CustomStreamer
  URI scheme) from the current track first, then any queue row. The owner
  must implement `provider.Recommender` to enable; disabling needs nothing
  (a config-seeded smart flag can always be turned off).
- Refusal: `Smart Shuffle needs a supporting provider` toast, no state
  change, no persist, no shuffle flip. (Empty queues also resolve to no
  owner → refusal, by design.)
- On: `EnableSmart()`; if shuffle was off, `ToggleShuffle()` too (smart
  implies shuffle — `AddSmart` no-ops otherwise) and both keys persist via
  configSaver (`smart_shuffle`, `shuffle`) with the `z`-handler error-toast
  fallback. ClearPreload + preloadNext + one immediate `smartMaybeFetch()`.
- Off: `DisableSmart()` (playlist drops unplayed smart rows), persist
  `smart_shuffle=false`, toast, preload refresh. Session no-repeat set and
  in-flight/backoff state are kept.
- Registry: `{Keys: ["Z"], KeyLabel: "Z", Label: "Toggle Smart Shuffle",
  Keymap: true, Mode: commandModeMain}` — the keymap/help overlay consumes
  the registry, so help is covered. `TestReservedKeysCoversHandleKey`
  stays green.

## Prefetch state machine (ui/model/smart.go)

- `smartMaybeFetch()` dispatches `smartRecommendsCmd` →
  `smartRecommendsMsg{tracks, err, gen, providerName, queueLen, tailPath}`
  with `nextRequest(&m.requests.smart)` gen plumbing
  (fetchSpotSearchAllCmd pattern); ctx via `newLikeContext()` (15 s).
- Trigger condition (exact):
  `Smart() && Shuffled() && !smart.fetching && backoff expired &&
   recommenderForQueue() != nil && SmartPending() <= max(2, Len()/10)`.
  DEVIATION (documented in code): the plan's "len(order tail after current
  pos)" is not computable from the playlist API — `Index()` is a track
  index, which shuffle decouples from the order position, and adding
  playlist methods was out of scope. `SmartPending()` (pending smart rows
  in the upcoming order) is the exactly computable lowness signal, keeps a
  steady cushion mixed into the order, and is the recipe the binding UX2A-1
  handoff prescribes. Net effect: toggling Z on a fresh queue tops up
  immediately (Spotify-like mixing), then re-tops as the cushion drains.
- Call sites: track advance via `playTrack` (both return paths), gapless
  advance (update.go tickMsg), `r`/`z` key handlers, Z-on, and the tick
  path (gated on playing && !paused, beside the preload retry; paused/
  stopped never poll).
- Accept guards, in order: stale gen dropped (in-flight flag stays with
  the newer request — gen bump implies a newer dispatch); provider-name
  mismatch dropped (flag released; avoids a stuck in-flight flag after a
  provider switch); smart toggled off mid-flight dropped; queue-identity
  guard (tracksAppendedMsg mirror pattern: `Len() == msg.queueLen &&
  tailPath == msg.tailPath`) dropped; shuffle off mid-flight dropped
  (AddSmart would silently no-op; session set not poisoned).
- On accept: skip empty paths, dedupe against the session injected-URI set
  (`m.smart.injected`, map field, session-only), `AddSmart(fresh...)`,
  `adjustScroll()`. No preload refresh — mirrors the existing
  `tracksAppendedMsg` → `Add()` path (see residual risks).
- Volume: `smartVolume(len) = clamp(len/6, 3, 30)` passed as the
  RecommendTracks limit, so injected ≤ limit by construction.
- Errors: toast `Smart Shuffle failed: %s` once per 30 s backoff window
  (`smartRetryBackoff`), smart mode stays on, never fatal. Empty or
  all-duplicates batches apply the same backoff silently (prevents a
  dispatch→empty→dispatch tick loop hammering the API).
- Seed: copy of `playlist.Tracks()` at dispatch time.

## Rendering

- Header chip: `[Smart]` (activeToggle style) rendered directly after
  `[Shuffle]` (before `[Repeat: …]`) in `renderPlaylistHeader`, only while
  `playlist.Smart()` is on.
- Row marker: `✚` in the 6-char marker block, rendered purely from
  `Track.Smart` (so played smart rows stay marked after DisableSmart).
  Precedence (documented in view.go): the marker shares the bookmark cell
  — an explicit user bookmark `★` outranks the injected `✚` when both
  apply. Width of the marker block is unchanged.

## Config

- `config.Config.SmartShuffle bool`; parsed from top-level
  `smart_shuffle` beside `shuffle`; `config.toml.example` gains the key
  with a one-line comment.
- Startup: `PlaylistConfig` interface gains `EnableSmart()`; `ApplyPlaylist`
  (called by main.go with the real playlist, unchanged) now does
  `if Shuffle || SmartShuffle → ToggleShuffle()` then
  `if SmartShuffle → EnableSmart()` — smart implies shuffle at startup too.
- Persistence path is the existing generic `config.Save` (top-level
  key/value rewrite), same as `z`'s `shuffle`.

## Test inventory

ui/model/smart_test.go (new fake: `fakeRecommenderProvider` —
CustomStreamer + Recommender recording calls/limit/seed-len; new
`recordingSaver` ConfigSaver stub):

- TestSmartToggleNeedsRecommender — Z on non-Recommender owner: toast, no
  flag/shuffle/persist change.
- TestSmartToggleOnOffWithRecommender — on: flag+shuffle+persist+toast;
  AddSmart rows present; off: flag cleared, unplayed smart rows removed,
  `smart_shuffle=false` saved.
- TestSmartToggleOnKeepsExistingShuffle — no double toggle, no shuffle save.
- TestSmartMaybeFetchConditions — no dispatch when smart off / shuffle
  off / cushion full (0 provider calls); dispatch when low; no
  double-dispatch while in flight; completion resets flag, injects
  smartVolume(len) rows, limit == clamp, seed == queue snapshot.
- TestSmartMaybeFetchBackoff — error → toast + future retryAt + smart
  stays on + no dispatch during backoff + resumes after expiry.
- TestSmartRecommendsMsgGuards — stale gen dropped (flag retained for the
  newer request); queue mutation mid-flight dropped; valid batch injected
  session-deduped with empties skipped and rows marked Smart; all-repeats
  batch triggers backoff without growth.
- TestSmartVolume — boundaries 0→3, 5→3, 6→3, 18→3, 60→10, 180→30, 600→30.
- TestSmartHeaderChipAndRowMarker — chip hidden/off-shown, exactly one ✚,
  ★ still renders (precedence).
- TestSmartShuffleAppliedFromConfig — ApplyPlaylist seeds smart+shuffle;
  default config leaves both off.
- TestSmartFetchTickWiring — one playing tick dispatches exactly one
  provider call.

config: TestDefaultConfig (smart false), TestApplyPlaylist (+2 smart
cases incl. implies-shuffle), TestLoadSmartShuffle (parse true/false/
absent), TestSaveSmartShuffleRoundtrip (Save → Load, section-safe).

## Verify output (scoped)

```
. ~/cliamp-libs/env.sh
go build ./...                                       # BUILD_OK
go vet ./ui/... ./config/...                         # VET_OK
go test -count=1 ./ui/... ./config/...               # ok ui, ok ui/model, ok config
gofmt -l ui config                                   # no output
go test -count=5 ./ui/model/ ./config/               # ok (shuffle-random stability)
```

## Residual risks / notes for Wave 5

- Gapless-vs-injection desync is possible in theory: AddSmart's tail
  reshuffle can move a different track into the immediate-next slot while
  a preload is armed. This exactly matches the pre-existing
  `tracksAppendedMsg → Add()` behavior (same reshuffle), so no new
  mechanism was added; if Wave 5 wants to fix it, fix it for both paths.
- Smart mode survives queue Replace (flag lives in the playlist); the
  prefetch then targets the new queue whenever its owner is a Recommender.
  The session no-repeat set also survives — intended.
- The handoff's task text described the trigger as "remaining upcoming
  order ≤ max(2, 10%)" but the binding UX2A-1 semantics table prescribes
  `SmartPending() <= max(2, len/10)`; the latter is implemented (see
  deviation note above).
- docs/ + site/ sync for the Z key is deliberately Wave 5 (out of this
  thread's write scope).
