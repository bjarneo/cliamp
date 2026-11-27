package luaplugin

import (
	"fmt"
	"math"
	"strings"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/pm"
)

const (
	// maxStringOutputBytes caps output from string.rep, string.format, and
	// string.gsub to block memory exhaustion inside Go stdlib calls.
	maxStringOutputBytes = 4 << 20 // 4 MiB

	// maxPatternInputBytes caps the subject string length for pattern matching.
	maxPatternInputBytes = 1 << 20 // 1 MiB

	// maxPatternBytes caps the pattern length for pattern matching.
	maxPatternBytes = 1024
)

var safeOSFuncs = []string{"clock", "date", "difftime", "getenv", "time"}

// newSandboxedState creates a Lua VM with only the safe allowlist of libraries.
func newSandboxedState() *lua.LState {
	L := lua.NewState(lua.Options{
		SkipOpenLibs: true,
	})
	openSafeLibs(L)
	sandbox(L)
	return L
}

// openSafeLibs opens the allowed Lua standard libraries and builds a filtered
// os table with only clock, date, difftime, getenv, and time.
func openSafeLibs(L *lua.LState) {
	for _, lib := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
		{lua.CoroutineLibName, lua.OpenCoroutine},
	} {
		L.Push(L.NewFunction(lib.fn))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}

	// Open the full os module briefly to copy the five allowed functions,
	// then replace the global and registry entries with the filtered table.
	L.Push(L.NewFunction(lua.OpenOs))
	L.Push(lua.LString(lua.OsLibName))
	L.Call(1, 0)
	filterOSModule(L)
}

// filterOSModule replaces the global os table and its _LOADED entry with a
// fresh table that contains only the five safe os functions.
func filterOSModule(L *lua.LState) {
	safeOS := L.NewTable()
	if osTbl, ok := L.GetGlobal(lua.OsLibName).(*lua.LTable); ok {
		for _, name := range safeOSFuncs {
			if fn := osTbl.RawGetString(name); fn != lua.LNil {
				safeOS.RawSetString(name, fn)
			}
		}
	}
	L.SetGlobal(lua.OsLibName, safeOS)
	if reg, ok := L.Get(lua.RegistryIndex).(*lua.LTable); ok {
		if loaded, ok := reg.RawGetString("_LOADED").(*lua.LTable); ok {
			loaded.RawSetString(lua.OsLibName, safeOS)
		}
	}
}

// sandbox restricts a Lua VM to the safe global surface, wraps string
// functions with memory and pattern limits, binds coroutine contexts, and adds
// utf8.char.
func sandbox(L *lua.LState) {
	// Remove unsafe base functions and libraries.
	for _, name := range []string{
		"dofile", "loadfile", "load", "loadstring",
		"require", "module", "_printregs", "collectgarbage",
		"package", "debug", "io", "channel",
	} {
		L.SetGlobal(name, lua.LNil)
	}
	if reg, ok := L.Get(lua.RegistryIndex).(*lua.LTable); ok {
		if loaded, ok := reg.RawGetString("_LOADED").(*lua.LTable); ok {
			for _, name := range []string{"package", "debug", "io", "channel"} {
				loaded.RawSetString(name, lua.LNil)
			}
		}
	}

	// Replace print with a no-op so untrusted metadata reads and early chunks
	// do not write to stdout. registerLogAPI routes print to plugins.log later.
	L.SetGlobal("print", L.NewFunction(func(*lua.LState) int { return 0 }))

	filterOSModule(L)
	wrapStringLib(L)
	bindCoroutineContext(L)

	// Provide utf8.char for Lua 5.1 compatibility in Braille visualizers.
	utf8Tbl := L.NewTable()
	L.SetField(utf8Tbl, "char", L.NewFunction(luaUTF8Char))
	L.SetGlobal("utf8", utf8Tbl)
}

// wrapStringLib wraps string functions to enforce output size and pattern caps.
func wrapStringLib(L *lua.LState) {
	strTbl, ok := L.GetGlobal(lua.StringLibName).(*lua.LTable)
	if !ok {
		return
	}

	strTbl.RawSetString("rep", L.NewFunction(luaBoundedStringRep))

	if origFormat, ok := strTbl.RawGetString("format").(*lua.LFunction); ok {
		strTbl.RawSetString("format", L.NewFunction(func(L *lua.LState) int {
			return luaBoundedStringFormat(L, origFormat)
		}))
	}

	for _, name := range []string{"find", "match", "gmatch", "gfind"} {
		orig, ok := strTbl.RawGetString(name).(*lua.LFunction)
		if !ok {
			continue
		}
		fnName := name
		strTbl.RawSetString(fnName, L.NewFunction(func(L *lua.LState) int {
			checkPatternLimits(L, fnName)
			n := L.GetTop()
			L.Insert(orig, 1)
			L.Call(n, lua.MultRet)
			return L.GetTop()
		}))
	}

	strTbl.RawSetString("gsub", L.NewFunction(luaBoundedStringGsub))
}

