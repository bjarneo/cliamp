package httpclient

import (
	"net/http"
	"net/url"
	"time"
)

// apiTransport is shared by every NewAPI client so that they pool
// connections, as clients on http.DefaultTransport did. It starts from
// DefaultTransport, so HTTP/2 stays on, and it picks a proxy with apiProxy.
// It has no ICY handling.
var apiTransport = newAPITransport()

func newAPITransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = apiProxy
	return tr
}

// apiProxy picks the proxy for req from HTTP_PROXY, HTTPS_PROXY, ALL_PROXY
// and NO_PROXY. Unlike Streaming, it also uses an http proxy from ALL_PROXY.
// It gives a socks5 or socks5h proxy to net/http, which dials it and sends
// the user and password in the proxy URL, as http.DefaultTransport does.
// Streaming refuses SOCKS5 credentials.
func apiProxy(req *http.Request) (*url.URL, error) {
	return resolveEnvProxy(req.URL.Scheme, canonicalAddr(req.URL))
}

// NewAPI returns a client for API, auth and download requests. timeout limits
// each request, including the body read. The client honors HTTP_PROXY,
// HTTPS_PROXY, ALL_PROXY and NO_PROXY as apiProxy describes, and it sends
// UserAgent when a request has no User-Agent header. Unlike Streaming, it
// uses an http proxy from ALL_PROXY and accepts a user and password in a
// socks5 proxy URL.
func NewAPI(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: userAgentTransport{base: apiTransport},
	}
}

// userAgentTransport sets UserAgent on a request that has no User-Agent
// header. A request that sets its own value, or an empty value to send none,
// keeps it.
type userAgentTransport struct {
	base http.RoundTripper
}

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, ok := req.Header["User-Agent"]; ok {
		return t.base.RoundTrip(req)
	}
	// A RoundTripper must not change the request it gets, so set the
	// header on a copy.
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", UserAgent)
	return t.base.RoundTrip(req)
}
