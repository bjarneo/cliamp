package player

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/internal/ytdlbin"
)

// A persistent HTTP 403 from yt-dlp is almost never a cliamp bug: YouTube
// rejects the media URL because the local yt-dlp is too old to sign it, or
// because yt-dlp has no JavaScript runtime to solve the challenge that
// produces the signature. Both are invisible in yt-dlp's own error text, so we
// probe the binary once and append what we find to the error.
const (
	// ytdlpStaleAfter matches yt-dlp's own "older than 90 days" warning.
	ytdlpStaleAfter = 90 * 24 * time.Hour
	// ytdlpProbeTimeout bounds the diagnostic probe. It runs no network I/O,
	// so this only guards against a wedged binary.
	ytdlpProbeTimeout = 10 * time.Second
	// ytdlpHealthTTL lets a user update yt-dlp and retry within one session.
	ytdlpHealthTTL = 10 * time.Minute
)

// ytdlpHealth is the subset of yt-dlp's debug header that explains a 403.
type ytdlpHealth struct {
	version    string    // e.g. "2025.12.08", empty when unknown
	released   time.Time // release date parsed from version, zero when unknown
	jsRuntimes string    // as reported by yt-dlp; "none" when it has none
}

var (
	ytdlpHealthMu     sync.Mutex
	ytdlpHealthCache  ytdlpHealth
	ytdlpHealthCached time.Time
)

// annotateYTDL403 appends actionable diagnostics to a persistent 403 error.
// The original error is preserved for errors.Is/As and for callers matching on
// its text.
func annotateYTDL403(err error) error {
	hint := ytdlpHealthHint()
	if hint == "" {
		return err
	}
	return fmt.Errorf("%w — %s", err, hint)
}

// ytdlpHealthHint describes what about the local yt-dlp install is likely to
// cause 403s, or "" when nothing actionable was found.
func ytdlpHealthHint() string {
	ytdlpHealthMu.Lock()
	defer ytdlpHealthMu.Unlock()

	now := time.Now()
	if ytdlpHealthCached.IsZero() || now.Sub(ytdlpHealthCached) > ytdlpHealthTTL {
		ctx, cancel := context.WithTimeout(context.Background(), ytdlpProbeTimeout)
		defer cancel()
		ytdlpHealthCache = probeYTDLPHealth(ctx)
		ytdlpHealthCached = now
	}
	return ytdlpHealthCache.hint(now)
}

// probeYTDLPHealth reads yt-dlp's verbose debug header. Passing no URL makes
// yt-dlp print the header and exit immediately without any network access.
func probeYTDLPHealth(ctx context.Context) ytdlpHealth {
	cmd := ytdlbin.CommandContext(ctx, "-v", "--simulate")
	cmd.WaitDelay = time.Second
	// yt-dlp exits non-zero because no URL was given; the header we want is
	// already on stderr, which CombinedOutput captures either way.
	out, _ := cmd.CombinedOutput()
	return parseYTDLPHealth(string(out))
}

// parseYTDLPHealth extracts the version and JS runtime lines from yt-dlp's
// verbose output. Unknown fields stay zero so callers stay silent rather than
// guess.
func parseYTDLPHealth(out string) ytdlpHealth {
	var health ytdlpHealth
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "[debug] ")
		if !ok {
			continue
		}
		switch {
		case health.version == "" && strings.HasPrefix(rest, "yt-dlp version "):
			health.version = parseYTDLPVersion(strings.TrimPrefix(rest, "yt-dlp version "))
			health.released = parseYTDLPReleaseDate(health.version)
		case health.jsRuntimes == "" && strings.HasPrefix(rest, "JS runtimes:"):
			health.jsRuntimes = strings.TrimSpace(strings.TrimPrefix(rest, "JS runtimes:"))
		}
	}
	return health
}

// parseYTDLPVersion reduces "stable@2026.08.19 from yt-dlp/yt-dlp (linux_exe)"
// to "2026.08.19".
func parseYTDLPVersion(field string) string {
	version, _, _ := strings.Cut(strings.TrimSpace(field), " ")
	if _, after, ok := strings.Cut(version, "@"); ok {
		version = after // drop the release channel, e.g. "nightly@".
	}
	return version
}

// parseYTDLPReleaseDate reads the YYYY.MM.DD date that opens every yt-dlp
// version string. Nightly builds append a build counter, which is ignored.
func parseYTDLPReleaseDate(version string) time.Time {
	const layout = "2006.01.02"
	if len(version) < len(layout) {
		return time.Time{}
	}
	released, err := time.Parse(layout, version[:len(layout)])
	if err != nil {
		return time.Time{}
	}
	return released
}

// hint renders the user-facing advice for this install, or "" when yt-dlp
// looks healthy or could not be probed.
func (h ytdlpHealth) hint(now time.Time) string {
	var hints []string
	if !h.released.IsZero() && now.Sub(h.released) > ytdlpStaleAfter {
		days := int(now.Sub(h.released).Hours() / 24)
		hints = append(hints, fmt.Sprintf(
			"yt-dlp %s is %d days old and YouTube breaks old versions quickly — update it (%s) or set ytdlp_path to a newer binary",
			h.version, days, YtdlpInstallHint(),
		))
	}
	if h.jsRuntimes == "none" {
		hints = append(hints,
			"yt-dlp found no JavaScript runtime, so it cannot sign YouTube media URLs — install deno, see https://github.com/yt-dlp/yt-dlp/wiki/EJS")
	}
	return strings.Join(hints, "; ")
}