// luaBoundedStringGsub replaces gopher-lua's string.gsub with a single-pass
// linear implementation that checks context cancellation and enforces
// maxStringOutputBytes incrementally before allocating large outputs.
func luaBoundedStringGsub(L *lua.LState) int {
	checkPatternLimits(L, "gsub")
	str := L.CheckString(1)
	pat := L.CheckString(2)
	L.CheckTypes(3, lua.LTString, lua.LTNumber, lua.LTTable, lua.LTFunction)
	repl := L.CheckAny(3)
	limit := L.OptInt(4, -1)

	if replStr, ok := repl.(lua.LString); ok && len(replStr) > maxStringOutputBytes {
		L.RaiseError("string.gsub replacement exceeds %d bytes", maxStringOutputBytes)
		return 0
	}

	mds, err := pm.Find(pat, []byte(str), 0, limit)
	if err != nil {
		L.RaiseError("%s", err.Error())
		return 0
	}
	if len(mds) == 0 {
		L.Push(lua.LString(str))
		L.Push(lua.LNumber(0))
		return 2
	}

	var out strings.Builder
	appendChecked := func(s string) {
		if out.Len()+len(s) > maxStringOutputBytes {
			L.RaiseError("string.gsub output exceeds %d bytes", maxStringOutputBytes)
		}
		out.WriteString(s)
	}
	appendByteChecked := func(b byte) {
		if out.Len()+1 > maxStringOutputBytes {
			L.RaiseError("string.gsub output exceeds %d bytes", maxStringOutputBytes)
		}
		out.WriteByte(b)
	}
	capturedString := func(m *pm.MatchData, idx int) string {
		if idx > 2 && idx >= m.CaptureLength() {
			L.RaiseError("invalid capture index")
		}
		if idx >= m.CaptureLength() && idx == 2 {
			idx = 0
		}
		if m.IsPosCapture(idx) {
			return fmt.Sprint(m.Capture(idx))
		}
		return str[m.Capture(idx):m.Capture(idx+1)]
	}

	last := 0
	ctx := L.Context()
	for _, match := range mds {
		if ctx != nil && ctx.Err() != nil {
			L.RaiseError("%s", ctx.Err().Error())
		}
		start, end := match.Capture(0), match.Capture(1)
		if start > last {
			appendChecked(str[last:start])
		}
		switch lv := repl.(type) {
		case lua.LString, lua.LNumber:
			replStr := lua.LVAsString(lv)
			for i := 0; i < len(replStr); i++ {
				if replStr[i] == '%' && i+1 < len(replStr) {
					i++
					c := replStr[i]
					switch {
					case c == '%':
						appendByteChecked('%')
					case c >= '0' && c <= '9':
						appendChecked(capturedString(match, 2*int(c-'0')))
					default:
						appendByteChecked('%')
						appendByteChecked(c)
					}
				} else {
					appendByteChecked(replStr[i])
				}
			}
		case *lua.LTable:
			idx := 0
			if match.CaptureLength() > 2 {
				idx = 2
			}
			var val lua.LValue
			if match.IsPosCapture(idx) {
				val = L.GetTable(lv, lua.LNumber(match.Capture(idx)))
			} else {
				val = L.GetField(lv, str[match.Capture(idx):match.Capture(idx+1)])
			}
			if !lua.LVIsFalse(val) {
				appendChecked(lua.LVAsString(val))
			} else {
				appendChecked(str[start:end])
			}
		case *lua.LFunction:
			L.Push(lv)
			nargs := 0
			if match.CaptureLength() > 2 {
				for i := 2; i < match.CaptureLength(); i += 2 {
					if match.IsPosCapture(i) {
						L.Push(lua.LNumber(match.Capture(i)))
					} else {
						L.Push(lua.LString(capturedString(match, i)))
					}
					nargs++
				}
			} else {
				L.Push(lua.LString(capturedString(match, 0)))
				nargs++
			}
			L.Call(nargs, 1)
			ret := L.Get(-1)
			L.Pop(1)
			if !lua.LVIsFalse(ret) {
				appendChecked(lua.LVAsString(ret))
			} else {
				appendChecked(str[start:end])
			}
		}
		last = end
	}
	if last < len(str) {
		appendChecked(str[last:])
	}
	L.Push(lua.LString(out.String()))
	L.Push(lua.LNumber(len(mds)))
	return 2
}

