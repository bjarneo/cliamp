package httpclient

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// clearProxyEnv resets every proxy-related env var (upper and lower case)
// so each subtest starts from a known-empty state regardless of what the
// test process's own environment happens to have set.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
		"ALL_PROXY", "all_proxy",
		"NO_PROXY", "no_proxy",
	} {
		t.Setenv(k, "")
	}
}

func TestSocks5DialerForScheme(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "socks5h://127.0.0.1:1080")

	d, err := socks5DialerFor("https", "example.com:443")
	if err != nil {
		t.Fatalf("https: unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("https: want a SOCKS5 dialer, got nil (HTTPS_PROXY should apply)")
	}

	d, err = socks5DialerFor("http", "example.com:80")
	if err != nil {
		t.Fatalf("http: unexpected error: %v", err)
	}
	if d != nil {
		t.Fatal("http: want nil (only HTTPS_PROXY is set, HTTP_PROXY should not borrow it)")
	}
}

func TestSocks5DialerForNoProxyBypass(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "socks5h://127.0.0.1:1080")
	t.Setenv("NO_PROXY", "example.com")

	d, err := socks5DialerFor("https", "example.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != nil {
		t.Fatal("want nil dialer: example.com is listed in NO_PROXY and should bypass the SOCKS5 proxy")
	}

	// A host not covered by NO_PROXY should still go through the proxy.
	d, err = socks5DialerFor("https", "other.example:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("want a SOCKS5 dialer for a host not listed in NO_PROXY")
	}
}

func TestSocks5DialerForPlainHTTPProxyDefersToTransport(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTP_PROXY", "http://realproxy.example:8080")

	d, err := socks5DialerFor("http", "example.com:80")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != nil {
		t.Fatal("want nil: a plain http:// proxy should be left to Transport's own Proxy field, not treated as SOCKS5")
	}
}

func TestSocks5DialerFromURLRejectsAnyCredentials(t *testing.T) {
	for _, host := range []string{"proxy.example.com:1080", "127.0.0.1:1080"} {
		u, err := url.Parse("socks5://user:pass@" + host)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := socks5DialerFromURL(u); err == nil {
			t.Fatalf("want an error for credentialed SOCKS5 URL %s: RFC 1929 auth is cleartext even on loopback", host)
		}
	}
}

func TestSocks5DialerFromURLAllowsNoCredentials(t *testing.T) {
	u, err := url.Parse("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := socks5DialerFromURL(u); err != nil {
		t.Fatalf("unexpected error for a SOCKS5 URL without credentials: %v", err)
	}
}

func TestSocks5DialerForALLProxyFallback(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("ALL_PROXY", "socks5h://127.0.0.1:1080")

	d, err := socks5DialerFor("https", "example.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("want a SOCKS5 dialer from ALL_PROXY when no scheme-specific proxy is set")
	}
}

func TestSocks5DialerForAllProxyLowercaseFallback(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("all_proxy", "socks5h://127.0.0.1:1080")

	d, err := socks5DialerFor("http", "example.com:80")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("want a SOCKS5 dialer from lowercase all_proxy")
	}
}

func TestSocks5DialerForALLProxyRespectsNoProxy(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("ALL_PROXY", "socks5h://127.0.0.1:1080")
	t.Setenv("NO_PROXY", "example.com")

	d, err := socks5DialerFor("https", "example.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != nil {
		t.Fatal("want nil: example.com is in NO_PROXY, ALL_PROXY should not override that")
	}
}

// TestDialDecisionIsNotReresolvedFromProxyHopAddress reproduces the mixed
// HTTP_PROXY/HTTPS_PROXY configuration CodeRabbit flagged: an https target
// whose HTTPS_PROXY is a plain http(s) proxy, while HTTP_PROXY happens to be
// a SOCKS5 proxy. net/http.Transport dials the CONNECT tunnel's proxy
// address (not the request's target) through DialContext for this request,
// so the dial decision must come from the request's own scheme (https,
// which resolves to a plain proxy) -- never re-derived from that proxy
// address as if it were something to look up HTTP_PROXY for.
func TestDialDecisionIsNotReresolvedFromProxyHopAddress(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://real-proxy.example:8080")
	t.Setenv("HTTP_PROXY", "socks5h://bogus-proxy.invalid:1080")

	baseCtx := context.Background()
	req, err := http.NewRequestWithContext(baseCtx, http.MethodGet, "https://example.com/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := dialDecisionFor(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.socks5 != nil {
		t.Fatal("want no SOCKS5 dialer: the request's own scheme (https) resolves to a plain http proxy, HTTP_PROXY's SOCKS5 setting must not apply to it")
	}

	// Stand in for net/http.Transport dialing the CONNECT tunnel's proxy
	// address directly (never "example.com:443") with a real, reachable
	// listener. dialWithDecision must reach it directly -- if the dial
	// decision were (wrongly) re-derived from this address instead of
	// read from ctx, HTTP_PROXY's bogus SOCKS5 proxy would apply instead
	// and this dial would fail or hang rather than connecting straight in.
	ln, err := (&net.ListenConfig{}).Listen(baseCtx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
			close(accepted)
		}
	}()

	ctx := context.WithValue(baseCtx, dialDecisionKey{}, decision)
	conn, err := dialWithDecision(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy hop directly: %v", err)
	}
	_ = conn.Close()

	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("listener never accepted a direct connection -- dial was routed elsewhere")
	}
}

func TestSocks5DialerForSchemeSpecificTakesPrecedenceOverALLProxy(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://realproxy.example:8080")
	t.Setenv("ALL_PROXY", "socks5h://127.0.0.1:1080")

	d, err := socks5DialerFor("https", "example.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != nil {
		t.Fatal("want nil: HTTPS_PROXY (a plain http proxy) should be used, not fall back to ALL_PROXY")
	}
}
