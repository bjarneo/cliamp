package luaplugin

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"

	lua "github.com/yuin/gopher-lua"
)

// registerCryptoAPI adds cliamp.crypto.{md5,sha256,hmac_sha256} to the cliamp table.
func registerCryptoAPI(L *lua.LState, cliamp *lua.LTable) {
	tbl := L.NewTable()

	L.SetField(tbl, "md5", L.NewFunction(func(L *lua.LState) int {
		s := L.CheckString(1)
		h := md5.Sum([]byte(s))
		L.Push(lua.LString(hex.EncodeToString(h[:])))
		return 1
	}))

	L.SetField(tbl, "sha256", L.NewFunction(func(L *lua.LState) int {
		s := L.CheckString(1)
		h := sha256.Sum256([]byte(s))
		L.Push(lua.LString(hex.EncodeToString(h[:])))
		return 1
	}))

	L.SetField(tbl, "hmac_sha256", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		msg := L.CheckString(2)
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(msg))
		L.Push(lua.LString(hex.EncodeToString(mac.Sum(nil))))
		return 1
	}))

	L.SetField(cliamp, "crypto", tbl)
}
