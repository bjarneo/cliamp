# W5C-1 — luaplugin security fixes (wave5-security-fixes)

## Status

Complete. All three Mimosa findings addressed; `gofmt`, `go vet`, and `go test`
pass scoped to `./luaplugin/...`.

## What Changed

### Finding 1 — CWE-327 weak crypto: `cliamp.crypto.md5` removed (BREAKING)

BREAKING PLUGIN-API CHANGE — lead should surface to maintainer: the Lua
function `cliamp.crypto.md5(s)` no longer exists. Plugins calling it will get
"attempt to call a nil value" (field `md5`). `cliamp.crypto.sha256` and
`cliamp.crypto.hmac_sha256` are unchanged.

- `luaplugin/api_crypto.go`: removed md5 registration and `crypto/md5` import.
- `luaplugin/api_crypto_test.go`: `TestCryptoMD5` replaced with
  `TestCryptoMD5Removed` (asserts `cliamp.crypto.md5 == nil` as a regression
  guard). sha256/hmac tests untouched.
- `docs/plugins.md`: dropped the md5 line from the `cliamp.crypto` block.
- `plugins/`: no bundled plugin uses crypto at all (verified by grep) — no
  plugin migrations needed.
- `site/index.html`: no change needed — it has no plugin-API crypto rows
  (checked for md5/sha256/hmac/crypto; the page only lists community plugins,
  not per-function API rows).
- Out of scope / untouched: `external/navidrome/`, `external/qobuz/`, and
  `docs/navidrome.md` still use MD5 — that is protocol-mandated (Subsonic
  token auth, Qobuz), owned by other threads, and unrelated to the plugin API.

### Finding 2 — CWE-78 exec taint: command construction extracted and hardened

Feature preserved (allowlist, `permissions = {"exec"}`, concurrency caps,
output budget, minimal env all unchanged). Structural hardening in
`luaplugin/api_exec.go`:

- New `newPluginCommand(ctx, path, argv, cwd)` is now the single place that
  builds the `exec.Cmd`. It (i) requires `path` to be absolute (the
  LookPath-resolved path is already used — this pins the binary and rejects
  relative PATH resolutions), (ii) rejects empty argv entries, and (iii)
  documents why argv-passed options are safe (no shell; argv values can only
  be arguments to the fixed binary).
- Call site in `registerExecAPI` returns the error to the plugin as
  `(nil, err)` like every other validation failure.
- New table-driven `TestNewPluginCommand` in `api_exec_test.go` (absolute pin,
  flags preserved in argv, relative/bare path rejected, empty entry rejected,
  cwd + minimal env asserted).
- Note for scanner triage: the `exec.CommandContext(ctx, path, argv...)` call
  now lives inside `newPluginCommand` with the invariants documented above.
  If Mimosa still flags the call site, it is a documented
  intentional-capability finding (permission-gated, allowlisted exec is the
  feature, not a bug) — do not "fix" by removing argv.

### Finding 3 — CWE-78 notify-send option injection

`luaplugin/api_notify.go`: title/body from plugins now flow in after a `"--"`
end-of-options separator (GOption honors it), so a title starting with `-`
can no longer be parsed as a notify-send option (urgency/expire-time/actions).
`exec.Command` now uses the literal `"notify-send"` name (no taintable
resolved path), while the `LookPath` availability check and both log messages
are unchanged.

## Files

- `luaplugin/api_crypto.go`
- `luaplugin/api_crypto_test.go`
- `luaplugin/api_exec.go`
- `luaplugin/api_exec_test.go`
- `luaplugin/api_notify.go`
- `docs/plugins.md` (crypto block only)
- `.planning/execution/handoffs/wave5-security-fixes/W5C-1-luaplugin.md`

## Verification

- `. /tmp/cliamp-libs/env.sh && gofmt -l -w luaplugin` — clean (reformatted
  test struct alignment once; second run reports nothing).
- `. /tmp/cliamp-libs/env.sh && go vet ./luaplugin/...` — pass.
- `. /tmp/cliamp-libs/env.sh && go test ./luaplugin/...` — ok (full package,
  including bundled-plugin regression tests; `TestCryptoMD5Removed` and
  `TestNewPluginCommand` pass with all subtests).
- Grep: no `crypto.md5` references remain in `docs/`, `site/index.html`, or
  `plugins/`; no `crypto/md5` import remains in `luaplugin/`. Only remaining
  mention is the intentional nil-assertion in `api_crypto_test.go`.
- Scoped to `./luaplugin/...` only — no repo-wide go commands, no git
  operations.

## Blockers Or Risks

- Breaking API removal (crypto.md5) — needs a release-notes mention. No
  bundled plugin or doc other than `docs/plugins.md` referenced it.
- `exec.Command("notify-send", ...)` re-runs LookPath internally after our
  availability check; if the binary vanished in between, the failure logs via
  the existing "notify-send failed" path. Behavior for the not-installed case
  is identical.
- Empty argv strings are now rejected at exec.run() (previously allowed);
  only affects plugins passing `""` as an argument — a pathological case.

## Next Thread Should Know

- `cliamp.crypto` is now sha256 + hmac_sha256 only; keep it that way (weak
  crypto in plugin API was a Mimosa CWE-327 finding).
- All plugin subprocess construction must go through `newPluginCommand` —
  do not call `exec.Command*` directly from API surface code in this package.
- If a future bundled plugin needs an MD5-based protocol (e.g. Subsonic-like
  auth), do it in Go under `external/`, never in the Lua API.
