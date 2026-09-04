// Package httpclient provides a shared HTTP client configured for audio streaming.
package httpclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

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

// socks5ProxyFromEnv looks for a socks5:// or socks5h:// proxy URL in the
// standard proxy environment variables.
//
// net/http.Transport's Proxy field, and the CONNECT-based tunneling it does
// under the hood, only understand plain HTTP proxying and HTTP(S) proxies.
// They have no support for the SOCKS5 protocol. If HTTPS_PROXY/HTTP_PROXY
// is set to a socks5:// URL (common with corporate VPN clients that expose
// a local SOCKS5 endpoint), http.ProxyFromEnvironment still happily returns
// it as "the proxy to use", so Transport dials the proxy's address and then
// writes an HTTP request line or a CONNECT request at it. A SOCKS5 server
// doesn't understand either, so the connection just hangs forever: no
// error, the local TCP connect to the proxy succeeds fine, it just never
// gets a response it can parse. This is handled explicitly below instead.
func socks5ProxyFromEnv() *url.URL {
	for _, env := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		v := os.Getenv(env)
		if v == "" {
			continue
		}
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "socks5" && u.Scheme != "socks5h") {
			continue
		}
		return u
	}
	return nil
}

func socks5DialerFromURL(u *url.URL) (proxy.Dialer, error) {
	var auth *proxy.Auth
	if u.User != nil {
		auth = &proxy.Auth{User: u.User.Username()}
		if pw, ok := u.User.Password(); ok {
			auth.Password = pw
		}
	}
	return proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
}

func newStreamingTransport() *http.Transport {
	var socks5 proxy.Dialer
	if u := socks5ProxyFromEnv(); u != nil {
		if d, err := socks5DialerFromURL(u); err == nil {
			socks5 = d
		}
	}

	tr := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}
	if socks5 == nil {
		// Only hand HTTP(S)-style proxying off to Transport's own logic.
		// When we're dialing through SOCKS5 ourselves (below), Transport
		// must not also try to proxy: it would dial the proxy's address
		// via DialContext and speak CONNECT at it, conflicting with the
		// SOCKS5 dial happening inside DialContext/DialTLSContext.
		tr.Proxy = http.ProxyFromEnvironment
	}

	rawDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if socks5 != nil {
			return socks5.Dial(network, addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := rawDial(ctx, network, addr)
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

		rawConn, err := rawDial(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("dial TLS %s: %w", addr, err)
		}
		tlsConn := tls.Client(rawConn, config)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("dial TLS %s: %w", addr, err)
		}
		return newICYConn(tlsConn), nil
	}
	return tr
}
