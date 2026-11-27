package kittyimg

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPercentEncodedAndLocalhostFileURL(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "my cover #1.png")
	want := []byte("fake-png-bytes")
	if err := os.WriteFile(imgPath, want, 0o600); err != nil {
		t.Fatal(err)
	}

	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(imgPath)}
	got, err := load(u.String())
	if err != nil {
		t.Fatalf("load(%q) error = %v", u.String(), err)
	}
	if string(got) != string(want) {
		t.Fatalf("load(%q) = %q, want %q", u.String(), got, want)
	}

	localhostURL := &url.URL{Scheme: "file", Host: "localhost", Path: filepath.ToSlash(imgPath)}
	got, err = load(localhostURL.String())
	if err != nil {
		t.Fatalf("load(%q) error = %v", localhostURL.String(), err)
	}
	if string(got) != string(want) {
		t.Fatalf("load(%q) = %q, want %q", localhostURL.String(), got, want)
	}

	if _, err := load("file://remote.example.com/cover.png"); err == nil {
		t.Fatal("load(remote file:// host) succeeded, want error")
	}
}
