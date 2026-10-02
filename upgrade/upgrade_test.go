package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

func testHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type rewriter struct {
	target *url.URL
	rt     http.RoundTripper
}

func (r rewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = r.target.Scheme
	clone.URL.Host = r.target.Host
	clone.Host = r.target.Host
	return r.rt.RoundTrip(clone)
}

func installTestClient(t *testing.T, serverURL string) {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The rewriter sits in front of the production transport, so the
	// tests see the headers that the real client sends.
	oldAPI, oldDownload := httpClient, downloadClient
	httpClient = &http.Client{
		Timeout:   10 * time.Second,
		Transport: rewriter{target: u, rt: oldAPI.Transport},
	}
	downloadClient = &http.Client{
		Timeout:   oldDownload.Timeout,
		Transport: rewriter{target: u, rt: oldDownload.Transport},
	}
	t.Cleanup(func() { httpClient, downloadClient = oldAPI, oldDownload })
}

func TestLatestVersionSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/repos/") || !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if ua := r.UserAgent(); ua != httpclient.UserAgent {
			t.Errorf("User-Agent = %q, want %q", ua, httpclient.UserAgent)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	tag, err := latestVersion(false)
	if err != nil {
		t.Fatalf("latestVersion: %v", err)
	}
	if tag != "v1.2.3" {
		t.Errorf("tag = %q, want v1.2.3", tag)
	}
}

func TestLatestPrereleaseVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		list    string
		want    string
		wantErr string
	}{
		{
			name: "prerelease newer than stable",
			list: `[
				{"tag_name":"v1.3.0-beta.2","prerelease":true,"draft":true},
				{"tag_name":"v1.2.3","prerelease":false},
				{"tag_name":"v1.3.0-beta.1","prerelease":true}
			]`,
			want: "v1.3.0-beta.1",
		},
		{
			name: "stable newer than prerelease",
			list: `[
				{"tag_name":"v2.3.0"},
				{"tag_name":"v2.2.0"},
				{"tag_name":"v2.0.0-rc.2","prerelease":true},
				{"tag_name":"v2.0.0-rc.1","prerelease":true}
			]`,
			want: "v2.3.0",
		},
		{
			name: "stable release beats its own release candidates",
			list: `[
				{"tag_name":"v2.4.0-rc.2","prerelease":true},
				{"tag_name":"v2.4.0"},
				{"tag_name":"v2.4.0-rc.1","prerelease":true}
			]`,
			want: "v2.4.0",
		},
		{
			name: "backported patch listed first",
			list: `[
				{"tag_name":"v1.9.1"},
				{"tag_name":"v2.0.0-rc.1","prerelease":true},
				{"tag_name":"v1.9.0"}
			]`,
			want: "v2.0.0-rc.1",
		},
		{
			name: "numeric prerelease parts compare by value",
			list: `[
				{"tag_name":"v2.0.0-rc.2","prerelease":true},
				{"tag_name":"v2.0.0-rc.10","prerelease":true}
			]`,
			want: "v2.0.0-rc.10",
		},
		{
			name: "tags that are not versions are skipped",
			list: `[
				{"tag_name":"nightly","prerelease":true},
				{"tag_name":"v1.2.3"}
			]`,
			want: "v1.2.3",
		},
		{
			name:    "only drafts",
			list:    `[{"tag_name":"v1.3.0-beta.1","prerelease":true,"draft":true}]`,
			wantErr: "no releases",
		},
		{
			name:    "empty list",
			list:    `[]`,
			wantErr: "no releases",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "/repos/") || !strings.HasSuffix(r.URL.Path, "/releases") {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				if got := r.URL.Query().Get("per_page"); got != "100" {
					t.Errorf("per_page = %q, want 100", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.list)
			}))
			defer srv.Close()
			installTestClient(t, srv.URL)

			tag, err := latestVersion(true)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("latestVersion error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("latestVersion: %v", err)
			}
			if tag != tc.want {
				t.Errorf("tag = %q, want %q", tag, tc.want)
			}
		})
	}
}

// TestLatestVersionBodySize verifies that a release response that fits the
// read limit decodes, and that a larger one reports httpclient.ErrTooLarge.
// The list of 100 releases with their assets is larger than 1 MiB.
func TestLatestVersionBodySize(t *testing.T) {
	// releases writes n releases with a padding field of pad bytes each.
	releases := func(n, pad int) string {
		var b strings.Builder
		b.WriteString(`[{"tag_name":"v2.3.0"}`)
		for i := range n {
			b.WriteString(`,{"tag_name":"v1.0.` + strconv.Itoa(i) + `","body":"` + strings.Repeat("x", pad) + `"}`)
		}
		b.WriteString("]")
		return b.String()
	}
	for _, tc := range []struct {
		name       string
		prerelease bool
		body       string
		want       string
		wantErr    error
	}{
		{
			name:       "release list larger than 1 MiB",
			prerelease: true,
			body:       releases(99, 20<<10),
			want:       "v2.3.0",
		},
		{
			name:       "release list over the limit",
			prerelease: true,
			body:       releases(1, releaseListMaxBytes),
			wantErr:    httpclient.ErrTooLarge,
		},
		{
			name:    "latest release over the limit",
			body:    `{"tag_name":"v2.3.0","body":"` + strings.Repeat("x", releaseMaxBytes) + `"}`,
			wantErr: httpclient.ErrTooLarge,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			installTestClient(t, srv.URL)

			tag, err := latestVersion(tc.prerelease)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("latestVersion error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("latestVersion: %v", err)
			}
			if tag != tc.want {
				t.Errorf("tag = %q, want %q", tag, tc.want)
			}
		})
	}
}

