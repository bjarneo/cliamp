# Execution Plan — spotify-library-parity

Wave-based execution of the approved plan to bring the Spotify provider to
near-GUI library/playlist navigation (browse interfaces, library rows,
incremental loading, multi-type search, write suite).

Approved plan summary: Spotify implements the existing capability interfaces
(ArtistBrowser / AlbumBrowser / AlbumTrackLoader, Navidrome-style), gains
Top Tracks / Recently Played rows, TrackPager-based incremental loading,
multi-type search with drill-down, and a full write suite (like/unlike,
follow/unfollow, remove-from-playlist, rename, batch add).

- Roadmap: `ROADMAP.md`
- Task registry: `tasks.json`
- Handoffs: `handoffs/<wave-id>/<task-id>.md`

## Wave structure

| Wave | Tasks | Theme |
|------|-------|-------|
| wave1-contract-lock | W1C-1 | Lock shared contracts (interfaces, types, config) |
| wave2-core-impl | W2A-1, W2B-1 | Parallel: spotify provider core vs ui/model |
| wave3-features-docs | W3A-1, W3B-1 | Parallel: spotify search+writes vs docs+site |
| wave4-integration-gate | lead | make check, review pass, fixes, commits |

Write-scope rule: parallel tasks within a wave own disjoint paths. The shared
contract files (`provider/interfaces.go`, `provider/types.go`,
`playlist/provider.go`, `config/config.go`) are frozen after wave 1 until the
integration gate.

## Merge gates

- After every wave: `go build ./...` and scoped `go test` per thread.
- Final gate: `make check` (gofmt + vet + all tests) and docs/site
  consistency check against `command_registry.go` keys.
