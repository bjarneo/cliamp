package luaplugin

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// imageTestState registers the image API for a plugin with the given
// permissions and returns the state.
func imageTestState(t *testing.T, perms ...string) *lua.LState {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	p := &Plugin{Name: "cover", perms: map[string]bool{}}
	for _, perm := range perms {
		p.perms[perm] = true
	}
	cliamp := L.NewTable()
	registerImageAPI(L, cliamp, p, nil)
	L.SetGlobal("cliamp", cliamp)
	return L
}

// tinyDataURL returns a 4x4 PNG as a data: URL.
func tinyDataURL(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.Set(x, y, color.RGBA{R: uint8(x * 60), G: uint8(y * 60), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestImageAPIRequiresPermission(t *testing.T) {
	L := imageTestState(t)
	if err := L.DoString(`_G.res, _G.err = cliamp.image.show("data:image/png;base64,AA==", {cols = 2, rows = 2})`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("res") != lua.LNil {
		t.Fatalf("show without permission returned %v, want nil", L.GetGlobal("res"))
	}
	if err := L.GetGlobal("err").(lua.LString); err == "" {
		t.Fatalf("show without permission returned no error")
	}
}

func TestImageAPIRejectsBadSize(t *testing.T) {
	L := imageTestState(t, PermImage)
	L.SetGlobal("src", lua.LString(tinyDataURL(t)))
	if err := L.DoString(`_G.res, _G.err = cliamp.image.show(src, {cols = 0, rows = 0})`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("res") != lua.LNil {
		t.Fatalf("show with no size returned %v, want nil", L.GetGlobal("res"))
	}
}

func TestImageAPIBlockRequiresKnownID(t *testing.T) {
	L := imageTestState(t, PermImage)
	if err := L.DoString(`_G.res, _G.err = cliamp.image.block(999999)`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("res") != lua.LNil {
		t.Fatalf("block of an unknown id returned %v, want nil", L.GetGlobal("res"))
	}
}

func TestImageAPIBlocksRendersText(t *testing.T) {
	L := imageTestState(t, PermImage)
	L.SetGlobal("src", lua.LString(tinyDataURL(t)))
	if err := L.DoString(`_G.text, _G.err = cliamp.image.blocks(src, {cols = 4, rows = 2})`); err != nil {
		t.Fatal(err)
	}
	text, ok := L.GetGlobal("text").(lua.LString)
	if !ok {
		t.Fatalf("blocks returned %v (%v), want a string", L.GetGlobal("text"), L.GetGlobal("err"))
	}
	lines := strings.Split(string(text), "\n")
	if len(lines) != 2 {
		t.Fatalf("blocks returned %d lines, want 2: %q", len(lines), text)
	}
	if !strings.Contains(string(text), "▀") {
		t.Fatalf("blocks output has no half-block cells: %q", text)
	}
}

func TestImageAPIClearValidatesPositiveID(t *testing.T) {
	L := imageTestState(t, PermImage)
	for _, id := range []string{"0", "-5"} {
		if err := L.DoString(`_G.res, _G.err = cliamp.image.clear(` + id + `)`); err != nil {
			t.Fatal(err)
		}
		if L.GetGlobal("res") != lua.LNil || !strings.Contains(L.GetGlobal("err").String(), "positive") {
			t.Errorf("clear(%s) = %v, %v; want nil and positive error", id, L.GetGlobal("res"), L.GetGlobal("err"))
		}
	}
}

func TestImageAPIShowRespectsManagerClose(t *testing.T) {
	m := newTestManager()
	p := loadTestPlugin(t, m, "imgclose", `
		plugin.register({name = "imgclose", type = "hook", permissions = {"image"}})
	`)
	m.Close()

	// After Close, m.ctx is cancelled so ShowContext fails immediately.
	p.mu.Lock()
	defer p.mu.Unlock()
	if m.ctx.Err() == nil {
		t.Fatal("manager context was not cancelled by Close")
	}
}
