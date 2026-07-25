package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamingClientExists(t *testing.T) {
	if Streaming == nil {
		t.Fatal("Streaming client is nil")
	}
}

func TestStreamingHeaderTimeout(t *testing.T) {
	tr, ok := Streaming.Transport.(*http.Transport)
	if !ok {
		t.Fatal("Transport is not *http.Transport")
	}
	want := 30 * time.Second
	if tr.ResponseHeaderTimeout != want {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, want)
	}
}

func TestStreamingHTTP2Disabled(t *testing.T) {
	tr, ok := Streaming.Transport.(*http.Transport)
	if !ok {
		t.Fatal("Transport is not *http.Transport")
	}
	if tr.TLSNextProto == nil {
		t.Fatal("TLSNextProto is nil, should be empty map to disable HTTP/2")
	}
	if len(tr.TLSNextProto) != 0 {
		t.Fatalf("TLSNextProto has %d entries, want 0", len(tr.TLSNextProto))
	}
}

func TestStreamingNoOverallTimeout(t *testing.T) {
	if Streaming.Timeout != 0 {
		t.Fatalf("Timeout = %v, want 0 (infinite streams)", Streaming.Timeout)
	}
}

func TestStreamingClientAcceptsICYStatus(t *testing.T) {
	for _, tt := range []struct {
		name string
		tls  bool
	}{
		{name: "http"},
		{name: "https", tls: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				defer conn.Close()
				_, _ = rw.WriteString("ICY 200 OK\r\nContent-Length: 5\r\nContent-Type: audio/mpeg\r\n\r\nhello")
				_ = rw.Flush()
			}))
			if tt.tls {
				server.StartTLS()
			} else {
				server.Start()
			}
			t.Cleanup(server.Close)

			tr := newStreamingTransport()
			if tt.tls {
				roots := x509.NewCertPool()
				roots.AddCert(server.Certificate())
				tr.TLSClientConfig = &tls.Config{RootCAs: roots}
			}
			client := &http.Client{Transport: tr}
			resp, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(body), "hello"; got != want {
				t.Fatalf("body = %q, want %q", got, want)
			}
			if got, want := resp.Proto, "HTTP/1.0"; got != want {
				t.Fatalf("Proto = %q, want %q", got, want)
			}
		})
	}
}
