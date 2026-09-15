package download

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSave(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		length  string
		success bool
	}{
		{"success", 200, "fake audio", "", true},
		{"http error", 403, "secret response", "", false},
		{"truncated", 200, "short", "100", false},
		{"empty", 200, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("credentials forwarded")
				}
				if tc.length != "" {
					w.Header().Set("Content-Length", tc.length)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			path, err := Save(context.Background(), server.Client(), server.URL+"/?signature=secret", dir, "../../Artist\x00\\Song", ".mp3")
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !tc.success {
				if err == nil || path != "" || len(entries) != 0 {
					t.Fatalf("path=%q err=%v entries=%v", path, err, entries)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("sensitive data in error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(path) != dir || filepath.Ext(path) != ".mp3" || len(entries) != 1 {
				t.Fatalf("invalid result: %q, %v", path, entries)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tc.body {
				t.Fatalf("data=%q err=%v", data, err)
			}
			second, err := Save(context.Background(), server.Client(), server.URL, dir, "../../Artist\x00\\Song", ".mp3")
			if err != nil || second == path {
				t.Fatalf("repeat download overwrote file: %q %v", second, err)
			}
		})
	}
}

func TestSaveCancellationCleansPart(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write([]byte("audio"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	// Cancel only once copying has started and a .part exists.
	client := server.Client()
	client.Transport = cancelOnReadTransport{base: client.Transport, cancel: cancel, dir: dir, t: t}
	path, err := Save(ctx, client, server.URL, dir, "song", ".mp3")
	if path != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("path=%q err=%v", path, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

type cancelOnReadTransport struct {
	base   http.RoundTripper
	cancel context.CancelFunc
	dir    string
	t      *testing.T
}

func (c cancelOnReadTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.base.RoundTrip(r)
	if err == nil {
		resp.Body = &cancelBody{ReadCloser: resp.Body, c: c}
	}
	return resp, err
}

type cancelBody struct {
	io.ReadCloser
	c cancelOnReadTransport
}

func (b *cancelBody) Read(p []byte) (int, error) {
	entries, _ := os.ReadDir(b.c.dir)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".part") {
		b.c.t.Error("expected temporary .part during transfer")
	}
	b.c.cancel()
	return b.ReadCloser.Read(p)
}

func TestFilename(t *testing.T) {
	for _, name := range []string{"", "..", "../../evil", `C:\\evil<>:*?"|`, "\x00\n", "CON", strings.Repeat("音", 200)} {
		got := Filename(name)
		if got == "" || strings.ContainsAny(got, "/\\\x00\n:*?\"<>|") || len(got) > 166 || filepath.Base(got) != got {
			t.Errorf("unsafe filename %q", got)
		}
	}
}
