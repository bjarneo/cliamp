package httpclient

import (
	"bytes"
	"cmp"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeSOCKS5 is a SOCKS5 proxy for tests. It accepts CONNECT requests,
// records the requested host and connects every tunnel to backend. With a
// non-nil auth, it accepts only RFC 1929 authentication with that user and
// password. Without auth, it accepts no authentication.
type fakeSOCKS5 struct {
	ln      net.Listener
	backend string
	auth    *url.Userinfo

	mu    sync.Mutex
	hosts []string
}

func startFakeSOCKS5(t *testing.T, backend string, auth *url.Userinfo) *fakeSOCKS5 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeSOCKS5{ln: ln, backend: backend, auth: auth}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(conn)
		}
	}()
	return p
}

func (p *fakeSOCKS5) requestedHosts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.hosts...)
}

func (p *fakeSOCKS5) serve(conn net.Conn) {
	defer conn.Close()
	// Greeting: version, method count, methods.
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if p.auth == nil {
		if _, err := conn.Write([]byte{5, 0}); err != nil {
			return
		}
	} else if !p.authenticate(conn, methods) {
		return
	}
	// Request: version, CONNECT, reserved, address type, address, port.
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return
		}
		name := make([]byte, n[0])
		if _, err := io.ReadFull(conn, name); err != nil {
			return
		}
		host = string(name)
	default:
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return
	}
	p.mu.Lock()
	p.hosts = append(p.hosts, net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))))
	p.mu.Unlock()

	up, err := net.Dial("tcp", p.backend)
	if err != nil {
		return
	}
	defer up.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(up, conn) }()
	_, _ = io.Copy(conn, up)
}

// authenticate selects RFC 1929 user and password authentication and checks
// the credentials that the client sends against p.auth.
func (p *fakeSOCKS5) authenticate(conn net.Conn, methods []byte) bool {
	if !bytes.Contains(methods, []byte{2}) {
		_, _ = conn.Write([]byte{5, 0xff})
		return false
	}
	if _, err := conn.Write([]byte{5, 2}); err != nil {
		return false
	}
	// Request: version, user length, user, password length, password.
	readField := func() (string, bool) {
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return "", false
		}
		b := make([]byte, n[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return "", false
		}
		return string(b), true
	}
	if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
		return false
	}
	user, ok := readField()
	if !ok {
		return false
	}
	pass, ok := readField()
	if !ok {
		return false
	}
	wantPass, _ := p.auth.Password()
	if user != p.auth.Username() || pass != wantPass {
		_, _ = conn.Write([]byte{1, 1})
		return false
	}
	_, err := conn.Write([]byte{1, 0})
	return err == nil
}

func TestNewAPIProxyAndUserAgent(t *testing.T) {
	uaCh := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uaCh <- r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(backend.Close)
	t.Cleanup(apiTransport.CloseIdleConnections)
	backendAddr := backend.Listener.Addr().String()

	tests := []struct {
		name string
		// proxyScheme is the scheme of the proxy URL. Empty means no proxy.
		proxyScheme string
		// proxyEnv is the variable that holds the proxy URL. Empty means
		// ALL_PROXY.
		proxyEnv string
		// proxyAuth is the user and password in the proxy URL. The proxy
		// then requires them.
		proxyAuth *url.Userinfo
		// url is the request URL. A proxied request uses a host that does
		// not resolve, so only the proxy can reach the backend.
		url string
		// setUA sets userAgent on the request before the call.
		setUA     bool
		userAgent string
		wantUA    string
		wantHost  string
	}{
		{
			name:   "direct request gets the cliamp User-Agent",
			url:    backend.URL + "/api",
			wantUA: UserAgent,
		},
		{
			name:        "ALL_PROXY socks5 carries the request",
			proxyScheme: "socks5",
			url:         "http://api.example.test:8096/api",
			wantUA:      UserAgent,
			wantHost:    "api.example.test:8096",
		},
		{
			name:        "ALL_PROXY socks5h keeps a caller User-Agent",
			proxyScheme: "socks5h",
			url:         "http://feeds.example.test/api",
			setUA:       true,
			userAgent:   "podcast/2",
			wantUA:      "podcast/2",
			wantHost:    "feeds.example.test:80",
		},
		{
			name:        "HTTP_PROXY socks5 sends the user and password in the URL",
			proxyScheme: "socks5",
			proxyEnv:    "HTTP_PROXY",
			proxyAuth:   url.UserPassword("user", "secret"),
			url:         "http://api.example.test:8096/api",
			wantUA:      UserAgent,
			wantHost:    "api.example.test:8096",
		},
		{
			name:      "an empty caller User-Agent sends none",
			url:       backend.URL + "/api",
			setUA:     true,
			userAgent: "",
			wantUA:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearProxyEnv(t)
			var proxy *fakeSOCKS5
			if tt.proxyScheme != "" {
				proxy = startFakeSOCKS5(t, backendAddr, tt.proxyAuth)
				env := cmp.Or(tt.proxyEnv, "ALL_PROXY")
				u := &url.URL{Scheme: tt.proxyScheme, User: tt.proxyAuth, Host: proxy.ln.Addr().String()}
				t.Setenv(env, u.String())
			}

			req, err := http.NewRequest(http.MethodGet, tt.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.setUA {
				req.Header.Set("User-Agent", tt.userAgent)
			}
			resp, err := NewAPI(5 * time.Second).Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(body) != "ok" {
				t.Fatalf("body = %q, want ok", body)
			}
			if got := <-uaCh; got != tt.wantUA {
				t.Errorf("User-Agent = %q, want %q", got, tt.wantUA)
			}
			if got := req.Header.Get("User-Agent"); got != tt.userAgent {
				t.Errorf("NewAPI changed the caller's request headers: %v", req.Header)
			}
			if proxy == nil {
				return
			}
			if hosts := proxy.requestedHosts(); len(hosts) != 1 || hosts[0] != tt.wantHost {
				t.Errorf("proxy CONNECT hosts = %v, want [%s]", hosts, tt.wantHost)
			}
		})
	}
}

