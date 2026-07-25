// Package httpclient provides a shared HTTP client configured for audio streaming.
package httpclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"
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

func newStreamingTransport() *http.Transport {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
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

		conn, err := (&tls.Dialer{Config: config}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("dial TLS %s: %w", addr, err)
		}
		return newICYConn(conn), nil
	}
	return tr
}
