// Package httpclient provides a shared HTTP client configured for audio streaming.
package httpclient

import (
	"crypto/tls"
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
var Streaming = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
	},
}
