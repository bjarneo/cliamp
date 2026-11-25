package luaplugin

import (
	"strings"

	"github.com/bjarneo/cliamp/ui"
	lua "github.com/yuin/gopher-lua"
)

// parseSelectOptions reads a list of select rows. A row is a string (title
// only) or a table with title, subtitle and description. The same shape works
// for select options and input suggestions.
func parseSelectOptions(L *lua.LState, value lua.LValue) []SelectOption {
	list, ok := value.(*lua.LTable)
	if !ok {
		return nil
	}
	var out []SelectOption
	list.ForEach(func(_, value lua.LValue) {
		switch item := value.(type) {
		case lua.LString:
			if text := strings.TrimSpace(string(item)); text != "" {
				out = append(out, SelectOption{Title: text})
			}
		case *lua.LTable:
			title := strings.TrimSpace(rawString(L, item, "title"))
			if title == "" {
				title = strings.TrimSpace(rawString(L, item, "label"))
			}
			if title == "" {
				title = strings.TrimSpace(rawString(L, item, "value"))
			}
			if title != "" {
				out = append(out, SelectOption{
					Title:       title,
					Subtitle:    strings.TrimSpace(rawString(L, item, "subtitle")),
					Description: strings.TrimSpace(rawString(L, item, "description")),
					Image:       rawString(L, item, "image"),
				})
			}
		}
	})
	return out
}

