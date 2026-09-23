# W5A-1 Handoff

## Status

- done

## What Changed

Hardened the 5 Mimosa findings (CWE-78, high) in `player/`. No user-visible
behavior change: same binaries, same flags, same output.

### Finding 1 — ytdl.go:58 (probeYTDLDuration) + same guard at decodeYTDLPipe (~:306)

- `pageURL` was appended directly to yt-dlp args, so a URL beginning with "-"
  would be parsed as a yt-dlp option (argument injection).
- Fix: `"--"` (end-of-options separator) is appended immediately before
  `pageURL` in both call sites.
- To make the invariant testable, arg construction moved into two small
  helpers, `ytdlProbeArgs` and `ytdlStreamArgs` (both read the
  `ytdlCookiesFrom` global exactly as before; the format-preference comment
  moved with the stream args). yt-dlp is argparse-based, so it honors `--`.

### Finding 2 — ytdl.go:104/:110 (InstallYTDLP pipx/pip3 fallbacks)

- `exec.Command(path, ...)` with a LookPath-resolved variable tripped the
  non-literal-command heuristic.
- Fix: LookPath checks kept as availability gates only; invocations now use
  literal names (`exec.Command("pipx", "install", "yt-dlp")` /
  `exec.Command("pip3", ...)`. `exec.Command` re-resolves the name via its own
  LookPath, which succeeds because the gate just passed — identical behavior.

### Finding 3 — audio_device_windows.go:52 (SwitchAudioDevice)

- `deviceName` was single-quote-escaped and interpolated into the PowerShell
  script via `fmt.Sprintf`.
- Fix: script is now the constant `switchAudioDeviceScript` with zero
  interpolation; the device ID travels via the `CLIAMP_AUDIO_DEVICE` env var
  (`cmd.Env = append(os.Environ(), "CLIAMP_AUDIO_DEVICE="+deviceName)`) and is
  referenced as `$env:CLIAMP_AUDIO_DEVICE` inside the `-ID "..."` quoting.
- Spec deviation (intentional): the task said to keep `-ID '...'` single
  quotes, but PowerShell single-quoted strings are literals and never expand
  `$env:` — that would pass the literal text as the device ID and break device
  switching (every call would fall through to nircmd). Double quotes are
  required for expansion and are equally injection-safe: PowerShell inserts an
  expanded variable's value verbatim without re-parsing (no nested expansion).
- nircmd fallback unchanged (argv-based, no shell).

### Finding 4 — audio_device_windows.go:15 (ListAudioDevices)

- Restructured to the same constant-script style: the script is now the
  package-level constant `listAudioDevicesScript` (no `fmt.Sprintf`, no
  interpolation) passed to `-Command`.
- Terminal state is a literal compile-time constant + `-Command`; per the task
  note this is a constant-script false positive (there is no path from any
  external value into the script). Not obfuscated with `-EncodedCommand`.

## Files

- player/ytdl.go (edited)
- player/audio_device_windows.go (edited)
- player/ytdl_test.go (edited: new test + `slices` import)

## Verification

- `. /tmp/cliamp-libs/env.sh && gofmt -l -w player`: pass (clean)
- `. /tmp/cliamp-libs/env.sh && go vet ./player/...`: pass
- `. /tmp/cliamp-libs/env.sh && go test ./player/...`: pass (`ok ... 0.269s`)
- `. /tmp/cliamp-libs/env.sh && GOOS=windows CGO_ENABLED=0 go build ./player/...`: pass

New test: `TestYTDLPArgsEndOfOptions` (table-driven) asserts for both builders
that `"--"` is the second-to-last arg, `pageURL` is last, the URL never
appears before the separator (cases include `-o/root/evil` and
`--exec whoami`), and `--cookies-from-browser` is present when configured.

## Blockers Or Risks

- None blocking.
- Risk (finding 3): if a future Windows device name contained characters that
  PowerShell re-parsed after env expansion, it could alter the `-ID` value —
  but PowerShell does not re-parse expanded values, so this is theoretical.
  The previous single-quote escaping was injection-safe in practice too; the
  constant-script form removes the interpolation pattern entirely.
- Risk (finding 1): if a hypothetical yt-dlp build ignored `--`, dash-prefixed
  URLs would fail — but mainline yt-dlp (argparse) honors it.

## Next Thread Should Know

- `ytdlProbeArgs` / `ytdlStreamArgs` are the single place yt-dlp args are
  built; keep appending the URL via `append(args, "--", pageURL)` if new flags
  are added, and extend `TestYTDLPArgsEndOfOptions` if a third builder appears.
- `switchAudioDeviceScript` / `listAudioDevicesScript` must stay interpolation
  free; pass data to PowerShell via env vars, never `fmt.Sprintf`.
- audio_device_windows.go is `//go:build windows`; verified with
  `GOOS=windows CGO_ENABLED=0 go build ./player/...` (CGO off is fine for a
  type-check; player's windows backend does not need cgo here).
- Finding 4 will likely still be reported by a pattern-based scanner
  (`-Command` + constant). It is a documented false positive — do not
  "fix" it with `-EncodedCommand` or other obfuscation.
