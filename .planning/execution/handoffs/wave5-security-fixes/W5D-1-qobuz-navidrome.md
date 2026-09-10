# W5D-1 — Qobuz hardcoded key + protocol-mandated MD5 isolation

Status: DONE (all three findings addressed; gofmt/vet/build/tests green on scoped packages)

## What Changed

### Finding 1 — CWE-798 hardcoded credential (external/qobuz/bundle.go) — FIXED

- Removed `fallbackPrivateKey = "6lz8C03UDIC7"` entirely. No copy of the value
  remains anywhere in the repo (verified by grep over go/md/toml/html).
- `bundle.privateKey()` is now `privateKey(fallback string) (string, error)`.
  Precedence: scraped value > config value > error. The error names the config
  key and the value's source:
  `qobuz: OAuth private key not found in the web-player bundle and no [qobuz] private_key configured; extract the current value from the Qobuz web player bundle.js (play.qobuz.com) and set it as [qobuz] private_key`.
- New config key: **`[qobuz] private_key`** (string, default empty), added to
  `config.QobuzConfig.PrivateKey` and parsed under the existing `[qobuz]` case
  via `parseString` (so it supports `$ENV` interpolation like other string
  config values).
- **Config wiring choice: no main.go change.** `qobuz.New(quality)`'s signature
  was left alone; instead `configuredPrivateKey()` in bundle.go reads
  `config.Load()` lazily at scrape time, mirroring external/spotify's lazy
  config reads. Deliberate deviation from spotify: it is NOT memoized, so a
  user who hits the missing-key error can set the config and retry sign-in
  without restarting cliamp (the read only happens on interactive sign-in, so
  cost is one file read per attempt).
- bundle_test.go: scraped-wins-over-fallback precedence pinned, config
  fallback path tested, missing-key error tested for naming `[qobuz]
  private_key` and `play.qobuz.com`. config_test.go TestLoadQobuz extended
  with a private_key row (table-driven).

### Finding 2 — CWE-327 md5hex (external/qobuz/client.go:62) — ISOLATED, KEPT (protocol-mandated)

- `md5hex` moved verbatim (and alone) to new file `external/qobuz/signature.go`.
  Its doc comment states: the Qobuz API mandates an MD5-based request signature
  (md5(app_secret + sorted params)); protocol requirement, not a cryptographic
  choice; protects no local secret material; used only for request_sig values.
- `crypto/md5` import removed from client.go; callers (`trackFileURLSig`,
  `favoriteParams`) unchanged. `TestMD5Hex` moved to `signature_test.go`;
  `TestTrackFileURLSig` (pinned sig layout) unchanged in client_test.go.
- **The MD5 usage remains** — now living in external/qobuz/signature.go (plus
  its call sites in client.go). If the scanner still flags it, the single-file
  isolation + doc comment is the justification surface.

### Finding 3 — CWE-327 md5.Sum (external/navidrome/client.go:191) — ISOLATED, KEPT (protocol-mandated)

- Salt+token construction moved verbatim to new `subsonicAuth(password) (salt,
  token)` in external/navidrome/auth.go, with a doc comment in the same spirit
  (Subsonic-mandated token, crypto/rand salt, protects no local secret).
- `buildURL` now calls `subsonicAuth(c.password)`; output is byte-identical
  (same hex salt, same `hex.EncodeToString(md5.Sum(...)[:])` token, same
  params). crypto/md5, crypto/rand, encoding/hex imports dropped from
  client.go.
- New auth_test.go round-trip: token == md5hex(password+salt) computed with
  crypto/md5 in the test, plus fresh-salt-per-call check.
- **The MD5 usage remains** — now living in external/navidrome/auth.go only.

## Files

- external/qobuz/bundle.go (constant removed, fallback + config path, error)
- external/qobuz/bundle_test.go (rewritten fallback tests)
- external/qobuz/signature.go (new; md5hex + protocol doc comment)
- external/qobuz/signature_test.go (new; TestMD5Hex moved here)
- external/qobuz/client.go (md5hex + crypto/md5 removed)
- external/qobuz/client_test.go (TestMD5Hex moved out)
- external/navidrome/auth.go (new; subsonicAuth)
- external/navidrome/auth_test.go (new; round-trip test)
- external/navidrome/client.go (buildURL delegates; imports trimmed)
- config/config.go (QobuzConfig.PrivateKey + `private_key` parse case)
- config/config_test.go (TestLoadQobuz private_key row)
- config.toml.example (commented `private_key` in [qobuz])
- docs/qobuz.md (Setup note + Troubleshooting bullet for the new key)

## Verification

`. /tmp/cliamp-libs/env.sh`:
- `gofmt -l -w external/qobuz external/navidrome config` — clean (no files needed rewriting)
- `go vet ./external/qobuz/... ./external/navidrome/... ./config/...` — pass
- `go build ./external/qobuz/... ./external/navidrome/... ./config/...` — pass
- `go test ./external/qobuz/... ./external/navidrome/... ./config/...` — ok (all three packages)

No git add/commit performed (lead owns git).

## Blockers Or Risks

- **MD5 findings will likely still scan** — that is by design (upstream
  protocols require them). They are now confined to external/qobuz/signature.go
  and external/navidrome/auth.go with explicit protocol-requirement doc
  comments. Recommend the lead mark these as accepted/false-positive if Mimosa
  re-flags.
- `cliamp setup` (cmd/setup.go, out of my scope) rewrites the whole `[qobuz]`
  section body on re-run and would drop a hand-edited `private_key`. Pre-existing
  behavior for any manual [qobuz] edit; noting for awareness only.
- The stored-credentials file (~/.config/cliamp/qobuz_credentials.json) still
  persists the private key from the successful sign-in; that is runtime user
  data (0600), not source, so out of scanner scope.
- Behavior change is limited to the fallback path: when scraping fails and no
  `[qobuz] private_key` is configured, sign-in now errors with the actionable
  message instead of silently using the embedded constant.

## Next Thread Should Know

- Exact config key added: `[qobuz] private_key` (string; supports `$ENV`
  interpolation via parseString; scraped value always wins).
- No main.go change was needed or made; qobuz.New signature unchanged.
- config/config.go also carries concurrent W5 agents' edits (Spotify
  album_sort, saveProviderSortKey refactor) — not mine, left untouched.
- site/index.html has no private-key/Qobuz-secret content (checked), so no site
  edit was required by this change; docs/qobuz.md was updated instead.
- docs/plugins.md and site/index.html were NOT touched by me under any
  circumstances (out of scope per task rules).
