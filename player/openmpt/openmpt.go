// Package openmpt is a minimal purego binding to libopenmpt, used to decode
// tracker module files (.mod, .s3m, .xm, .it, and other formats libopenmpt
// supports).
//
// It loads the system's libopenmpt shared library at runtime via purego —
// no CGo, no build-time SDK, no static linking. This keeps `go install` and
// cross-compiled release builds working exactly as they do today. If the
// library isn't installed, Available reports false and Open returns
// ErrUnavailable so callers can degrade gracefully, the same way this
// codebase already treats ffmpeg and yt-dlp as optional runtime
// dependencies.
//
// libopenmpt's C API promises long-term ABI/API stability, which is what
// makes a dynamically-loaded binding like this viable — the small set of
// functions bound here (module create/destroy/read/seek/duration) has been
// stable since libopenmpt 0.3.
package openmpt

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// ErrUnavailable indicates the libopenmpt shared library could not be
// loaded (not installed, or not found on the platform's library search
// path).
var ErrUnavailable = errors.New("libopenmpt: shared library not found")

// SupportedExtensions lists the file extensions (lowercase, no leading dot)
// that libopenmpt can load. This is a static snapshot of
// openmpt_get_supported_extensions() from libopenmpt 0.8 — it's kept as a
// plain list rather than queried live so callers (e.g. the file browser)
// can check an extension without first loading the shared library.
//
// `go generate ./player/openmpt/...` reprints the live list (requires a C
// compiler and libopenmpt's headers/lib installed) so it can be diffed
// against the one below and copied in by hand after a libopenmpt upgrade;
// it does not rewrite this file itself.
//
//go:generate sh -c "printf '#include <stdio.h>\\n#include <libopenmpt/libopenmpt.h>\\nint main(){puts(openmpt_get_supported_extensions());}' | cc -x c - -lopenmpt -o /tmp/openmpt-supported-exts && /tmp/openmpt-supported-exts"
var SupportedExtensions = []string{
	"mptm", "mod", "s3m", "xm", "it",
	"667", "669", "amf", "ams", "c67", "cba", "dbm", "digi", "dmf", "dsm",
	"dsym", "dtm", "etx", "far", "fc", "fc13", "fc14", "fmt", "fst", "ftm",
	"imf", "ims", "ice", "j2b", "m15", "mdl", "med", "mms", "mt2", "mtm",
	"mus", "nst", "okt", "plm", "psm", "pt36", "ptm", "puma", "rtm", "sfx",
	"sfx2", "smod", "st26", "stk", "stm", "stx", "stp", "symmod", "tcb",
	"gmc", "gtk", "gt2", "ult", "unic", "wow", "xmf", "gdm", "mo3", "oxm",
	"umx", "xpk", "ppm", "mmcmp",
}

var (
	initOnce sync.Once
	initErr  error

	fnCreateFromMemory2          func(filedata []byte, filesize uintptr, logfunc uintptr, loguser unsafe.Pointer, errfunc uintptr, erruser unsafe.Pointer, errOut *int32, errMsgOut *uintptr, ctls unsafe.Pointer) uintptr
	fnDestroy                    func(mod uintptr)
	fnReadInterleavedFloatStereo func(mod uintptr, samplerate int32, count uintptr, interleaved []float32) uintptr
	fnGetDurationSeconds         func(mod uintptr) float64
	fnGetPositionSeconds         func(mod uintptr) float64
	fnSetPositionSeconds         func(mod uintptr, seconds float64) float64
	fnFreeString                 func(str uintptr)

	// logFunc and errFunc point at libopenmpt's own no-op log/error
	// handlers (resolved by address, never called from Go). Passing NULL
	// for these instead falls back to libopenmpt's *default* handlers,
	// which write straight to stderr — fine for a CLI tool, but it would
	// garble cliamp's full-screen TUI the moment someone opens a
	// corrupt or unrecognized module file. Zero is still a safe fallback
	// if resolving either symbol fails.
	logFunc uintptr
	errFunc uintptr
)

