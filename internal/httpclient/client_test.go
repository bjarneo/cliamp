package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamingClientExists(t *testing.T) {
	if Streaming == nil {
		t.Fatal("Streaming client is nil")
	}
}

// streamingTransport unwraps Streaming's socks5RoundTripper to reach the
// underlying *http.Transport it configures.
func streamingTransport(t *testing.T) *http.Transport {
	t.Helper()
	rt, ok := Streaming.Transport.(*socks5RoundTripper)
	if !ok {
		t.Fatal("Transport is not *socks5RoundTripper")
	}
	return rt.transport
}

func TestStreamingHeaderTimeout(t *testing.T) {
	tr := streamingTransport(t)
	want := 30 * time.Second
	if tr.ResponseHeaderTimeout != want {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, want)
	}
}

func TestStreamingHTTP2Disabled(t *testing.T) {
	tr := streamingTransport(t)
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

func TestStreamingLegacyTLSFallback(t *testing.T) {
	rsaOnly := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256},
	}
	for _, tt := range []struct {
		name      string
		serverTLS *tls.Config
		// clientTLS replaces the default client config, which trusts roots.
		clientTLS func(roots *x509.CertPool) *tls.Config
		wantErr   bool
		wantSuite uint16
		wantVer   uint16
		// wantConns is the server connection count after each request.
		wantConns [2]int32
	}{
		{
			// The first dial fails with handshake_failure and the retry
			// succeeds. The second request dials the legacy config at once.
			name:      "RSA-only server uses the fallback",
			serverTLS: rsaOnly,
			wantSuite: tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			wantVer:   tls.VersionTLS12,
			wantConns: [2]int32{2, 3},
		},
		{
			name:      "modern server keeps the default config",
			wantSuite: tls.TLS_AES_128_GCM_SHA256,
			wantVer:   tls.VersionTLS13,
			wantConns: [2]int32{1, 2},
		},
		{
			name:      "untrusted certificate does not retry",
			clientTLS: func(*x509.CertPool) *tls.Config { return &tls.Config{} },
			wantErr:   true,
			wantConns: [2]int32{1, 2},
		},
		{
			// The server sends a protocol_version alert, not handshake_failure.
			name:      "other server alert does not retry",
			serverTLS: &tls.Config{MinVersion: tls.VersionTLS13},
			clientTLS: func(roots *x509.CertPool) *tls.Config {
				return &tls.Config{RootCAs: roots, MaxVersion: tls.VersionTLS12}
			},
			wantErr:   true,
			wantConns: [2]int32{1, 2},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var conns atomic.Int32
			var gotSuite, gotVer atomic.Uint32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotSuite.Store(uint32(r.TLS.CipherSuite))
				gotVer.Store(uint32(r.TLS.Version))
				_, _ = io.WriteString(w, "ok")
			}))
			server.TLS = tt.serverTLS
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					conns.Add(1)
				}
			}
			server.StartTLS()
			t.Cleanup(server.Close)

			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			tr := newStreamingTransport()
			tr.TLSClientConfig = &tls.Config{RootCAs: roots}
			if tt.clientTLS != nil {
				tr.TLSClientConfig = tt.clientTLS(roots)
			}
			t.Cleanup(tr.CloseIdleConnections)
			client := &http.Client{Transport: tr}

			for i, wantConns := range tt.wantConns {
				resp, err := client.Get(server.URL)
				if tt.wantErr {
					if err == nil {
						resp.Body.Close()
						t.Fatalf("request %d: want an error, got status %d", i, resp.StatusCode)
					}
				} else {
					if err != nil {
						t.Fatalf("request %d: %v", i, err)
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
					}
					if got := uint16(gotSuite.Load()); got != tt.wantSuite {
						t.Fatalf("request %d: cipher suite = %s, want %s", i, tls.CipherSuiteName(got), tls.CipherSuiteName(tt.wantSuite))
					}
					if got := uint16(gotVer.Load()); got != tt.wantVer {
						t.Fatalf("request %d: TLS version = %#x, want %#x", i, got, tt.wantVer)
					}
				}
				if got := conns.Load(); got != wantConns {
					t.Fatalf("request %d: server connections = %d, want %d", i, got, wantConns)
				}
				// Close the idle connection so the next request dials again.
				tr.CloseIdleConnections()
			}
		})
	}
}

func TestLegacyTLSAddrsBounded(t *testing.T) {
	var l legacyTLSAddrs
	if !l.add("a:443") {
		t.Fatal("add of a new address returned false")
	}
	if l.add("a:443") {
		t.Fatal("add of a known address returned true")
	}
	for i := range maxLegacyTLSAddrs * 2 {
		l.add(fmt.Sprintf("host%d:443", i))
	}
	if got := len(l.addrs); got != maxLegacyTLSAddrs {
		t.Fatalf("len = %d, want %d", got, maxLegacyTLSAddrs)
	}
	if last := fmt.Sprintf("host%d:443", maxLegacyTLSAddrs*2-1); !l.has(last) {
		t.Fatalf("has(%q) = false, want true", last)
	}
}
