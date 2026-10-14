package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// keyCodeNames gives the key name of each tea key code that a handler
// switches on through msg.Code.
var keyCodeNames = map[string]string{
	"KeyEscape": "esc", "KeyEnter": "enter", "KeySpace": "space", "KeyTab": "tab",
	"KeyBackspace": "backspace", "KeyDelete": "delete",
	"KeyUp": "up", "KeyDown": "down", "KeyLeft": "left", "KeyRight": "right",
	"KeyHome": "home", "KeyEnd": "end", "KeyPgUp": "pgup", "KeyPgDown": "pgdown",
}

// modelFuncs parses the non-test Go files of the package and returns the
// functions and methods by name.
func modelFuncs(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	funcs := make(map[string]*ast.FuncDecl)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if _, dup := funcs[fd.Name.Name]; dup {
				// Two declarations share the name. Mark it, so a lookup fails.
				funcs[fd.Name.Name] = nil
				continue
			}
			funcs[fd.Name.Name] = fd
		}
	}
	return funcs
}

// lookupFunc returns the declaration of name, or fails the test.
func lookupFunc(t *testing.T, funcs map[string]*ast.FuncDecl, name string) *ast.FuncDecl {
	t.Helper()
	fd, ok := funcs[name]
	if !ok {
		t.Fatalf("no function %s in package model", name)
	}
	if fd == nil {
		t.Fatalf("more than one function is named %s", name)
	}
	return fd
}

// handlerKeys returns the keys that fd handles itself. A key counts when fd
// compares it with msg.String(), with a variable that holds msg.String(), or
// with a string parameter named key. A tea key code in a switch on msg.Code
// counts too. The keys of the functions that fd calls do not count.
func handlerKeys(t *testing.T, fd *ast.FuncDecl) []string {
	t.Helper()
	holders := make(map[string]bool)
	for _, field := range fd.Type.Params.List {
		if typ, ok := field.Type.(*ast.Ident); ok && typ.Name == "string" {
			for _, name := range field.Names {
				if name.Name == "key" {
					holders[name.Name] = true
				}
			}
		}
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if assign, ok := n.(*ast.AssignStmt); ok && len(assign.Rhs) == 1 && isMsgString(assign.Rhs[0]) {
			for _, lhs := range assign.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					holders[id.Name] = true
				}
			}
		}
		return true
	})
	isKey := func(e ast.Expr) bool {
		if isMsgString(e) {
			return true
		}
		id, ok := e.(*ast.Ident)
		return ok && holders[id.Name]
	}

	var keys []string
	add := func(e ast.Expr) {
		var key string
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				return
			}
			key, _ = strconv.Unquote(v.Value)
		case *ast.SelectorExpr:
			pkg, ok := v.X.(*ast.Ident)
			if !ok || pkg.Name != "tea" {
				return
			}
			if key, ok = keyCodeNames[v.Sel.Name]; !ok {
				t.Fatalf("%s switches on tea.%s. Add it to keyCodeNames.", fd.Name.Name, v.Sel.Name)
			}
		}
		if key != "" && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SwitchStmt:
			if v.Tag == nil || !isKey(v.Tag) && !isMsgCode(v.Tag) {
				return true
			}
			for _, stmt := range v.Body.List {
				for _, e := range stmt.(*ast.CaseClause).List {
					add(e)
				}
			}
		case *ast.BinaryExpr:
			if v.Op != token.EQL && v.Op != token.NEQ {
				return true
			}
			if isKey(v.X) {
				add(v.Y)
			} else if isKey(v.Y) {
				add(v.X)
			}
		}
		return true
	})
	return keys
}

// handlerCalls returns the names of the m.handle...Key methods that fd calls.
func handlerCalls(fd *ast.FuncDecl) []string {
	var names []string
	for _, name := range methodCalls(fd) {
		if strings.HasPrefix(name, "handle") && strings.HasSuffix(name, "Key") {
			names = append(names, name)
		}
	}
	return names
}

// keyHelpers take keys on behalf of the handler that calls them, in the mode
// of that handler. Their keys count as keys of the caller.
var keyHelpers = []string{"filterKey", "stepListCursor"}