// luaBoundedStringRep enforces maxStringOutputBytes before repeating a string.
func luaBoundedStringRep(L *lua.LState) int {
	s := L.CheckString(1)
	nNum := float64(L.CheckNumber(2))
	if math.IsNaN(nNum) || nNum <= 0 || len(s) == 0 {
		L.Push(lua.LString(""))
		return 1
	}
	if math.IsInf(nNum, 1) || nNum > float64(maxStringOutputBytes) {
		L.RaiseError("string.rep output exceeds %d bytes", maxStringOutputBytes)
		return 0
	}
	n := int(nNum)
	if n > maxStringOutputBytes/len(s) {
		L.RaiseError("string.rep output exceeds %d bytes", maxStringOutputBytes)
		return 0
	}
	L.Push(lua.LString(strings.Repeat(s, n)))
	return 1
}

// luaBoundedStringFormat validates format widths and output budget before
// running Go's fmt.Sprintf, then checks the final output length.
func luaBoundedStringFormat(L *lua.LState, orig *lua.LFunction) int {
	fmtStr := L.CheckString(1)
	if err := checkFormatBudget(L, fmtStr); err != nil {
		L.RaiseError("%v", err)
		return 0
	}
	n := L.GetTop()
	L.Insert(orig, 1)
	L.Call(n, 1)
	if out, ok := L.Get(-1).(lua.LString); ok && len(out) > maxStringOutputBytes {
		L.Pop(1)
		L.RaiseError("string.format output exceeds %d bytes", maxStringOutputBytes)
		return 0
	}
	return 1
}

// checkFormatBudget estimates an upper bound on fmt.Sprintf output for fmtStr
// and rejects directives that would exceed maxStringOutputBytes.
func checkFormatBudget(L *lua.LState, fmtStr string) error {
	if len(fmtStr) > maxStringOutputBytes {
		return fmt.Errorf("string.format output exceeds %d bytes", maxStringOutputBytes)
	}
	top := L.GetTop()
	argAt := func(idx int) lua.LValue {
		if idx >= 1 && idx+1 <= top {
			return L.Get(idx + 1)
		}
		return lua.LNil
	}

	var total int64
	nextArg := 1
	for i := 0; i < len(fmtStr); {
		if fmtStr[i] != '%' {
			total++
			if total > maxStringOutputBytes {
				return fmt.Errorf("string.format output exceeds %d bytes", maxStringOutputBytes)
			}
			i++
			continue
		}
		i++
		if i >= len(fmtStr) {
			break
		}
		if fmtStr[i] == '%' {
			total++
			if total > maxStringOutputBytes {
				return fmt.Errorf("string.format output exceeds %d bytes", maxStringOutputBytes)
			}
			i++
			continue
		}

		// Skip flags.
		for i < len(fmtStr) && strings.IndexByte("+-# 0", fmtStr[i]) >= 0 {
			i++
		}

		argIdx := nextArg
		if idx, ok := parseBracketIndex(fmtStr, &i); ok {
			argIdx = idx
		}

		// Parse field width.
		var width int64
		if i < len(fmtStr) && fmtStr[i] == '*' {
			i++
			w, err := dynamicFormatSize(argAt(argIdx))
			if err != nil {
				return err
			}
			width = w
			argIdx++
		} else {
			w, err := parseFormatDigits(fmtStr, &i)
			if err != nil {
				return err
			}
			width = w
		}

		// Parse precision.
		var prec int64
		hasPrec := false
		if i < len(fmtStr) && fmtStr[i] == '.' {
			hasPrec = true
			i++
			if idx, ok := parseBracketIndex(fmtStr, &i); ok {
				argIdx = idx
			}
			if i < len(fmtStr) && fmtStr[i] == '*' {
				i++
				p, err := dynamicFormatSize(argAt(argIdx))
				if err != nil {
					return err
				}
				prec = p
				argIdx++
			} else {
				p, err := parseFormatDigits(fmtStr, &i)
				if err != nil {
					return err
				}
				prec = p
			}
		}

		if idx, ok := parseBracketIndex(fmtStr, &i); ok {
			argIdx = idx
		}

		var verb byte
		if i < len(fmtStr) {
			verb = fmtStr[i]
			i++
		}

		itemLen := estimateFormatArg(argAt(argIdx), verb, prec, hasPrec, maxStringOutputBytes-total)
		nextArg = argIdx + 1

		contrib := max(width, itemLen)
		total += contrib
		if total > maxStringOutputBytes {
			return fmt.Errorf("string.format output exceeds %d bytes", maxStringOutputBytes)
		}
	}
	return nil
}

// parseBracketIndex reads a Go fmt explicit argument index such as [2].
func parseBracketIndex(s string, pos *int) (int, bool) {
	i := *pos
	if i >= len(s) || s[i] != '[' {
		return 0, false
	}
	i++
	start := i
	val := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		if val <= maxStringOutputBytes {
			val = val*10 + int(s[i]-'0')
		}
		i++
	}
	if i == start || i >= len(s) || s[i] != ']' {
		return 0, false
	}
	*pos = i + 1
	return val, true
}

