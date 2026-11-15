// Package httpclient provides a shared HTTP client configured for audio
// streaming, a constructor for API clients that read the same proxy
// variables, and helpers that read API response bodies.
package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/proxy"

	"github.com/bjarneo/cliamp/applog"
)

// UserAgent is shared by API and stream requests so Navidrome identifies them
// as the same player when applying per-player settings.
const UserAgent = "cliamp/1.0 (https://github.com/bjarneo/cliamp)"

// Streaming is a shared HTTP client for audio streaming connections.
// It sets a generous header timeout but no overall timeout, so infinite
// live streams (Icecast/SHOUTcast) aren't killed. HTTP/2 is explicitly
// disabled via TLSNextProto because Icecast/SHOUTcast servers don't
// support it — Go's default ALPN negotiation causes EOF.
//
// Proxy is read from the environment (HTTP_PROXY, HTTPS_PROXY, ALL_PROXY,
// NO_PROXY) so users behind corporate or local proxies aren't bypassed.
// ALL_PROXY applies to streams only when it names a socks5 or socks5h
// proxy. With an http proxy in ALL_PROXY, a stream dials its server
// directly. NewAPI clients use that http proxy. http.DefaultTransport
// ignores ALL_PROXY.
var Streaming = &http.Client{Transport: &socks5RoundTripper{transport: newStreamingTransport()}}

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
	if u != nil {
		return u, nil
	}
	// httpproxy.FromEnvironment() only understands HTTP_PROXY/HTTPS_PROXY
	// and NO_PROXY -- it does not fall back to ALL_PROXY the way
	// golang.org/x/net/proxy.FromEnvironment does. Without this, setting
	// only ALL_PROXY=socks5h://... (a common convention for "use this
	// proxy for everything") would silently dial every connection
	// directly instead of through the SOCKS5 proxy. Re-resolve using
	// ALL_PROXY/all_proxy as both the HTTP and HTTPS proxy so
	// httpproxy.Config still applies its own NO_PROXY bypass logic.
	allProxy := firstNonEmpty(os.Getenv("ALL_PROXY"), os.Getenv("all_proxy"))
	if allProxy == "" {
		return nil, nil
	}
	cfg := &httpproxy.Config{
		HTTPProxy:  allProxy,
		HTTPSProxy: allProxy,
		NoProxy:    firstNonEmpty(os.Getenv("NO_PROXY"), os.Getenv("no_proxy")),
	}
	u, err = cfg.ProxyFunc()(reqURL)
	if err != nil {
		return nil, fmt.Errorf("resolve ALL_PROXY for %s://%s: %w", scheme, addr, err)
	}
	return u, nil
}

