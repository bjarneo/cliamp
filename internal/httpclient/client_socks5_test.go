package httpclient

import (
	"net/url"
	"testing"
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

func TestSocks5DialerFromURLRejectsRemoteCredentials(t *testing.T) {
	u, err := url.Parse("socks5://user:pass@proxy.example.com:1080")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := socks5DialerFromURL(u); err == nil {
		t.Fatal("want an error: credentials to a non-loopback SOCKS5 proxy would cross the network unencrypted")
	}
}

func TestSocks5DialerFromURLAllowsLoopbackCredentials(t *testing.T) {
	u, err := url.Parse("socks5://user:pass@127.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := socks5DialerFromURL(u); err != nil {
		t.Fatalf("unexpected error for a loopback proxy: %v", err)
	}
}
