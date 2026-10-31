package artcache

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return out.Bytes()
}

func TestStoreIsContentAddressed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	raw := testPNG(t)
	first, err := Store(raw)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	second, err := Store(raw)
	if err != nil {
		t.Fatalf("Store again: %v", err)
	}
	if first != second {
		t.Fatalf("URLs differ: %q and %q", first, second)
	}
	u, err := url.Parse(first)
	if err != nil || u.Scheme != "file" {
		t.Fatalf("Store URL = %q, err = %v", first, err)
	}
	got, err := os.ReadFile(filepath.FromSlash(u.Path))
	if err != nil {
		t.Fatalf("read cached artwork: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("cached artwork differs from source")
	}
}

func TestFileURLEscapesPath(t *testing.T) {
	got := FileURL(filepath.Join(t.TempDir(), "cover art.png"))
	if !strings.HasPrefix(got, "file:///") || !strings.Contains(got, "cover%20art.png") {
		t.Fatalf("FileURL = %q", got)
	}
}

func TestResolveDownloadsImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	raw := testPNG(t)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Accept"); got != "image/*" {
			t.Errorf("Accept = %q, want image/*", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	got, err := Resolve(context.Background(), srv.URL+"/cover")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.HasPrefix(got, "file://") {
		t.Fatalf("Resolve = %q, want local file URL", got)
	}
	again, err := Resolve(context.Background(), srv.URL+"/cover")
	if err != nil {
		t.Fatalf("Resolve cached: %v", err)
	}
	if again != got || requests != 1 {
		t.Fatalf("cached Resolve = %q after %d requests, want %q after 1", again, requests, got)
	}
}

func TestStoreForKeyCanBeLookedUp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want, err := StoreForKey("provider:track:42", testPNG(t))
	if err != nil {
		t.Fatalf("StoreForKey: %v", err)
	}
	got, ok := Lookup("provider:track:42")
	if !ok || got != want {
		t.Fatalf("Lookup = (%q,%v), want (%q,true)", got, ok, want)
	}
	if _, ok := Lookup("provider:track:other"); ok {
		t.Fatal("Lookup found artwork under a different key")
	}
}

func TestResolveRejectsCredentialedURL(t *testing.T) {
	_, err := Resolve(context.Background(), "https://user:secret@example.com/cover.jpg")
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("Resolve error = %v, want credentials rejection", err)
	}
}

func TestStoreRejectsUnsupportedImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := Store([]byte("not an image")); err == nil {
		t.Fatal("Store accepted non-image bytes")
	}
}
