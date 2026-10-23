package luaplugin

import (
	"strings"

	"github.com/bjarneo/cliamp/v2/internal/kittyimg"
	lua "github.com/yuin/gopher-lua"
)

// registerImageAPI adds cliamp.image.{show,block,clear,blocks} to the cliamp
// table. The image permission reads any regular file the user can read, up to
// 32 MiB: a cover can live anywhere, so paths are not restricted to an image
// directory. Devices and pipes are rejected.
// The image travels to the terminal out of band; block returns the Unicode
// placeholders that display it, so a plugin composes it inside its own frame.
// Gated on permissions = {"image"}.
func registerImageAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin) {
	tbl := L.NewTable()
	guard := func(name string) bool { return p.permitted(PermImage, "cliamp.image."+name) }

	// cliamp.image.show(source, {cols=n, rows=n}) -> id, err
	L.SetField(tbl, "show", L.NewFunction(func(L *lua.LState) int {
		if !guard("show") {
			return pushErr(L, "image permission required")
		}
		source := strings.TrimSpace(L.CheckString(1))
		opts := L.OptTable(2, nil)
		cols, rows := 0, 0
		if opts != nil {
			cols = optIntField(L, opts, "cols")
			rows = optIntField(L, opts, "rows")
		}
		if cols <= 0 || rows <= 0 {
			return pushErr(L, "cliamp.image.show: cols and rows are required")
		}
		placement, err := kittyimg.Show(source, cols, rows, optIntField(L, opts, "id"))
		if err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LNumber(placement.ID))
		return 1
	}))

	// cliamp.image.block(id) -> string, err
	L.SetField(tbl, "block", L.NewFunction(func(L *lua.LState) int {
		if !guard("block") {
			return pushErr(L, "image permission required")
		}
		id := L.CheckInt(1)
		placement, ok := kittyimg.PlacementByID(id)
		if !ok {
			return pushErr(L, "cliamp.image.block: unknown image id")
		}
		L.Push(lua.LString(placement.Block()))
		return 1
	}))

	// cliamp.image.clear(id)
	L.SetField(tbl, "clear", L.NewFunction(func(L *lua.LState) int {
		if !guard("clear") {
			return pushErr(L, "image permission required")
		}
		kittyimg.Clear(L.CheckInt(1))
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.image.blocks(source, {cols=n, rows=n}) -> text, err
	L.SetField(tbl, "blocks", L.NewFunction(func(L *lua.LState) int {
		if !guard("blocks") {
			return pushErr(L, "image permission required")
		}
		source := strings.TrimSpace(L.CheckString(1))
		opts := L.OptTable(2, nil)
		cols, rows := 0, 0
		if opts != nil {
			cols = optIntField(L, opts, "cols")
			rows = optIntField(L, opts, "rows")
		}
		if cols <= 0 || rows <= 0 {
			return pushErr(L, "cliamp.image.blocks: cols and rows are required")
		}
		text, err := kittyimg.Blocks(source, cols, rows)
		if err != nil {
			return pushErr(L, err.Error())
		}
		L.Push(lua.LString(text))
		return 1
	}))

	L.SetField(cliamp, "image", tbl)
}

// optIntField reads an optional integer field from a Lua table.
func optIntField(L *lua.LState, tbl *lua.LTable, key string) int {
	value := tbl.RawGetString(key)
	if n, ok := value.(lua.LNumber); ok {
		return int(n)
	}
	return 0
}
