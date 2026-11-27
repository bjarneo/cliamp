package luaplugin

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// allowedPrivateAddrs holds the non-public IP:port pairs that ssrfGuard lets
// through, from [plugins] allowed_private_addresses in config.toml.
var allowedPrivateAddrs atomic.Pointer[map[string]bool]

// blockedPrefixes lists special-purpose IP ranges that ssrfGuard blocks in
// addition to loopback, private, link-local, multicast, and unspecified ranges.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("fec0::/10"),
}

// isNonPublicAddr reports whether addr falls in a non-public or special range.
func isNonPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.Zone() != "" {
		return true
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// ssrfGuard rejects connections to non-public addresses. It runs as the
// dialer's Control hook so it inspects the resolved IP after redirects and DNS
// resolution. Exact IP:port entries in allowedPrivateAddrs bypass the block.
func ssrfGuard(network, address string, _ syscall.RawConn) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("cannot resolve dial address %q", address)
	}
	addr = addr.Unmap()
	if isNonPublicAddr(addr) {
		if addr.Zone() == "" {
			if allowed := allowedPrivateAddrs.Load(); allowed != nil && (*allowed)[net.JoinHostPort(addr.String(), port)] {
				return nil
			}
		}
		return fmt.Errorf("blocked request to non-public address %s", addr)
	}
	return nil
}

// resolveAllowedPrivateAddrs parses [plugins] allowed_private_addresses as a
// comma-separated list of ip:port pairs and returns valid entries plus invalid
// entries to log.
func resolveAllowedPrivateAddrs(pluginCfg map[string]map[string]string) (allowed map[string]bool, invalid []string) {
	raw := pluginCfg[""]["allowed_private_addresses"]
	allowed = make(map[string]bool)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		host, port, err := net.SplitHostPort(entry)
		addr, addrErr := netip.ParseAddr(host)
		number, portErr := strconv.Atoi(port)
		if err != nil || addrErr != nil || addr.Zone() != "" || portErr != nil || number < 1 || number > 65535 {
			invalid = append(invalid, entry)
			continue
		}
		addr = addr.Unmap()
		allowed[net.JoinHostPort(addr.String(), strconv.Itoa(number))] = true
	}
	return allowed, invalid
}

var httpClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
			Control: ssrfGuard,
		}).DialContext,
	},
}

// registerHTTPAPI adds cliamp.http.{get,post} to the cliamp table.
func registerHTTPAPI(L *lua.LState, cliamp *lua.LTable) {
	tbl := L.NewTable()

	// cliamp.http.get(url, opts?) -> body, status
	L.SetField(tbl, "get", L.NewFunction(func(L *lua.LState) int {
		return doHTTP(L, "GET")
	}))

	// cliamp.http.post(url, opts?) -> body, status
	L.SetField(tbl, "post", L.NewFunction(func(L *lua.LState) int {
		return doHTTP(L, "POST")
	}))

	L.SetField(cliamp, "http", tbl)
}

const maxResponseBody = 1 << 20 // 1MB

func doHTTP(L *lua.LState, method string) int {
	rawURL := L.CheckString(1)
	if u, err := url.Parse(rawURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return pushErr(L, "only http and https URLs are allowed")
	}
	opts := L.OptTable(2, nil)

	var bodyReader io.Reader
	if opts != nil {
		// JSON body: cliamp.http.post(url, {json = {...}})
		if jsonVal := opts.RawGetString("json"); jsonVal != lua.LNil {
			goVal, err := luaToGoCtx(callContext(L), jsonVal)
			if err != nil {
				return pushErr(L, err.Error())
			}
			data, err := json.Marshal(goVal)
			if err != nil {
				return pushErr(L, err.Error())
			}
			bodyReader = strings.NewReader(string(data))
		}

		// Raw body: cliamp.http.post(url, {body = "..."})
		if body := opts.RawGetString("body"); body != lua.LNil {
			bodyReader = strings.NewReader(body.String())
		}
	}

	req, err := http.NewRequestWithContext(callContext(L), method, rawURL, bodyReader)
	if err != nil {
		return pushErr(L, err.Error())
	}

	// Apply headers from opts.
	if opts != nil {
		if hdrs := opts.RawGetString("headers"); hdrs != lua.LNil {
			if tbl, ok := hdrs.(*lua.LTable); ok {
				tbl.ForEach(func(k, v lua.LValue) {
					req.Header.Set(k.String(), v.String())
				})
			}
		}
		// Auto-set Content-Type for JSON if not explicitly set.
		if opts.RawGetString("json") != lua.LNil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return pushErr(L, err.Error())
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return pushErr(L, err.Error())
	}

	L.Push(lua.LString(string(body)))
	L.Push(lua.LNumber(resp.StatusCode))
	return 2
}