// firstNonEmpty returns the first non-empty string, or "" if all are empty --
// used to check an env var's upper- and lowercase spelling.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// canonicalAddr returns u's authority as "host:port", applying the scheme's
// default port (80/443) when u carries none -- matching how net/http itself
// canonicalizes a request URL's address before proxy/dial resolution.
func canonicalAddr(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	if u.Scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// dialDecisionKey is the context key socks5RoundTripper.RoundTrip uses to
// pin a request's resolved dial decision so DialContext/DialTLSContext can
// read it back later.
type dialDecisionKey struct{}

// dialDecision is resolved once per request, from the request's own scheme
// and target host, and travels with req.Context() down into
// DialContext/DialTLSContext -- both of which net/http.Transport calls
// with that same context object. This matters because the address those
// two functions are asked to dial is NOT always the request's target: for
// a request routed through a plain http(s) proxy, Transport dials the
// PROXY's address itself (to open the CONNECT tunnel, or to relay a plain
// http request), never the origin host. Re-resolving *_PROXY env vars
// against that proxy address (as if it were the thing to reach) can pick
// a completely unrelated proxy for it -- e.g. HTTPS_PROXY=http://real:8080
// together with HTTP_PROXY=socks5://other:1080 previously made the dial
// to real:8080 get routed through other:1080 via SOCKS5, instead of
// dialing real:8080 directly like it should. Pinning the decision once,
// from the true target, avoids that entirely: whatever address Transport
// later dials for this connection, the SOCKS5-or-not decision is already
// fixed and correct.
type dialDecision struct {
	socks5 proxy.Dialer // nil: no SOCKS5 for this connection, dial addr directly
}

func dialDecisionFor(req *http.Request) (*dialDecision, error) {
	d, err := socks5DialerFor(req.URL.Scheme, canonicalAddr(req.URL))
	if err != nil {
		return nil, err
	}
	return &dialDecision{socks5: d}, nil
}

// socks5RoundTripper wraps a *http.Transport to resolve each request's
// dial decision (see dialDecision) exactly once, from the request itself,
// before handing off to the real Transport -- which will call
// DialContext/DialTLSContext with the same context one or more times as
// it establishes (or reuses) the underlying connection.
type socks5RoundTripper struct {
	transport *http.Transport
}

func (rt *socks5RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	decision, err := dialDecisionFor(req)
	if err != nil {
		return nil, fmt.Errorf("resolve proxy dial decision for %s: %w", req.URL, err)
	}
	ctx := context.WithValue(req.Context(), dialDecisionKey{}, decision)
	return rt.transport.RoundTrip(req.WithContext(ctx))
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
	if u.User != nil {
		// RFC 1929 SOCKS5 username/password authentication is sent in
		// cleartext on the wire. A loopback-only restriction still limits
		// exposure to a REMOTE eavesdropper, but not to another local,
		// unprivileged process capturing loopback traffic -- so it is not
		// a safe enough bar to forward credentials on. Fail clearly
		// instead of silently sending (or silently dropping) them.
		return nil, fmt.Errorf("SOCKS5 credentials in proxy URL for %q are not supported: RFC 1929 auth is sent in cleartext, even over loopback", u.Host)
	}
	return proxy.SOCKS5("tcp", u.Host, nil, proxy.Direct)
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

// transportProxy delegates to the environment exactly like
// http.ProxyFromEnvironment, EXCEPT when the resolved proxy is
// socks5/socks5h. net/http.Transport can dial a SOCKS5 proxy itself, but it
// then runs the SOCKS5 handshake through the icyConn of the Streaming
// DialContext and adds TLS without the Streaming DialTLSContext, so icyConn
// never sees the ICY status line.
// Returning nil here for that case tells Transport "no proxy, dial the
// target directly" -- the transport's dial functions then do the actual
// SOCKS5 dial themselves through dialWithDecision, re-resolving per request
// so NO_PROXY and the scheme-specific *_PROXY variable are both honored,
// not just baked in once at transport-construction time.
func transportProxy(req *http.Request) (*url.URL, error) {
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

func newStreamingTransport() *http.Transport {
	tr := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		Proxy:                 transportProxy,
	}

	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialWithDecision(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		return newICYConn(conn), nil
	}
	// Behind an http or https proxy, Transport runs the TLS handshake with
	// the stream server itself, through a CONNECT tunnel and with
	// TLSClientConfig. The legacy TLS fallback below does not cover that
	// handshake.
	var legacy legacyTLSAddrs
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

		tlsConn, err := dialStreamTLS(ctx, network, addr, config, &legacy)
		if err != nil {
			return nil, err
		}
		return newICYConn(tlsConn), nil
	}
	return tr
}

// dialStreamTLS dials addr with config. When the server rejects config with
// a handshake_failure alert, it dials again with legacyTLSConfig and records
// addr in legacy. The next dial of addr then uses legacyTLSConfig at once.
func dialStreamTLS(ctx context.Context, network, addr string, config *tls.Config, legacy *legacyTLSAddrs) (*tls.Conn, error) {
	if legacy.has(addr) {
		return dialTLS(ctx, network, addr, legacyTLSConfig(config))
	}
	conn, err := dialTLS(ctx, network, addr, config)
	if err == nil || !isHandshakeFailure(err) {
		return conn, err
	}
	conn, fbErr := dialTLS(ctx, network, addr, legacyTLSConfig(config))
	if fbErr != nil {
		return nil, fmt.Errorf("%w (legacy TLS retry: %w)", err, fbErr)
	}
	if legacy.add(addr) {
		suite := tls.CipherSuiteName(conn.ConnectionState().CipherSuite)
		applog.Warn("httpclient: %s rejects the default TLS config. Using TLS 1.2 with %s", addr, suite)
	}
	return conn, nil
}

// dialTLS dials addr and runs the TLS handshake with config.
func dialTLS(ctx context.Context, network, addr string, config *tls.Config) (*tls.Conn, error) {
	rawConn, err := dialWithDecision(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("dial TLS %s: %w", addr, err)
	}
	tlsConn := tls.Client(rawConn, config)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("dial TLS %s: %w", addr, err)
	}
	return tlsConn, nil
}