// helperCalls returns the names of the keyHelpers that fd calls, as methods
// on m or as plain functions.
func helperCalls(fd *ast.FuncDecl) []string {
	var names []string
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			if recv, ok := fun.X.(*ast.Ident); ok && recv.Name == "m" {
				name = fun.Sel.Name
			}
		}
		if slices.Contains(keyHelpers, name) && !slices.Contains(names, name) {
			names = append(names, name)
		}
		return true
	})
	return names
}

// methodCalls returns the names of the methods that fd calls on m.
func methodCalls(fd *ast.FuncDecl) []string {
	var names []string
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "m" && !slices.Contains(names, sel.Sel.Name) {
			names = append(names, sel.Sel.Name)
		}
		return true
	})
	return names
}

// shortcutKeys returns the provider shortcut keys that a handler takes
// through the helpers in calls. quickSwitchProvider takes every key of
// providerKeyForShortcut. providerShortcut takes every key but N.
func shortcutKeys(t *testing.T, funcs map[string]*ast.FuncDecl, calls []string) []string {
	t.Helper()
	quick := slices.Contains(calls, "quickSwitchProvider")
	if !quick && !slices.Contains(calls, "providerShortcut") {
		return nil
	}
	keys := handlerKeys(t, lookupFunc(t, funcs, "providerKeyForShortcut"))
	if !quick {
		keys = slices.DeleteFunc(keys, func(key string) bool { return key == "N" })
	}
	return keys
}