// registerUIAPI adds cliamp.ui.select. It lets a plugin ask the user to pick
// one of several strings, which is the piece a plugin needs to build its own
// settings menu: choose a visualizer, a weight, a layout pane, and so on.
// Gated on permissions = {"ui"}.
func registerUIAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin, m *Manager) {
	tbl := L.NewTable()
	var inputToken uint64

	// cliamp.ui.select{title = "...", options = {"a", "b"}, on_select = fn}
	L.SetField(tbl, "select", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "cliamp.ui.select") {
			return pushErr(L, "ui permission required")
		}
		opts := L.CheckTable(1)
		title := strings.TrimSpace(rawString(L, opts, "title"))
		fn, ok := opts.RawGetString("on_select").(*lua.LFunction)
		if !ok {
			return pushErr(L, "cliamp.ui.select: on_select is required")
		}
		options := parseSelectOptions(L, opts.RawGetString("options"))
		if len(options) == 0 {
			return pushErr(L, "cliamp.ui.select: options is empty")
		}
		up := m.ui.Load()
		if up == nil || up.ShowSelect == nil {
			return pushErr(L, "ui: not available")
		}
		cursor := optIntField(L, opts, "cursor")
		cancelFn, _ := opts.RawGetString("on_cancel").(*lua.LFunction)
		onCancel := func() {}
		if cancelFn != nil {
			onCancel = func() {
				m.enqueue(p, "ui.select cancel", func() {
					m.call(p, "ui.select cancel", hookTimeout, 0, fixedArgs(cancelFn))
				})
			}
		}
		body := rawString(L, opts, "body")
		up.ShowSelect(title, body, options, cursor, func(index int) {
			if index < 0 {
				return
			}
			// The callback runs on the UI goroutine, so it only queues the Lua
			// call: the plugin keeps its single-callback ordering.
			m.enqueue(p, "ui.select", func() {
				m.call(p, "ui.select", hookTimeout, 0, fixedArgs(fn, lua.LNumber(index+1)))
			})
		}, onCancel)
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.ui.input{title = "...", placeholder = "...", value = "...",
	//                 on_submit = fn, on_cancel = fn}
	L.SetField(tbl, "input", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "cliamp.ui.input") {
			return pushErr(L, "ui permission required")
		}
		opts := L.CheckTable(1)
		fn, ok := opts.RawGetString("on_submit").(*lua.LFunction)
		if !ok {
			return pushErr(L, "cliamp.ui.input: on_submit is required")
		}
		up := m.ui.Load()
		if up == nil || up.ShowInput == nil {
			return pushErr(L, "ui: not available")
		}
		title := strings.TrimSpace(rawString(L, opts, "title"))
		placeholder := strings.TrimSpace(rawString(L, opts, "placeholder"))
		value := rawString(L, opts, "value")
		suggestions := parseSelectOptions(L, opts.RawGetString("suggestions"))
		cancelFn, _ := opts.RawGetString("on_cancel").(*lua.LFunction)
		onCancel := func() {}
		if cancelFn != nil {
			onCancel = func() {
				m.enqueue(p, "ui.input cancel", func() {
					m.call(p, "ui.input cancel", hookTimeout, 0, fixedArgs(cancelFn))
				})
			}
		}
		onChangeFn, _ := opts.RawGetString("on_change").(*lua.LFunction)
		onChange := func(text string) {}
		if onChangeFn != nil {
			onChange = func(text string) {
				m.enqueue(p, "ui.input change", func() {
					m.call(p, "ui.input change", hookTimeout, 0, fixedArgs(onChangeFn, lua.LString(text)))
				})
			}
		}
		onSelectFn, _ := opts.RawGetString("on_select").(*lua.LFunction)
		onSelect := func(index int) {}
		if onSelectFn != nil {
			onSelect = func(index int) {
				m.enqueue(p, "ui.input select", func() {
					m.call(p, "ui.input select", hookTimeout, 0, fixedArgs(onSelectFn, lua.LNumber(index+1)))
				})
			}
		}
		inputToken = up.ShowInput(title, placeholder, value, suggestions, onChange, func(text string) {
			m.enqueue(p, "ui.input", func() {
				m.call(p, "ui.input", hookTimeout, 0, fixedArgs(fn, lua.LString(text)))
			})
		}, onSelect, onCancel)
		L.Push(lua.LTrue)
		L.Push(lua.LNumber(inputToken))
		return 2
	}))

	// cliamp.ui.suggestions(options, {live = true}) replaces the list under the
	// open input. live rows are results: Enter selects one instead of
	// submitting the text.
	L.SetField(tbl, "suggestions", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "cliamp.ui.suggestions") {
			return pushErr(L, "ui permission required")
		}
		options := parseSelectOptions(L, L.Get(1))
		live := true
		query := ""
		querySet := false
		token := inputToken
		if opts, ok := L.Get(2).(*lua.LTable); ok {
			if value, ok := opts.RawGetString("live").(lua.LBool); ok {
				live = bool(value)
			}
			if value := opts.RawGetString("query"); value != lua.LNil {
				querySet = true
				query = strings.TrimSpace(value.String())
			}
			if value, ok := opts.RawGetString("token").(lua.LNumber); ok {
				token = uint64(value)
			}
		}
		up := m.ui.Load()
		if up == nil || up.UpdateInput == nil {
			return pushErr(L, "ui: not available")
		}
		up.UpdateInput(options, live, query, querySet, token)
		L.Push(lua.LTrue)
		return 1
	}))

	// cliamp.ui.text{title = "...", text = "...", on_close = fn}
	L.SetField(tbl, "text", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "cliamp.ui.text") {
			return pushErr(L, "ui permission required")
		}
		opts := L.CheckTable(1)
		text := rawString(L, opts, "text")
		if strings.TrimSpace(text) == "" {
			return pushErr(L, "cliamp.ui.text: text is required")
		}
		up := m.ui.Load()
		if up == nil || up.ShowText == nil {
			return pushErr(L, "ui: not available")
		}
		closeFn, _ := opts.RawGetString("on_close").(*lua.LFunction)
		onClose := func() {}
		if closeFn != nil {
			onClose = func() {
				m.enqueue(p, "ui.text close", func() {
					m.call(p, "ui.text close", hookTimeout, 0, fixedArgs(closeFn))
				})
			}
		}
		up.ShowText(strings.TrimSpace(rawString(L, opts, "title")), text, onClose)
		L.Push(lua.LTrue)
		return 1
	}))

	vis := L.NewTable()
	// cliamp.vis.set("Scatter")
	L.SetField(vis, "set", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "cliamp.vis.set") {
			return pushErr(L, "ui permission required")
		}
		name := strings.TrimSpace(L.CheckString(1))
		up := m.ui.Load()
		if up == nil || up.SetVis == nil {
			return pushErr(L, "vis: not available")
		}
		up.SetVis(name)
		L.Push(lua.LTrue)
		return 1
	}))
	// cliamp.vis.list() -> {"Bars", "Rain", ...}
	L.SetField(vis, "list", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermUI, "cliamp.vis.list") {
			return pushErr(L, "ui permission required")
		}
		table := L.NewTable()
		names := ui.VisualizerNames()
		names = append(names, m.Visualizers()...)
		for i, name := range names {
			table.RawSetInt(i+1, lua.LString(name))
		}
		L.Push(table)
		return 1
	}))
	L.SetField(cliamp, "vis", vis)

	L.SetField(cliamp, "ui", tbl)
}
