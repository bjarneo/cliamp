// Package upgrade implements self-upgrade by downloading the latest
// release binary from GitHub, mirroring the install.sh mechanism.
package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

const repo = "bjarneo/cliamp"

// httpClient serves the GitHub API and checksum requests.
var httpClient = httpclient.NewAPI(30 * time.Second)

// downloadClient fetches the release binary. A client timeout also covers the
// body read, so this client has none and a slow connection can finish the
// download. downloadIdleTimeout ends a download that stops sending.
var downloadClient = httpclient.NewAPI(0)

// downloadIdleTimeout is the longest wait of the binary download for the
// response headers or for the next body bytes.
var downloadIdleTimeout = 30 * time.Second

// errDownloadStalled ends a binary download that got no data within
// downloadIdleTimeout.
var errDownloadStalled = errors.New("download stalled")

// releaseMaxBytes limits the releases/latest response. releaseListMaxBytes
// limits the list of up to 100 releases. Each release lists its assets, so
// the list is larger than 1 MiB.
const (
	releaseMaxBytes     = 1 << 20
	releaseListMaxBytes = 16 << 20
)

type release struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
}

// Run checks for a newer release and replaces the current binary if one is found.
func Run(currentVersion string, prerelease bool) error {
	latest, err := latestVersion(prerelease)
	if err != nil {
		return fmt.Errorf("checking latest version: %w", err)
	}

	// With prerelease, the user can run a build newer than every listed
	// release. An older release must not replace it.
	if currentVersion != "" && (currentVersion == latest || prerelease && notNewer(latest, currentVersion)) {
		fmt.Printf("Already up to date (%s)\n", currentVersion)
		return nil
	}

	if currentVersion == "" {
		releaseType := "release"
		if prerelease {
			releaseType = "prerelease"
		}
		fmt.Printf("Latest %s is %s, downloading...\n", releaseType, latest)
	} else {
		fmt.Printf("Upgrading %s → %s\n", currentVersion, latest)
	}

	binaryName := fmt.Sprintf("cliamp-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	baseURL := fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, latest)
	checksumURL := baseURL + "/checksums.txt"
	binaryURL := baseURL + "/" + binaryName

	fmt.Printf("Downloading %s...\n", binaryName)

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating current binary: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	expectedHash, err := releaseChecksum(checksumURL, binaryName)
	if err != nil {
		return fmt.Errorf("verifying release checksum: %w", err)
	}
	if err := downloadAndReplace(binaryURL, exe, expectedHash); err != nil {
		return err
	}

	fmt.Printf("Upgraded to %s\n", latest)
	return nil
}

func latestVersion(prerelease bool) (string, error) {
	endpoint := "releases/latest"
	if prerelease {
		endpoint = "releases?per_page=100"
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", repo, endpoint)
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	if !prerelease {
		var r release
		if err := httpclient.ReadJSON(resp.Body, releaseMaxBytes, &r); err != nil {
			return "", fmt.Errorf("parsing response: %w", err)
		}
		return validTag(r.TagName)
	}

	var releases []release
	if err := httpclient.ReadJSON(resp.Body, releaseListMaxBytes, &releases); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	return newestRelease(releases)
}

// newestRelease returns the tag of the release with the highest SemVer
// version that is not a draft. Stable releases count too, so a stable release
// that follows a prerelease wins over it. Tags that are not SemVer versions
// are skipped.
func newestRelease(releases []release) (string, error) {
	var best string
	var bestVersion version
	for _, r := range releases {
		v, ok := parseVersion(r.TagName)
		if r.Draft || !ok {
			continue
		}
		if best == "" || v.compare(bestVersion) > 0 {
			best, bestVersion = r.TagName, v
		}
	}
	if best == "" {
		return "", errors.New("no releases found")
	}
	return validTag(best)
}

func validTag(tag string) (string, error) {
	if strings.TrimSpace(tag) == "" || strings.ContainsAny(tag, "/\\") {
		return "", errors.New("release response contains an invalid tag")
	}
	return tag, nil
}

func releaseChecksum(url, binaryName string) (string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum download failed: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", errors.New("checksum file is too large")
	}
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != binaryName {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", fmt.Errorf("invalid SHA-256 entry for %s", binaryName)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("invalid SHA-256 entry for %s", binaryName)
		}
		return strings.ToLower(fields[0]), nil
	}
	return "", fmt.Errorf("no SHA-256 entry for %s", binaryName)
}

