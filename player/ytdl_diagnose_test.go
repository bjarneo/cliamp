package player

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const ytdlpDebugHeader = `[debug] Command-line config: ['-v', '--simulate']
[debug] Encodings: locale UTF-8, fs utf-8
[debug] yt-dlp version stable@2025.12.08 from yt-dlp/yt-dlp [7a52ff29d]
[debug] Python 3.14.7 (CPython aarch64 64bit)
[debug] JS runtimes: none
[debug] Proxy map: {}
`

func TestParseYTDLPHealth(t *testing.T) {
	tests := []struct {
		name           string
		out            string
		wantVersion    string
		wantReleased   string // YYYY-MM-DD, empty when unknown
		wantJSRuntimes string
	}{
		{
			name:           "stable build without a runtime",
			out:            ytdlpDebugHeader,
			wantVersion:    "2025.12.08",
			wantReleased:   "2025-12-08",
			wantJSRuntimes: "none",
		},
		{
			name:           "nightly build with a runtime",
			out:            "[debug] yt-dlp version nightly@2026.08.19.232711 from yt-dlp/yt-dlp-nightly-builds\n[debug] JS runtimes: deno-2.9.6\n",
			wantVersion:    "2026.08.19.232711",
			wantReleased:   "2026-08-19",
			wantJSRuntimes: "deno-2.9.6",
		},
		{
			name:           "release without a channel prefix",
			out:            "[debug] yt-dlp version 2026.08.19 from yt-dlp/yt-dlp (linux_aarch64_exe)\n",
			wantVersion:    "2026.08.19",
			wantReleased:   "2026-08-19",
			wantJSRuntimes: "",
		},
		{
			name: "no debug header at all",
			out:  "ERROR: You must provide at least one URL\n",
		},
		{
			name:        "unparseable version keeps the string but no date",
			out:         "[debug] yt-dlp version dev from source\n",
			wantVersion: "dev",
		},
		{
			name:           "first version line wins",
			out:            "[debug] yt-dlp version 2026.08.19 from a\n[debug] yt-dlp version 2020.01.01 from b\n[debug] JS runtimes: deno-2.9.6\n[debug] JS runtimes: none\n",
			wantVersion:    "2026.08.19",
			wantReleased:   "2026-08-19",
			wantJSRuntimes: "deno-2.9.6",
		},
		{
			name: "non-debug lines are ignored",
			out:  "WARNING: yt-dlp version 2019.01.01 is old\nJS runtimes: none\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseYTDLPHealth(tt.out)
			if got.version != tt.wantVersion {
				t.Errorf("version = %q, want %q", got.version, tt.wantVersion)
			}
			if got.jsRuntimes != tt.wantJSRuntimes {
				t.Errorf("jsRuntimes = %q, want %q", got.jsRuntimes, tt.wantJSRuntimes)
			}
			released := ""
			if !got.released.IsZero() {
				released = got.released.Format("2006-01-02")
			}
			if released != tt.wantReleased {
				t.Errorf("released = %q, want %q", released, tt.wantReleased)
			}
		})
	}
}

func TestYTDLPHealthHint(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-10 * 24 * time.Hour)
	stale := now.Add(-268 * 24 * time.Hour)

	tests := []struct {
		name        string
		health      ytdlpHealth
		wantStale   bool
		wantRuntime bool
	}{
		{name: "probe failed", health: ytdlpHealth{}},
		{name: "healthy install", health: ytdlpHealth{version: "2026.08.19", released: fresh, jsRuntimes: "deno-2.9.6"}},
		{name: "stale only", health: ytdlpHealth{version: "2025.12.08", released: stale, jsRuntimes: "deno-2.9.6"}, wantStale: true},
		{name: "missing runtime only", health: ytdlpHealth{version: "2026.08.19", released: fresh, jsRuntimes: "none"}, wantRuntime: true},
		{name: "both problems", health: ytdlpHealth{version: "2025.12.08", released: stale, jsRuntimes: "none"}, wantStale: true, wantRuntime: true},
		{name: "unknown release date stays silent", health: ytdlpHealth{version: "dev", jsRuntimes: "deno-2.9.6"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := tt.health.hint(now)
			if got := strings.Contains(hint, "days old"); got != tt.wantStale {
				t.Errorf("stale hint = %v, want %v (hint = %q)", got, tt.wantStale, hint)
			}
			if got := strings.Contains(hint, "JavaScript runtime"); got != tt.wantRuntime {
				t.Errorf("runtime hint = %v, want %v (hint = %q)", got, tt.wantRuntime, hint)
			}
			if !tt.wantStale && !tt.wantRuntime && hint != "" {
				t.Errorf("hint = %q, want empty", hint)
			}
			if tt.wantStale && !strings.Contains(hint, "268 days old") {
				t.Errorf("hint = %q, want the release age in days", hint)
			}
		})
	}
}

func TestAnnotateYTDL403(t *testing.T) {
	base := errors.New("yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 403: Forbidden")

	t.Run("healthy install returns the error untouched", func(t *testing.T) {
		resetYTDLPHealthCache(t, ytdlpHealth{version: "2026.08.19", released: time.Now(), jsRuntimes: "deno-2.9.6"})

		if got := annotateYTDL403(base); got != base {
			t.Fatalf("annotateYTDL403() = %v, want the original error", got)
		}
	})

	t.Run("unhealthy install appends the hint", func(t *testing.T) {
		resetYTDLPHealthCache(t, ytdlpHealth{version: "2025.12.08", released: time.Now().Add(-200 * 24 * time.Hour), jsRuntimes: "none"})

		got := annotateYTDL403(base)
		if !errors.Is(got, base) {
			t.Fatalf("annotateYTDL403() dropped the wrapped error: %v", got)
		}
		if !strings.Contains(got.Error(), "HTTP Error 403: Forbidden") {
			t.Fatalf("annotateYTDL403() = %q, want the original 403 text", got)
		}
		if !strings.Contains(got.Error(), "days old") || !strings.Contains(got.Error(), "JavaScript runtime") {
			t.Fatalf("annotateYTDL403() = %q, want both hints", got)
		}
	})
}

// expireYTDLPHealthCache forces the next hint to re-probe yt-dlp, and restores
// the previous cache afterwards.
func expireYTDLPHealthCache(t *testing.T) {
	t.Helper()
	resetYTDLPHealthCache(t, ytdlpHealth{})

	ytdlpHealthMu.Lock()
	ytdlpHealthCached = time.Time{}
	ytdlpHealthMu.Unlock()
}

// resetYTDLPHealthCache primes the probe cache so tests never exec yt-dlp, and
// restores it afterwards.
func resetYTDLPHealthCache(t *testing.T, health ytdlpHealth) {
	t.Helper()
	ytdlpHealthMu.Lock()
	prevHealth, prevAt := ytdlpHealthCache, ytdlpHealthCached
	ytdlpHealthCache, ytdlpHealthCached = health, time.Now()
	ytdlpHealthMu.Unlock()

	t.Cleanup(func() {
		ytdlpHealthMu.Lock()
		ytdlpHealthCache, ytdlpHealthCached = prevHealth, prevAt
		ytdlpHealthMu.Unlock()
	})
}
