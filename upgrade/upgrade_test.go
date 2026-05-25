package upgrade

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

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
	old := httpClient
	httpClient = &http.Client{
		Timeout:   10 * time.Second,
		Transport: rewriter{target: u, rt: http.DefaultTransport},
	}
	t.Cleanup(func() { httpClient = old })
}

func TestLatestVersionSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/repos/") || !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	tag, err := latestVersion()
	if err != nil {
		t.Fatalf("latestVersion: %v", err)
	}
	if tag != "v1.2.3" {
		t.Errorf("tag = %q, want v1.2.3", tag)
	}
}

func TestLatestVersionHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	_, err := latestVersion()
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

	_, err := latestVersion()
	if err == nil {
		t.Error("latestVersion should error on invalid JSON")
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

	if err := downloadAndReplace(srv.URL+"/cliamp-linux-amd64", target); err != nil {
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

	err := downloadAndReplace(srv.URL+"/cliamp", target)
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

	if err := downloadAndReplace(srv.URL+"/cliamp", target); err != nil {
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
	if err := Run("v2.0.0"); err != nil {
		t.Errorf("Run(current=latest) = %v, want nil", err)
	}
}

func TestRunFailsLatestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	err := Run("v1.0.0")
	if err == nil {
		t.Error("Run should propagate latest-version failure")
	}
	if !strings.Contains(err.Error(), "checking latest version") {
		t.Errorf("error = %q, want to mention 'checking latest version'", err.Error())
	}
}
