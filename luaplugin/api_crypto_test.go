package luaplugin

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestCryptoMD5(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cliamp := L.NewTable()
	registerCryptoAPI(L, cliamp)
	L.SetGlobal("cliamp", cliamp)

	err := L.DoString(`_G.hash = cliamp.crypto.md5("hello")`)
	if err != nil {
		t.Fatal(err)
	}

	want := "5d41402abc4b2a76b9719d911017c592"
	if got := L.GetGlobal("hash").String(); got != want {
		t.Fatalf("md5('hello') = %q, want %q", got, want)
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
