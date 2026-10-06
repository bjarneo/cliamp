// Package httpclienttest provides a test proxy that shows whether a client
// follows the proxy variables, as clients from httpclient.NewAPI do.
package httpclienttest

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Proxy is a plain HTTP proxy for tests. It forwards nothing. It records the
// target host of each request and answers a plain http request with its
// handler. It refuses CONNECT, so an https request through it fails after
// the proxy records the host.
type Proxy struct {
	mu    sync.Mutex
	hosts []string
}

// UseAllProxy starts a Proxy and points ALL_PROXY at it for the rest of the
// test. It clears the other proxy variables, so only ALL_PROXY applies.
// handler answers the plain http requests. A nil handler answers 200 with an
// empty body.
func UseAllProxy(t *testing.T, handler http.Handler) *Proxy {
	t.Helper()
	if handler == nil {
		handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	p := &Proxy{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hosts = append(p.hosts, r.Host)
		p.mu.Unlock()
		if r.Method == http.MethodConnect {
			http.Error(w, "the test proxy opens no tunnels", http.StatusBadGateway)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "all_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("ALL_PROXY", srv.URL)
	return p
}

// Hosts returns the target host of each request that reached the proxy, in
// order. A CONNECT request gives host:port.
func (p *Proxy) Hosts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.hosts...)
}
