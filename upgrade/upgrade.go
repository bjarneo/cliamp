// Package upgrade implements self-upgrade by downloading the latest
// release binary from GitHub, mirroring the install.sh mechanism.
package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const repo = "bjarneo/cliamp"

var httpClient = &http.Client{Timeout: 30 * time.Second}

type release struct {
	TagName string `json:"tag_name"`
}

// Run checks for a newer release and replaces the current binary if one is found.
func Run(currentVersion string) error {
	latest, err := latestVersion()
	if err != nil {
		return fmt.Errorf("checking latest version: %w", err)
	}

	if currentVersion != "" && currentVersion == latest {
		fmt.Printf("Already up to date (%s)\n", currentVersion)
		return nil
	}

	if currentVersion == "" {
		fmt.Printf("Latest release is %s, downloading...\n", latest)
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

func latestVersion() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var r release
	// Limit response body to 1 MB to prevent unbounded memory usage.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	if strings.TrimSpace(r.TagName) == "" || strings.ContainsAny(r.TagName, "/\\") {
		return "", errors.New("release response contains an invalid tag")
	}
	return r.TagName, nil
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
	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()

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
	written, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxBinarySize+1))
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
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
