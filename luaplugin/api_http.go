package luaplugin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

var httpClient = &http.Client{
	Timeout: 5 * time.Second,
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
	url := L.CheckString(1)
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

	req, err := http.NewRequest(method, url, bodyReader)
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