// TestRunPrereleaseNeverDowngrades verifies that --prerelease does not move
// the user to a release older than the one they run.
func TestRunPrereleaseNeverDowngrades(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current string
		list    string
	}{
		{
			name:    "newest stable is current, older prerelease listed",
			current: "v2.3.0",
			list:    `[{"tag_name":"v2.3.0"},{"tag_name":"v2.0.0-rc.2","prerelease":true}]`,
		},
		{
			name:    "current prerelease is newer than every listed release",
			current: "v2.4.0-beta.1",
			list:    `[{"tag_name":"v2.3.0"},{"tag_name":"v2.0.0-rc.2","prerelease":true}]`,
		},
		{
			name:    "current is the newest prerelease",
			current: "v2.4.0-rc.2",
			list:    `[{"tag_name":"v2.4.0-rc.2","prerelease":true},{"tag_name":"v2.3.0"}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/releases") {
					t.Errorf("Run requested %q, want no download", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.list)
			}))
			defer srv.Close()
			installTestClient(t, srv.URL)

			if err := Run(tc.current, true); err != nil {
				t.Errorf("Run(%q, prerelease) = %v, want nil", tc.current, err)
			}
		})
	}
}

func TestLatestVersionHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	_, err := latestVersion(false)
	if err == nil {
		t.Error("latestVersion should error on 500")
	}
	if !strings.Contains(err.Error(), "GitHub API") {
		t.Errorf("error = %q, want to mention 'GitHub API'", err.Error())
	}
}

func TestLatestVersionBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not valid json`))
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	_, err := latestVersion(false)
	if err == nil {
		t.Error("latestVersion should error on invalid JSON")
	}
}

func TestReleaseChecksum(t *testing.T) {
	want := strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, want+"  cliamp-linux-amd64\n")
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	got, err := releaseChecksum(srv.URL+"/checksums.txt", "cliamp-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("checksum = %q, want %q", got, want)
	}
}

func TestReleaseChecksumMissingEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("a", 64)+"  another-file\n")
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)
	if _, err := releaseChecksum(srv.URL+"/checksums.txt", "cliamp-linux-amd64"); err == nil {
		t.Fatal("releaseChecksum accepted a missing asset entry")
	}
}

func TestDownloadAndReplaceChecksumMismatchPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cliamp")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "NEW")
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	err := downloadAndReplace(srv.URL+"/cliamp", target, testHash([]byte("different")))
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("downloadAndReplace error = %v, want checksum mismatch", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil || string(got) != "OLD" {
		t.Fatalf("original after mismatch = %q, %v", got, readErr)
	}
}

func TestDownloadAndReplace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cliamp")
	// Pre-create target with old contents.
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	newContent := []byte("NEW BINARY BYTES")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(newContent)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	if err := downloadAndReplace(srv.URL+"/cliamp-linux-amd64", target, testHash(newContent)); err != nil {
		t.Fatalf("downloadAndReplace: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(newContent) {
		t.Errorf("target content = %q, want %q", got, newContent)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Verify executable bit is set where the platform models one.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("target mode = %o, want executable", info.Mode().Perm())
	}
}

// TestDownloadOutlivesAPITimeout verifies that the binary download is not
// cut by the short timeout of the API client. The API limit covers the whole
// body read, so a slow connection could not finish a 37 MB binary in it.
func TestDownloadOutlivesAPITimeout(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cliamp")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	newContent := []byte("NEW BINARY BYTES")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(newContent)))
		_, _ = w.Write(newContent[:4])
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write(newContent[4:])
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)
	httpClient.Timeout = 50 * time.Millisecond

	if err := downloadAndReplace(srv.URL+"/cliamp-linux-amd64", target, testHash(newContent)); err != nil {
		t.Fatalf("downloadAndReplace: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != string(newContent) {
		t.Errorf("target content = %q, want %q", got, newContent)
	}
}