// isHandshakeFailure reports whether err holds a handshake_failure alert
// from the server. crypto/tls returns a received alert as an unexported type
// inside a "remote error" *net.OpError, not as tls.AlertError. The check
// therefore compares the alert text with the text of tls.AlertError.
func isHandshakeFailure(err error) bool {
	const alertHandshakeFailure = tls.AlertError(40)
	opErr, ok := errors.AsType[*net.OpError](err)
	return ok && opErr.Op == "remote error" && opErr.Err != nil &&
		opErr.Err.Error() == alertHandshakeFailure.Error()
}

// legacyCipherSuites holds the secure TLS 1.2 suites of crypto/tls and the
// 2 RSA key exchange suites that legacy Icecast servers accept. Go 1.22
// removed RSA key exchange from the default suites, because RSA key exchange
// has no forward secrecy. Only the streaming fallback uses these suites.
var legacyCipherSuites = func() []uint16 {
	var ids []uint16
	for _, s := range tls.CipherSuites() {
		if slices.Contains(s.SupportedVersions, tls.VersionTLS12) {
			ids = append(ids, s.ID)
		}
	}
	return append(ids, tls.TLS_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_RSA_WITH_AES_256_GCM_SHA384)
}()

// legacyTLSConfig returns a copy of config for servers that reject the
// default TLS config. Certificate verification stays on.
func legacyTLSConfig(config *tls.Config) *tls.Config {
	legacy := config.Clone()
	legacy.MaxVersion = tls.VersionTLS12
	legacy.CipherSuites = legacyCipherSuites
	return legacy
}

// maxLegacyTLSAddrs limits legacyTLSAddrs. An address that drops out costs
// 1 failed handshake on its next dial.
const maxLegacyTLSAddrs = 64

// legacyTLSAddrs holds the "host:port" addresses that need the legacy TLS
// config. It is safe for concurrent use.
type legacyTLSAddrs struct {
	mu    sync.Mutex
	addrs map[string]struct{}
}

func (l *legacyTLSAddrs) has(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.addrs[addr]
	return ok
}

// add records addr and reports whether addr is new.
func (l *legacyTLSAddrs) add(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.addrs[addr]; ok {
		return false
	}
	if l.addrs == nil {
		l.addrs = make(map[string]struct{})
	}
	if len(l.addrs) >= maxLegacyTLSAddrs {
		for k := range l.addrs {
			delete(l.addrs, k)
			break
		}
	}
	l.addrs[addr] = struct{}{}
	return true
}

// dialWithDecision dials addr directly, unless ctx carries a *dialDecision
// (stashed by socks5RoundTripper.RoundTrip, from the original request's own
// scheme and target host) that calls for a SOCKS5 proxy, in which case it
// dials through that proxy instead. Context cancellation applies to both
// paths (the SOCKS5 dialer's context-aware DialContext is used when the
// proxy supports it, which proxy.SOCKS5's dialer does).
//
// Deliberately NOT re-resolved from addr here: see dialDecision's doc
// comment for why that was wrong for connections routed through a plain
// http(s) proxy.
func dialWithDecision(ctx context.Context, network, addr string) (net.Conn, error) {
	decision, _ := ctx.Value(dialDecisionKey{}).(*dialDecision)
	var d proxy.Dialer
	if decision != nil {
		d = decision.socks5
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