// isMsgString reports whether e is msg.String().
func isMsgString(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "String" {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && recv.Name == "msg"
}

// isMsgCode reports whether e is msg.Code.
func isMsgCode(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Code" {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && recv.Name == "msg"
}

// TestHandlerKeysFindsEveryForm checks the walker on the forms that the key
// handlers use.
func TestHandlerKeysFindsEveryForm(t *testing.T) {
	src := `package model
func (m *Model) handleProbeKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" || "ctrl+z" != msg.String() {
		return nil
	}
	key := msg.String()
	if key == "N" {
		return m.handleOtherKey(msg)
	}
	switch key {
	case "a", "b":
	}
	switch msg.String() {
	case "c":
		m.handleOtherKey(msg)
	}
	switch msg.Code {
	case tea.KeyEscape, tea.KeyEnter:
	}
	switch m.focus {
	case "not a key":
	}
	if msg.Code == tea.KeySpace || m.name == "not a key" {
		m.editText("field", &m.name, msg)
	}
	if stepListCursor(key, &m.cursor, 3, 1) || m.filterKey(&m.list, "field", msg, 3, nil) {
		return nil
	}
	return nil
}
func shortcut(key string) string {
	switch key {
	case "S":
		return "spotify"
	}
	return ""
}`
	file, err := parser.ParseFile(token.NewFileSet(), "probe.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	probe := file.Decls[0].(*ast.FuncDecl)
	if got, want := handlerKeys(t, probe), []string{"ctrl+c", "ctrl+z", "N", "a", "b", "c", "esc", "enter"}; !slices.Equal(got, want) {
		t.Errorf("handlerKeys = %q, want %q", got, want)
	}
	if got, want := handlerCalls(probe), []string{"handleOtherKey"}; !slices.Equal(got, want) {
		t.Errorf("handlerCalls = %q, want %q", got, want)
	}
	if got, want := helperCalls(probe), []string{"stepListCursor", "filterKey"}; !slices.Equal(got, want) {
		t.Errorf("helperCalls = %q, want %q", got, want)
	}
	if got, want := handlerKeys(t, file.Decls[1].(*ast.FuncDecl)), []string{"S"}; !slices.Equal(got, want) {
		t.Errorf("handlerKeys(shortcut) = %q, want %q", got, want)
	}
}

func TestShortcutKeysFollowsTheShortcutHelpers(t *testing.T) {
	funcs := modelFuncs(t)
	tests := []struct {
		name  string
		calls []string
		want  []string
	}{
		{name: "quick switch takes N", calls: []string{"editText", "quickSwitchProvider"},
			want: []string{"S", "N", "P", "J", "E", "B", "Y", "C", "X", "M", "Q", "T", "L", "R", "O"}},
		{name: "shortcut leaves N", calls: []string{"providerShortcut"},
			want: []string{"S", "P", "J", "E", "B", "Y", "C", "X", "M", "Q", "T", "L", "R", "O"}},
		{name: "other calls take none", calls: []string{"editText", "switchToProvider"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortcutKeys(t, funcs, tt.calls); !slices.Equal(got, tt.want) {
				t.Errorf("shortcutKeys = %q, want %q", got, tt.want)
			}
		})
	}
}

// subKeyHandlers maps the handlers that serve another command mode than
// the handler that calls them, such as the text field of an overlay. Any
// other handler serves the mode of its caller.
var subKeyHandlers = map[string]commandMode{
	"handleKeymapSearchKey":          commandModeKeymapSearch,
	"handlePlaylistPickerNewNameKey": commandModePlaylistPickerInput,
	"handleFileBrowserSearchKey":     commandModeFileBrowserSearch,
	"handleNavSearchKey":             commandModeNavSearch,
	"handleThemeFilterKey":           commandModeThemePickerFilter,
	"handleVisPickerFilterKey":       commandModeVisPickerFilter,
	"handlePlMgrDirsKey":             commandModePlaylistManagerDirs,
	"handlePlMgrNewNameKey":          commandModePlaylistManagerInput,
	"handlePlMgrRenameKey":           commandModePlaylistManagerInput,
	"handleSubsFilterKey":            commandModeSubsFilter,
}

// listKeys move the cursor or change the view size the same way in every
// list. The keymap shows them once, in its player and library section.
var listKeys = []string{"up", "down", "k", "j", "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end", "g", "G", "ctrl+x"}

// unlistedKeys holds the keys that a handler takes but that commandRegistry
// does not list for the mode of the handler. The keymap and the help line do
// not show them. Most are second keys for Esc or Enter, such as q, h and l.
// The provider shortcuts, such as S and T, have rows only in the main mode.
// The others are actions with no row yet. When a key gets a registry row,
// delete it here.
var unlistedKeys = map[string][]string{
	"handleDeviceKey":               {"d"},
	"handleFileBrowserKey":          {"o", "right", "l", "backspace", "left", "h", "~", "."},
	"handleInfoKey":                 {"i"},
	"handleKeymapKey":               {"?", "backspace", "h", "l"},
	"handleLyricsKey":               {"y"},
	"handleNavAlbumListKey":         {"l", "right", "h", "left", "backspace"},
	"handleNavArtistListKey":        {"l", "right", "h", "left", "backspace"},
	"handleNavBrowserKey":           {"N", "ctrl+f", "S", "P", "J", "E", "B", "Y", "C", "X", "M", "Q", "T", "L", "O"},
	"handleNavGenreListKey":         {"l", "right", "h", "left", "backspace"},
	"handleNavGenreSortKey":         {"l", "right", "h", "left", "backspace"},
	"handleNavMenuKey":              {"l", "right", "N", "backspace", "b"},
	"handleNavTrackListKey":         {"ctrl+h", "a", "q", "h", "left", "backspace"},
	"handleNetSearchResultsKey":     {"ctrl+p", "ctrl+n", "a", "q"},
	"handlePlMgrDirsKey":            {"y", "Y"},
	"handlePlMgrFilterKey":          {"backspace"},
	"handlePlMgrListKey":            {"y", "Y", "/", "l", "right", "r"},
	"handlePlMgrTracksKey":          {"ctrl+h", "/", "backspace", "h", "left"},
	"handlePlaylistManagerKey":      {"S", "N", "P", "J", "E", "B", "Y", "C", "X", "M", "Q", "T", "L", "R", "O"},
	"handlePlaylistPickerKey":       {"backspace"},
	"handleProvPillKey":             {"left", "h", "right", "l", "space"},
	"handleProvSearchKey":           {"ctrl+n", "ctrl+p"},
	"handleProviderPaneKey":         {"y", "Y", "n", "space", "/", "o", "ctrl+j", "ctrl+f", "S", "P", "J", "E", "B", "C", "X", "M", "Q", "T", "L", "R", "O"},
	"handleQueueKey":                {"A"},
	"handleSearchKey":               {"ctrl+n", "ctrl+p"},
	"handleSpeedKey":                {"l", "h", "esc", "backspace"},
	"handleSearchOverlayResultsKey": {"ctrl+p", "ctrl+n", "a", "q"},
	"handleSubsKey":                 {"?"},
	"handleThemeKey":                {"t"},
	"handleVisPickerKey":            {"ctrl+v"},
}

// keyHandlerModes returns the command mode of each handler that an overlay
// or a focused area with its own mode reaches.
func keyHandlerModes(t *testing.T, funcs map[string]*ast.FuncDecl) map[string]commandMode {
	t.Helper()
	roots := make(map[string]commandMode)
	for name, mode := range focusKeyHandlers {
		roots[name] = mode
	}
	for _, spec := range overlayStack {
		name := runtime.FuncForPC(reflect.ValueOf(spec.key).Pointer()).Name()
		name = name[strings.LastIndex(name, ".")+1:]
		open := overlayOpeners[spec.screen]
		if open == nil {
			t.Fatalf("overlayOpeners has no opener for screen %d", spec.screen)
		}
		m := Model{playlist: playlist.New()}
		open(&m)
		roots[name], _ = m.commandContext()
	}

	modes := make(map[string]commandMode)
	var visit func(name string, mode commandMode)
	visit = func(name string, mode commandMode) {
		if sub, ok := subKeyHandlers[name]; ok {
			mode = sub
		}
		if prev, seen := modes[name]; seen {
			if prev != mode {
				t.Errorf("%s serves command modes %d and %d. Add it to subKeyHandlers.", name, prev, mode)
			}
			return
		}
		modes[name] = mode
		for _, callee := range handlerCalls(lookupFunc(t, funcs, name)) {
			visit(callee, mode)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(roots)) {
		visit(name, roots[name])
	}
	return modes
}

// TestKeyHandlersMatchCommandRegistry is a drift guard for the overlays and
// for the focused areas with their own command mode. Each key that a handler
// takes needs a registry row in the mode of the handler, or an entry in
// unlistedKeys. The keys of shortcutKeys count as keys of the handler. Each
// key that a registry row offers in such a mode needs a handler of that
// mode. The keymap runs a row by sending its key.
func TestKeyHandlersMatchCommandRegistry(t *testing.T) {
	funcs := modelFuncs(t)
	modes := keyHandlerModes(t, funcs)
	editorKeys := handlerKeys(t, lookupFunc(t, funcs, "editText"))

	taken := make(map[commandMode][]string)
	handlers := make(map[commandMode][]string)
	unlisted := make(map[string][]string)
	for _, name := range slices.Sorted(maps.Keys(modes)) {
		mode := modes[name]
		fd := lookupFunc(t, funcs, name)
		keys := handlerKeys(t, fd)
		calls := methodCalls(fd)
		for _, helper := range helperCalls(fd) {
			hd := lookupFunc(t, funcs, helper)
			for _, key := range handlerKeys(t, hd) {
				if !slices.Contains(keys, key) {
					keys = append(keys, key)
				}
			}
			calls = append(calls, methodCalls(hd)...)
		}
		for _, key := range shortcutKeys(t, funcs, calls) {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
		handlers[mode] = append(handlers[mode], name)
		taken[mode] = append(taken[mode], keys...)
		if slices.Contains(calls, "editText") {
			taken[mode] = append(taken[mode], editorKeys...)
		}
		for _, key := range keys {
			switch {
			case registryLists(mode, key), slices.Contains(listKeys, key):
			case slices.Contains(unlistedKeys[name], key):
				unlisted[name] = append(unlisted[name], key)
			default:
				t.Errorf("%s takes %q, but commandRegistry has no row for it in mode %d. Add a row, or add the key to unlistedKeys.", name, key, mode)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(unlistedKeys)) {
		for _, key := range unlistedKeys[name] {
			if !slices.Contains(unlisted[name], key) {
				t.Errorf("unlistedKeys names %s %q, which has a registry row now or no handler. Delete it from unlistedKeys.", name, key)
			}
		}
	}

	// The handlers of the main mode are on the main key path, which
	// TestReservedKeysCoversHandleKey checks.
	delete(taken, commandModeMain)
	for _, command := range commandRegistry {
		if command.Mode == commandModeAny {
			continue
		}
		for _, mode := range slices.Sorted(maps.Keys(taken)) {
			if command.Mode&mode == 0 {
				continue
			}
			for _, key := range command.Keys {
				if !slices.Contains(taken[mode], key) {
					t.Errorf("commandRegistry row %q %q offers %q, but no handler of mode %d takes it: %v", command.KeyLabel, command.Label, key, mode, handlers[mode])
				}
			}
		}
	}
}

// registryLists reports whether a registry row lists key in mode.
func registryLists(mode commandMode, key string) bool {
	for _, command := range commandRegistry {
		if command.Mode&mode != 0 && slices.Contains(command.Keys, key) {
			return true
		}
	}
	return false
}
