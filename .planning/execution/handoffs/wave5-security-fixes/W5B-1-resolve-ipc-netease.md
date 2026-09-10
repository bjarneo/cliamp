# W5B-1: resolve / ipc / netease security fixes

## Status

Done. All three Mimosa findings (CWE-78, high) fixed; verification green.

## What Changed

### Finding 1 — resolve/resolve.go: option injection via pageURL (resolveYTDLRange)

- `resolveYTDLRange` now appends `"--"` (end-of-options separator) immediately
  before `pageURL`, so a URL starting with "-" is treated as an operand, not a
  yt-dlp flag.
- Sibling call site audited: `DownloadYTDL` had the same shape (pageURL passed
  positionally after `-o`). Applied the same `"--"` guard there.

### Finding 2 — ipc/process_windows.go: Sprintf-built /FI filter (processAlive)

- Dropped `"/FI", fmt.Sprintf("PID eq %d", pid)` in favor of all-literal argv:
  `tasklist /FO CSV /NH`. The PID is matched in the existing Go CSV parsing
  loop (record[1] comparison was already in place).
- Kept the 5s timeout and error-wrapping style. Behavior for callers is
  unchanged (existing windows-tagged table test expectations still hold;
  without a filter, tasklist always exits 0 and the loop decides liveness).
- `fmt` import retained (still used by `fmt.Errorf`).

### Finding 3 — external/netease/provider.go: browser value into --cookies-from-browser

- Added `validateCookieBrowser` at the exec boundary
  (`extractBrowserCookieHeader` now calls it first). Allows exactly:
  chrome, chromium, firefox, brave, edge, opera, safari, vivaldi, whale.
  Case-insensitive match (mirrors yt-dlp's own name handling); anything else
  is rejected with `unsupported browser %q for cookies_from_browser (valid: ...)`
  listing the accepted values.
- Table-driven test added: `TestValidateCookieBrowser` in provider_test.go
  (all 9 names, case-insensitivity, empty, unknown, "-chrome" option-injection
  shape, "chrome:./x" subcommand shape).
- Config surface checked: value flows from `[netease] cookies_from_browser`
  (config/config.go) via `cfg.NetEase.CookiesFrom` → `NewFromConfig` →
  `ensureCookieHeader` → `extractBrowserCookieHeader`. Validation is at the
  exec boundary only, per task rules.

## Files

- `resolve/resolve.go`
- `ipc/process_windows.go`
- `external/netease/provider.go`
- `external/netease/provider_test.go`

## Verification

- `gofmt -l -w resolve ipc external/netease` — clean (no files needed changes).
- `go vet ./resolve/... ./ipc/... ./external/netease/...` — pass.
- `go test ./resolve/... ./ipc/... ./external/netease/...` — all pass.
- `GOOS=windows go build ./ipc/...` — pass (process_windows.go compiles).
- `GOOS=windows go vet ./ipc/...` — pass (type-checks the windows-tagged test
  file too; the test itself only runs on Windows hosts).

## Blockers Or Risks

- None blocking.
- Intentional scope note: the netease validator may reject exotic values
  yt-dlp would have accepted (e.g. profile suffixes like `chrome:Profile 1`).
  The task sanctioned this; error is phrased "unsupported browser" and
  restricted to the netease exec boundary. If profile support is ever needed,
  extend `supportedCookieBrowsers`/`validateCookieBrowser` with explicit
  parsing rather than loosening the allowlist.
- `resolve.SetYTDLCookiesFrom` (also fed from soundcloud/ytmusic config blocks
  in main.go, outside this thread's ownership) is NOT validated at the resolve
  exec site. If Mimosa flags it later, the same allowlist pattern applies.

## Next Thread Should Know

- yt-dlp's argparse honors `--`, so the resolve.go guard is safe for all
  current invocations (no `--`-sensitive positional handling elsewhere).
- The windows test suite for `ipc` cannot execute on Linux; rely on
  `GOOS=windows go build/vet` until a Windows CI runner exists.
