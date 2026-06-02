package luaplugin

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// ssrfGuard rejects connections to non-public addresses. It runs as the
// dialer's Control hook, so it sees the resolved IP for every connection
// attempt, including ones reached via HTTP redirects or DNS rebinding.
func ssrfGuard(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("cannot resolve dial address %q", address)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("blocked request to non-public address %s", ip)
	}
	return nil
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
		L.Push(lua.LNil)
		L.Push(lua.LString("only http and https URLs are allowed"))
		return 2
	}
	opts := L.OptTable(2, nil)

	var bodyReader io.Reader
	if opts != nil {
		// JSON body: cliamp.http.post(url, {json = {...}})
		if jsonVal := opts.RawGetString("json"); jsonVal != lua.LNil {
			goVal := luaToGo(jsonVal)
			data, err := json.Marshal(goVal)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			bodyReader = strings.NewReader(string(data))
		}

		// Raw body: cliamp.http.post(url, {body = "..."})
		if body := opts.RawGetString("body"); body != lua.LNil {
			bodyReader = strings.NewReader(body.String())
		}
	}

	req, err := http.NewRequest(method, rawURL, bodyReader)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
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
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	L.Push(lua.LString(string(body)))
	L.Push(lua.LNumber(resp.StatusCode))
	return 2
}
