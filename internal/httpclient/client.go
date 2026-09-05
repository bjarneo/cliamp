// Package httpclient provides a shared HTTP client configured for audio streaming.
package httpclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/proxy"
)

// Streaming is a shared HTTP client for audio streaming connections.
// It sets a generous header timeout but no overall timeout, so infinite
// live streams (Icecast/SHOUTcast) aren't killed. HTTP/2 is explicitly
// disabled via TLSNextProto because Icecast/SHOUTcast servers don't
// support it — Go's default ALPN negotiation causes EOF.
//
// Proxy is read from the environment (HTTP_PROXY, HTTPS_PROXY, NO_PROXY)
// so users behind corporate or local proxies aren't bypassed; the rest of
// the codebase uses http.DefaultTransport, which already honors these vars.
var Streaming = &http.Client{Transport: newStreamingTransport()}

// resolveEnvProxy resolves the proxy that applies to a request for the
// given scheme+addr, honoring HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY
// exactly like the standard library does: httpproxy.FromEnvironment()
// reads those same variables (NO_PROXY included) and applies the same
// host/port bypass matching net/http itself uses. addr is "host:port";
// scheme is "http" or "https" so the scheme-specific proxy variable
// (HTTP_PROXY vs HTTPS_PROXY) is honored per request rather than picking
// one proxy for every request regardless of scheme.
func resolveEnvProxy(scheme, addr string) (*url.URL, error) {
	reqURL := &url.URL{Scheme: scheme, Host: addr}
	u, err := httpproxy.FromEnvironment().ProxyFunc()(reqURL)
	if err != nil {
		return nil, fmt.Errorf("resolve environment proxy for %s://%s: %w", scheme, addr, err)
	}
	return u, nil
}

// socks5DialerFromURL builds a SOCKS5 dialer for u (scheme socks5/socks5h).
//
// If u carries credentials, they're refused unless the proxy is reached
// over loopback: the SOCKS5 handshake itself is unauthenticated/unencrypted
// on the wire, so a username/password would otherwise cross the network in
// the clear. A local proxy (127.0.0.1/::1 -- the common case for VPN
// clients that expose a SOCKS5 endpoint on the machine itself) doesn't
// have that exposure, so credentials are allowed there.
func socks5DialerFromURL(u *url.URL) (proxy.Dialer, error) {
	var auth *proxy.Auth
	if u.User != nil {
		host, _, err := net.SplitHostPort(u.Host)
		if err != nil {
			host = u.Host
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("refusing to send SOCKS5 credentials to non-local proxy %q over an unencrypted connection", u.Host)
		}
		auth = &proxy.Auth{User: u.User.Username()}
		if pw, ok := u.User.Password(); ok {
			auth.Password = pw
		}
	}
	return proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
}

// socks5DialerFor returns a SOCKS5 dialer to use for a request of the
// given scheme to addr, or nil if the environment's proxy configuration
// (NO_PROXY included) doesn't call for SOCKS5 here -- either no proxy
// applies, or it's a plain http(s) proxy that net/http.Transport's own
// Proxy field already knows how to speak to.
func socks5DialerFor(scheme, addr string) (proxy.Dialer, error) {
	u, err := resolveEnvProxy(scheme, addr)
	if err != nil || u == nil {
		return nil, err
	}
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return nil, nil
	}
	d, err := socks5DialerFromURL(u)
	if err != nil {
		return nil, fmt.Errorf("build SOCKS5 dialer for %s: %w", u.Host, err)
	}
	return d, nil
}

func newStreamingTransport() *http.Transport {
	tr := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}

	// Delegate to the environment exactly like http.ProxyFromEnvironment,
	// EXCEPT when the resolved proxy is socks5/socks5h: net/http.Transport
	// only understands plain HTTP proxying and HTTP CONNECT tunneling
	// against an http(s):// proxy, nothing else. If HTTPS_PROXY/HTTP_PROXY
	// is set to a socks5:// URL, Transport would dial the proxy's address
	// and write an HTTP request/CONNECT at it; a SOCKS5 server doesn't
	// understand either, so the connection hangs forever with no error.
	// Returning nil here for that case tells Transport "no proxy, dial
	// the target directly" -- DialContext/DialTLSContext below then do
	// the actual SOCKS5 dial themselves, re-resolving per request so
	// NO_PROXY and the scheme-specific *_PROXY variable are both honored,
	// not just baked in once at transport-construction time.
	tr.Proxy = func(req *http.Request) (*url.URL, error) {
		u, err := httpproxy.FromEnvironment().ProxyFunc()(req.URL)
		if err != nil {
			return nil, fmt.Errorf("resolve proxy for %s: %w", req.URL, err)
		}
		if u == nil {
			return nil, nil
		}
		if u.Scheme == "socks5" || u.Scheme == "socks5h" {
			return nil, nil
		}
		return u, nil
	}

	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialMaybeSOCKS5(ctx, "http", network, addr)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		return newICYConn(conn), nil
	}
	tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		config := &tls.Config{}
		if tr.TLSClientConfig != nil {
			config = tr.TLSClientConfig.Clone()
		}
		if config.ServerName == "" {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("split TLS address %q: %w", addr, err)
			}
			config.ServerName = host
		}
		// Icecast and SHOUTcast servers only support HTTP/1.x.
		config.NextProtos = nil

		rawConn, err := dialMaybeSOCKS5(ctx, "https", network, addr)
		if err != nil {
			return nil, fmt.Errorf("dial TLS %s: %w", addr, err)
		}
		tlsConn := tls.Client(rawConn, config)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("dial TLS %s: %w", addr, err)
		}
		return newICYConn(tlsConn), nil
	}
	return tr
}

// dialMaybeSOCKS5 dials addr directly, unless the environment's proxy
// configuration calls for a SOCKS5 proxy for a request of this scheme to
// this addr, in which case it dials through that proxy instead. Context
// cancellation applies to both paths (the SOCKS5 dialer's context-aware
// DialContext is used when the proxy supports it, which proxy.SOCKS5's
// dialer does).
func dialMaybeSOCKS5(ctx context.Context, scheme, network, addr string) (net.Conn, error) {
	d, err := socks5DialerFor(scheme, addr)
	if err != nil {
		return nil, fmt.Errorf("resolve dialer for %s: %w", addr, err)
	}
	if d == nil {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("dial %s directly: %w", addr, err)
		}
		return conn, nil
	}
	if cd, ok := d.(proxy.ContextDialer); ok {
		conn, err := cd.DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("dial %s via SOCKS5: %w", addr, err)
		}
		return conn, nil
	}
	conn, err := d.Dial(network, addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s via SOCKS5: %w", addr, err)
	}
	return conn, nil
}