// TestAPIProxy verifies that NewAPI hands the proxy URL, credentials
// included, to net/http, and that it keeps the ALL_PROXY and NO_PROXY rules.
func TestAPIProxy(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		url  string
		want string
	}{
		{
			name: "HTTPS_PROXY socks5 keeps its credentials",
			env:  map[string]string{"HTTPS_PROXY": "socks5://user:secret@proxy.example:1080"},
			url:  "https://api.example.test/api",
			want: "socks5://user:secret@proxy.example:1080",
		},
		{
			name: "HTTPS_PROXY does not apply to http",
			env:  map[string]string{"HTTPS_PROXY": "socks5://user:secret@proxy.example:1080"},
			url:  "http://api.example.test/api",
		},
		{
			name: "HTTP_PROXY http proxy",
			env:  map[string]string{"HTTP_PROXY": "http://user:secret@proxy.example:3128"},
			url:  "http://api.example.test/api",
			want: "http://user:secret@proxy.example:3128",
		},
		{
			name: "ALL_PROXY socks5h with credentials",
			env:  map[string]string{"ALL_PROXY": "socks5h://user:secret@proxy.example:1080"},
			url:  "https://api.example.test/api",
			want: "socks5h://user:secret@proxy.example:1080",
		},
		{
			name: "NO_PROXY bypasses ALL_PROXY",
			env: map[string]string{
				"ALL_PROXY": "socks5h://proxy.example:1080",
				"NO_PROXY":  "api.example.test",
			},
			url: "https://api.example.test/api",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearProxyEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			req, err := http.NewRequest(http.MethodGet, tt.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := apiProxy(req)
			if err != nil {
				t.Fatalf("apiProxy: %v", err)
			}
			if gotStr := urlString(got); gotStr != tt.want {
				t.Errorf("apiProxy = %q, want %q", gotStr, tt.want)
			}
		})
	}
}

// urlString returns u as a string, or "" for a nil u.
func urlString(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func TestNewAPIClient(t *testing.T) {
	if c := NewAPI(7 * time.Second); c.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v, want 7s", c.Timeout)
	}

	// The API transport keeps HTTP/2, which Streaming turns off for Icecast.
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	clearProxyEnv(t)
	tr := newAPITransport()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	tr.TLSClientConfig = &tls.Config{RootCAs: roots}
	client := &http.Client{Transport: tr}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Errorf("Proto = %s, want HTTP/2", resp.Proto)
	}
}

// TestNewAPIDialErrorKeepsAddress verifies that a failed dial through NewAPI
// keeps the *net.OpError and the TCP address of the server in the error chain.
// netdiag.Explain needs both to add the macOS Local Network hint.
func TestNewAPIDialErrorKeepsAddress(t *testing.T) {
	clearProxyEnv(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	want := srv.Listener.Addr().(*net.TCPAddr)
	srv.Close()

	resp, err := NewAPI(10 * time.Second).Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("Get to a closed server succeeded")
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("error %v has no *net.OpError", err)
	}
	got, ok := opErr.Addr.(*net.TCPAddr)
	if !ok || !got.IP.Equal(want.IP) || got.Port != want.Port {
		t.Errorf("OpError.Addr = %v, want %v", opErr.Addr, want)
	}
}