// ensureInit loads libopenmpt and resolves the symbols we need, exactly
// once. Safe to call from multiple goroutines.
func ensureInit() error {
	initOnce.Do(func() {
		handle, err := loadLibrary()
		if err != nil {
			initErr = fmt.Errorf("%w: %v", ErrUnavailable, err)
			return
		}
		purego.RegisterLibFunc(&fnCreateFromMemory2, handle, "openmpt_module_create_from_memory2")
		purego.RegisterLibFunc(&fnDestroy, handle, "openmpt_module_destroy")
		purego.RegisterLibFunc(&fnReadInterleavedFloatStereo, handle, "openmpt_module_read_interleaved_float_stereo")
		purego.RegisterLibFunc(&fnGetDurationSeconds, handle, "openmpt_module_get_duration_seconds")
		purego.RegisterLibFunc(&fnGetPositionSeconds, handle, "openmpt_module_get_position_seconds")
		purego.RegisterLibFunc(&fnSetPositionSeconds, handle, "openmpt_module_set_position_seconds")
		purego.RegisterLibFunc(&fnFreeString, handle, "openmpt_free_string")

		if addr, err := dlsym(handle, "openmpt_log_func_silent"); err == nil {
			logFunc = addr
		}
		if addr, err := dlsym(handle, "openmpt_error_func_ignore"); err == nil {
			errFunc = addr
		}
	})
	return initErr
}

// Available reports whether libopenmpt was found and its symbols resolved.
// The underlying library load happens at most once no matter how many
// times Available or Open is called.
func Available() bool {
	return ensureInit() == nil
}

// Module is an open tracker module ready for rendering.
type Module struct {
	mu  sync.Mutex
	mod uintptr
}

// Open loads a module from its raw file bytes. Per libopenmpt's contract,
// data only needs to stay valid for the duration of this call.
func Open(data []byte) (*Module, error) {
	if err := ensureInit(); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("libopenmpt: empty file")
	}

	var errCode int32
	var errMsg uintptr
	mod := fnCreateFromMemory2(data, uintptr(len(data)), logFunc, nil, errFunc, nil, &errCode, &errMsg, nil)
	if mod == 0 {
		if msg := takeCString(errMsg); msg != "" {
			return nil, fmt.Errorf("libopenmpt: %s", msg)
		}
		return nil, fmt.Errorf("libopenmpt: unrecognized or corrupt module file (error code %d)", errCode)
	}
	return &Module{mod: mod}, nil
}

// takeCString copies a NUL-terminated string libopenmpt allocated and
// returned by pointer (as opposed to one purego already copied for us via
// a func's string return type - see RegisterFunc's docs on that
// conversion) into a Go string, then frees the original with
// openmpt_free_string as libopenmpt's API requires. Returns "" for a NULL
// pointer, or if fnFreeString itself failed to resolve (ptr == 0 is the
// only value that's safe to leak: it means there was nothing to free).
func takeCString(cstr uintptr) string {
	if cstr == 0 {
		return ""
	}
	defer func() {
		if fnFreeString != nil {
			fnFreeString(cstr)
		}
	}()

	// Taking the address of the uintptr and reinterpreting it as
	// *unsafe.Pointer, rather than converting cstr directly, is the same
	// trick purego's own internal/strings.GoString uses to keep `go vet`
	// from flagging this as a misuse of unsafe.Pointer.
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&cstr))
	var length int
	for *(*byte)(unsafe.Add(ptr, uintptr(length))) != 0 {
		length++
	}
	return string(unsafe.Slice((*byte)(ptr), length))
}

// ReadInterleavedFloatStereo renders up to len(buf)/2 stereo frames into buf
// (interleaved L,R,L,R,...) at the given sample rate. It returns the number
// of frames actually rendered; 0 means the module has finished playing.
func (m *Module) ReadInterleavedFloatStereo(sampleRate int, buf []float32) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mod == 0 || len(buf) < 2 {
		return 0
	}
	count := len(buf) / 2
	n := fnReadInterleavedFloatStereo(m.mod, int32(sampleRate), uintptr(count), buf[:count*2])
	return int(n)
}

// DurationSeconds returns the module's estimated playback duration.
func (m *Module) DurationSeconds() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mod == 0 {
		return 0
	}
	return fnGetDurationSeconds(m.mod)
}

// PositionSeconds returns the current playback position.
func (m *Module) PositionSeconds() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mod == 0 {
		return 0
	}
	return fnGetPositionSeconds(m.mod)
}

// SetPositionSeconds seeks to the given position and returns the position
// actually reached (libopenmpt clamps out-of-range requests).
func (m *Module) SetPositionSeconds(seconds float64) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mod == 0 {
		return 0
	}
	return fnSetPositionSeconds(m.mod, seconds)
}

// Close releases the module's native resources. Safe to call more than
// once.
func (m *Module) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mod != 0 {
		fnDestroy(m.mod)
		m.mod = 0
	}
}
