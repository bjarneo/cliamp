package luaplugin

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// md5 was removed from the plugin API (weak crypto); guard against it
// creeping back in.
func TestCryptoMD5Removed(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerCryptoAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	if err := L.DoString(`assert(cliamp.crypto.md5 == nil)`); err != nil {
		t.Fatal(err)
	}
}

func TestCryptoSHA256(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerCryptoAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	err := L.DoString(`_G.hash = cliamp.crypto.sha256("hello")`)
	if err != nil {
		t.Fatal(err)
	}

	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := L.GetGlobal("hash").String(); got != want {
		t.Fatalf("sha256('hello') = %q, want %q", got, want)
	}
}

func TestCryptoHMACSHA256(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerCryptoAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	err := L.DoString(`_G.hash = cliamp.crypto.hmac_sha256("key", "message")`)
	if err != nil {
		t.Fatal(err)
	}

	want := "6e9ef29b75fffc5b7abae527d58fdadb2fe42e7219011976917343065f58ed4a"
	if got := L.GetGlobal("hash").String(); got != want {
		t.Fatalf("hmac_sha256('key','message') = %q, want %q", got, want)
	}
}
