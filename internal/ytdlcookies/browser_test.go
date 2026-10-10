package ytdlcookies

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const browserCookieFixture = "# Netscape HTTP Cookie File\n" +
	"#HttpOnly_.mixcloud.com\tTRUE\t/\tTRUE\t0\tsessionid\tsession-value\n" +
	".mixcloud.com\tTRUE\t/private\tTRUE\t0\tprivate\tprivate-value\n" +
	".mixcloud.com\tTRUE\t/\tTRUE\t1\texpired\texpired-value\n" +
	".other.example\tTRUE\t/\tTRUE\t0\tother\tother-value\n" +
	"mixcloud.com\tFALSE\t/\tTRUE\t0\troot-only\troot-value\n" +
	"www.mixcloud.com\tFALSE\t/\tTRUE\t0\twww-only\twww-value\n" +
	"app.mixcloud.com\tFALSE\t/\tTRUE\t0\tapp-only\tapp-value\n" +
	"# not a cookie\nmalformed\n" +
	".mixcloud.com\tTRUE\t/\tTRUE\t0\tbad name\tvalue\n"

func fakeCookieExporter(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-based fake yt-dlp")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestFromBrowserExportsOfflineAndCleansUp(t *testing.T) {
	dir := fakeCookieExporter(t, `printf '%s\n' "$@" > "$FAKE_ARGS"
cat > "$FAKE_STDIN"
while [ "$#" -gt 0 ]; do
  if [ "$1" = '--cookies' ]; then shift; cookie_path="$1"; fi
  shift
done
cp -p "$cookie_path" "$FAKE_INITIAL"
printf '%s' "$FAKE_COOKIES" > "$cookie_path"
`)
	argsPath := filepath.Join(dir, "args")
	stdinPath := filepath.Join(dir, "stdin")
	initialPath := filepath.Join(dir, "initial")
	t.Setenv("FAKE_ARGS", argsPath)
	t.Setenv("FAKE_STDIN", stdinPath)
	t.Setenv("FAKE_INITIAL", initialPath)
	t.Setenv("FAKE_COOKIES", browserCookieFixture)
	jar, err := FromBrowser(context.Background(), "firefox:profile")
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]string{
		"mixcloud.com":         "sessionid,root-only",
		"www.mixcloud.com":     "sessionid,www-only",
		"app.mixcloud.com":     "sessionid,app-only",
		"sub.app.mixcloud.com": "sessionid",
	} {
		endpoint, _ := url.Parse("https://" + host + "/graphql")
		var names []string
		for _, cookie := range jar.Cookies(endpoint) {
			names = append(names, cookie.Name)
		}
		if got := strings.Join(names, ","); got != want {
			t.Errorf("cookies for %s = %s, want %s", host, got, want)
		}
	}
	stdin, err := os.ReadFile(stdinPath)
	if err != nil || string(stdin) != "[]" {
		t.Fatalf("offline input = %s, error=%v", stdin, err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--ignore-config", "--no-cache-dir", "--simulate", "--cookies-from-browser\nfirefox:profile", "--load-info-json\n-"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("missing cookie export option %s", want)
		}
	}
	info, err := os.Stat(initialPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("initial cookie file permissions: %v, %v", info, err)
	}
	lines := strings.Split(string(args), "\n")
	for i, line := range lines {
		if line == "--cookies" {
			if _, err := os.Stat(filepath.Dir(lines[i+1])); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("private cookie directory not removed: %v", err)
			}
		}
	}
}

func TestFromBrowserFailureDoesNotExposeCookieOutput(t *testing.T) {
	fakeCookieExporter(t, "echo synthetic-secret\necho synthetic-secret >&2\nexit 1\n")
	_, err := FromBrowser(context.Background(), "firefox")
	if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("unsafe error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = FromBrowser(ctx, "firefox")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}