// parseFormatDigits reads a decimal width or precision and fails if it exceeds
// maxStringOutputBytes.
func parseFormatDigits(s string, pos *int) (int64, error) {
	var val int64
	for *pos < len(s) && s[*pos] >= '0' && s[*pos] <= '9' {
		val = val*10 + int64(s[*pos]-'0')
		if val > maxStringOutputBytes {
			return 0, fmt.Errorf("string.format output exceeds %d bytes", maxStringOutputBytes)
		}
		*pos++
	}
	return val, nil
}

// dynamicFormatSize validates a dynamic * width or precision argument.
func dynamicFormatSize(v lua.LValue) (int64, error) {
	n, ok := v.(lua.LNumber)
	if !ok {
		return 0, nil
	}
	f := math.Abs(float64(n))
	if math.IsNaN(f) || math.IsInf(f, 0) || f > float64(maxStringOutputBytes) {
		return 0, fmt.Errorf("string.format output exceeds %d bytes", maxStringOutputBytes)
	}
	return int64(f), nil
}

// estimateFormatArg returns a conservative byte estimate for formatting v.
func estimateFormatArg(v lua.LValue, verb byte, prec int64, hasPrec bool, remaining int64) int64 {
	switch val := v.(type) {
	case lua.LString:
		s := string(val)
		n := int64(len(s))
		switch verb {
		case 'q':
			if hasPrec && prec < n {
				n = prec
			}
			est := int64(2)
			for i := int64(0); i < n; i++ {
				b := s[i]
				if b < 0x20 || b >= 0x7f || b == '"' || b == '\\' {
					est += 4
				} else {
					est++
				}
				if est > remaining {
					return est
				}
			}
			return est
		case 'x', 'X':
			if hasPrec && prec < n {
				n = prec
			}
			return n * 2
		default:
			if hasPrec && verb == 's' && prec < n {
				return prec
			}
			return n
		}
	case lua.LNumber:
		switch verb {
		case 'f', 'F', 'e', 'E', 'g', 'G':
			p := prec
			if !hasPrec {
				p = 6
			}
			return 320 + p
		default:
			return 64 + prec
		}
	default:
		return 64
	}
}

// checkPatternLimits enforces subject and pattern length caps before pattern matching.
func checkPatternLimits(L *lua.LState, fnName string) {
	subject := L.CheckString(1)
	if len(subject) > maxPatternInputBytes {
		L.RaiseError("string.%s input exceeds %d bytes", fnName, maxPatternInputBytes)
	}
	pattern := L.CheckString(2)
	if len(pattern) > maxPatternBytes {
		L.RaiseError("string.%s pattern exceeds %d bytes", fnName, maxPatternBytes)
	}
}

// bindCoroutineContext makes coroutine.resume and coroutine.wrap run each
// coroutine under the context of the call that resumes it.
func bindCoroutineContext(L *lua.LState) {
	co, ok := L.GetGlobal("coroutine").(*lua.LTable)
	if !ok {
		return
	}
	resume, ok := co.RawGetString("resume").(*lua.LFunction)
	if !ok || !resume.IsG {
		return
	}
	wrap, ok := co.RawGetString("wrap").(*lua.LFunction)
	if !ok || !wrap.IsG {
		return
	}
	resumeIn := func(L, th *lua.LState) int {
		if ctx := L.Context(); ctx != nil {
			th.SetContext(ctx)
		} else {
			th.RemoveContext()
		}
		return resume.GFunction(L)
	}
	co.RawSetString("resume", L.NewFunction(func(L *lua.LState) int {
		return resumeIn(L, L.CheckThread(1))
	}))
	// The wrap of gopher-lua returns a closure over the new coroutine, which
	// resumes it with the thread as the first argument.
	co.RawSetString("wrap", L.NewFunction(func(L *lua.LState) int {
		wrap.GFunction(L)
		fn, ok := L.Get(-1).(*lua.LFunction)
		if !ok || len(fn.Upvalues) != 1 {
			return 1
		}
		th, ok := fn.Upvalues[0].Value().(*lua.LState)
		if !ok {
			return 1
		}
		L.Pop(1)
		L.Push(L.NewFunction(func(L *lua.LState) int {
			L.Insert(th, 1)
			return resumeIn(L, th)
		}))
		return 1
	}))
}

// luaUTF8Char converts one or more integer codepoints to a UTF-8 string.
func luaUTF8Char(L *lua.LState) int {
	n := L.GetTop()
	buf := make([]rune, n)
	for i := 1; i <= n; i++ {
		buf[i-1] = rune(L.CheckInt(i))
	}
	L.Push(lua.LString(string(buf)))
	return 1
}