// TestDownloadIdleTimeout verifies that the binary download has no total
// time limit, and that it fails when the server sends nothing for the idle
// timeout.
func TestDownloadIdleTimeout(t *testing.T) {
	newContent := []byte("NEW BINARY BYTES FOR A SLOW LINK")
	// stall blocks until the client gives up. The fallback ends the handler
	// when the client does not.
	stall := func(r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}
	for _, tc := range []struct {
		name        string
		handler     http.HandlerFunc
		wantStalled bool
	}{
		{
			name: "slow body that keeps sending",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(len(newContent)))
				// 16 chunks with 50 ms between them take longer than the
				// idle timeout, but no single gap reaches it.
				for chunk := range slices.Chunk(newContent, 2) {
					_, _ = w.Write(chunk)
					w.(http.Flusher).Flush()
					time.Sleep(50 * time.Millisecond)
				}
			},
		},
		{
			name: "slow headers then a short pause before the body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// Each wait is shorter than the idle timeout, but the
				// two waits together are longer.
				time.Sleep(200 * time.Millisecond)
				w.Header().Set("Content-Length", strconv.Itoa(len(newContent)))
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				time.Sleep(200 * time.Millisecond)
				_, _ = w.Write(newContent)
			},
		},
		{
			name: "body stops sending",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(len(newContent)))
				_, _ = w.Write(newContent[:4])
				w.(http.Flusher).Flush()
				stall(r)
			},
			wantStalled: true,
		},
		{
			name: "server sends no headers",
			handler: func(w http.ResponseWriter, r *http.Request) {
				stall(r)
			},
			wantStalled: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "cliamp")
			if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			installTestClient(t, srv.URL)
			oldIdle := downloadIdleTimeout
			downloadIdleTimeout = 300 * time.Millisecond
			t.Cleanup(func() { downloadIdleTimeout = oldIdle })

			start := time.Now()
			err := downloadAndReplace(srv.URL+"/cliamp-linux-amd64", target, testHash(newContent))
			got, _ := os.ReadFile(target)
			if !tc.wantStalled {
				if err != nil {
					t.Fatalf("downloadAndReplace: %v", err)
				}
				if string(got) != string(newContent) {
					t.Errorf("target content = %q, want %q", got, newContent)
				}
				return
			}
			if !errors.Is(err, errDownloadStalled) {
				t.Fatalf("err = %v, want %v", err, errDownloadStalled)
			}
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Errorf("stalled download took %s, want it to end after the idle timeout", elapsed)
			}
			if string(got) != "OLD" {
				t.Errorf("target content = %q, want the original binary", got)
			}
		})
	}
}

func TestReplaceExecutableOnWindows(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cliamp.exe")
	download := filepath.Join(dir, "cliamp-upgrade.exe")
	backup := filepath.Join(dir, ".cliamp.exe.old")

	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(download, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("STALE"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceExecutableOnWindows(download, target); err != nil {
		t.Fatalf("replaceExecutableOnWindows: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Fatalf("target content = %q, want NEW", got)
	}
	if _, err := os.Stat(download); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("download still exists or stat failed: %v", err)
	}
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup still exists or stat failed: %v", err)
	}
}

func TestReplaceExecutableOnWindowsRollsBack(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cliamp.exe")
	missingDownload := filepath.Join(dir, "missing.exe")

	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := replaceExecutableOnWindows(missingDownload, target)
	if err == nil {
		t.Fatal("replaceExecutableOnWindows should fail for a missing download")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("reading restored target: %v", readErr)
	}
	if string(got) != "OLD" {
		t.Fatalf("restored target content = %q, want OLD", got)
	}
}

func TestDownloadAndReplaceHTTPError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cliamp")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	err := downloadAndReplace(srv.URL+"/cliamp", target, testHash([]byte("unused")))
	if err == nil {
		t.Error("downloadAndReplace should error on 404")
	}

	// Original file should remain untouched on failure.
	got, _ := os.ReadFile(target)
	if string(got) != "OLD" {
		t.Errorf("target content = %q, want OLD — file should be untouched on error", got)
	}
}

func TestDownloadAndReplaceTruncatesOversize(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cliamp")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Serve a body larger than maxBinarySize but streamable (cannot easily
	// verify the limit without a massive body; we just confirm normal
	// operation works with a reasonably sized body).
	body := strings.Repeat("Z", 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	if err := downloadAndReplace(srv.URL+"/cliamp", target, testHash([]byte(body))); err != nil {
		t.Fatalf("downloadAndReplace: %v", err)
	}
	got, _ := os.ReadFile(target)
	if len(got) != 1024 {
		t.Errorf("target len = %d, want 1024", len(got))
	}
}

func TestRunAlreadyUpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v2.0.0"}`))
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	// Same current version → no download attempted.
	if err := Run("v2.0.0", false); err != nil {
		t.Errorf("Run(current=latest) = %v, want nil", err)
	}
}

func TestRunFailsLatestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	err := Run("v1.0.0", false)
	if err == nil {
		t.Error("Run should propagate latest-version failure")
	}
	if !strings.Contains(err.Error(), "checking latest version") {
		t.Errorf("error = %q, want to mention 'checking latest version'", err.Error())
	}
}