func downloadAndReplace(url, destPath, expectedHash string) error {
	if len(expectedHash) != sha256.Size*2 {
		return errors.New("valid expected SHA-256 is required")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	idle := time.AfterFunc(downloadIdleTimeout, func() {
		cancel(fmt.Errorf("%w: no data for %s", errDownloadStalled, downloadIdleTimeout))
	})
	defer idle.Stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		if stall := stallError(ctx); stall != nil {
			err = stall
		}
		return fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()
	// The headers arrived, so the first body bytes get a full idle window.
	idle.Reset(downloadIdleTimeout)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	// Write to a temp file in the same directory as the target so
	// os.Rename works (same filesystem).
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, "cliamp-upgrade-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w (try running with sudo)", err)
	}
	tmpPath := tmp.Name()

	// Limit download to 200 MB to prevent unbounded disk usage from a
	// rogue redirect or compromised CDN.
	const maxBinarySize = 200 << 20
	h := sha256.New()
	body := &idleReader{r: resp.Body, timer: idle, timeout: downloadIdleTimeout}
	written, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(body, maxBinarySize+1))
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		if stall := stallError(ctx); stall != nil {
			return fmt.Errorf("downloading: %w", stall)
		}
		return fmt.Errorf("writing binary: %w", err)
	}
	if written == 0 || written > maxBinarySize {
		tmp.Close()
		os.Remove(tmpPath)
		return errors.New("download is empty or exceeds maximum size")
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("truncated download: received %d of %d bytes", written, resp.ContentLength)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, expectedHash) {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("SHA-256 mismatch: got %s", got)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("syncing binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("writing binary: %w", err)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("setting permissions: %w", err)
	}

	if err := replaceExecutable(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		if runtime.GOOS != "windows" {
			return fmt.Errorf("replacing binary: %w (try running with sudo)", err)
		}
		return fmt.Errorf("replacing binary: %w", err)
	}

	return nil
}

// idleReader restarts timer each time a read returns data, so the timer
// fires only when the body stops sending for timeout.
type idleReader struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.timer.Reset(r.timeout)
	}
	return n, err
}

// stallError returns the stall error when the idle timer canceled ctx, and
// nil otherwise. The canceled request reports only a generic context error.
func stallError(ctx context.Context) error {
	if cause := context.Cause(ctx); errors.Is(cause, errDownloadStalled) {
		return cause
	}
	return nil
}

func replaceExecutable(sourcePath, destPath string) error {
	if runtime.GOOS == "windows" {
		return replaceExecutableOnWindows(sourcePath, destPath)
	}
	return os.Rename(sourcePath, destPath)
}

func replaceExecutableOnWindows(sourcePath, destPath string) error {
	backupPath := filepath.Join(filepath.Dir(destPath), "."+filepath.Base(destPath)+".old")
	if err := os.Remove(backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing previous binary backup: %w", err)
	}

	if err := os.Rename(destPath, backupPath); err != nil {
		return fmt.Errorf("moving current binary aside: %w", err)
	}

	if err := os.Rename(sourcePath, destPath); err != nil {
		if rollbackErr := os.Rename(backupPath, destPath); rollbackErr != nil {
			return fmt.Errorf("installing new binary: %w; restoring current binary: %w", err, rollbackErr)
		}
		return fmt.Errorf("installing new binary: %w", err)
	}

	// Windows keeps the running executable locked. A leftover backup is
	// harmless and will be removed before the next upgrade.
	_ = os.Remove(backupPath)
	return nil
}
