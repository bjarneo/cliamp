package luaplugin

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// blockedYTDLPFlags lists yt-dlp flags that run commands, load configs/plugins,
// or invoke caller-selected executables.
var blockedYTDLPFlags = []string{
	"--exec",
	"--exec-before-download",
	"--config-location",
	"--config-locations",
	"--plugin-dirs",
	"--alias",
	"--netrc-cmd",
	"--js-runtimes",
	"--ffmpeg-location",
	"--downloader",
	"--external-downloader",
}

// allowedYTDLPPrefixExceptions lists exact safe yt-dlp options that happen to
// be a literal prefix of a blocked option (e.g. --netrc vs --netrc-cmd).
var allowedYTDLPPrefixExceptions = map[string]bool{
	"--netrc": true,
}

// blockedExecFlag returns the first disallowed flag for binary, or empty string.
func blockedExecFlag(binary string, argv []string) string {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(binary)), ".exe")
	if base != "yt-dlp" {
		return ""
	}
	for _, arg := range argv {
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "--") || len(arg) < 3 {
			continue
		}
		opt, _, _ := strings.Cut(arg, "=")
		if allowedYTDLPPrefixExceptions[opt] {
			continue
		}
		for _, flag := range blockedYTDLPFlags {
			if opt == flag {
				return flag
			}
		}
		// Python optparse accepts unambiguous prefixes of long options.
		for _, flag := range blockedYTDLPFlags {
			if strings.HasPrefix(flag, opt) {
				return flag
			}
		}
	}
	return ""
}

type ffmpegWriteFlagRule struct {
	allowStdio      bool
	passLogPrefix   bool
	allowEmptyToCWD bool
}

// ffmpegWriteFlags maps ffmpeg options that accept a file path or output
// target that must stay within write rules.
var ffmpegWriteFlags = map[string]ffmpegWriteFlagRule{
	"progress":               {allowStdio: true},
	"vstats_file":            {},
	"sdp_file":               {},
	"passlogfile":            {passLogPrefix: true},
	"dump_attachment":        {allowEmptyToCWD: true},
	"hls_segment_filename":   {},
	"hls_fmp4_init_filename": {},
	"segment_list":           {},
	"master_pl_name":         {},
	"init_seg_name":          {},
	"media_seg_name":         {},
	"manifest_name":          {},
}

// ffmpegBlockedFlags lists ffmpeg flags that load unchecked option values or
// filtergraphs from files (which could contain arbitrary output paths).
var ffmpegBlockedFlags = map[string]struct{}{
	"filter_complex_script": {},
	"filter_script":         {},
}

// ffmpegFilterFlags lists ffmpeg options whose value is a filtergraph string.
var ffmpegFilterFlags = map[string]struct{}{
	"af":             {},
	"vf":             {},
	"filter":         {},
	"filter_complex": {},
	"lavfi":          {},
}

// ffmpegCWDWriteFlags lists zero-argument ffmpeg flags that write log files
// into the current working directory.
var ffmpegCWDWriteFlags = map[string]struct{}{
	"report": {},
	"vstats": {},
}

// ffmpegValueFlags lists standard ffmpeg options that consume one value
// argument. Any flag not listed here does not consume the following argument,
// so unknown boolean flags cannot hide a positional output path.
var ffmpegValueFlags = map[string]struct{}{
	"abort_on":                   {},
	"ab":                         {},
	"absf":                       {},
	"ac":                         {},
	"acodec":                     {},
	"af":                         {},
	"aframes":                    {},
	"allowed_extensions":         {},
	"analyzeduration":            {},
	"application":                {},
	"aq":                         {},
	"ar":                         {},
	"aspect":                     {},
	"async":                      {},
	"atag":                       {},
	"avioflags":                  {},
	"avoid_negative_ts":          {},
	"b":                          {},
	"bf":                         {},
	"bsf":                        {},
	"buffer_size":                {},
	"bufsize":                    {},
	"ch_layout":                  {},
	"channel_layout":             {},
	"codec":                      {},
	"color_primaries":            {},
	"color_range":                {},
	"color_trc":                  {},
	"colorspace":                 {},
	"compression_level":          {},
	"content_type":               {},
	"cookies":                    {},
	"copytb":                     {},
	"cpu-used":                   {},
	"cpucount":                   {},
	"cpuflags":                   {},
	"crf":                        {},
	"cutoff":                     {},
	"c":                          {},
	"dcodec":                     {},
	"deadline":                   {},
	"dframes":                    {},
	"disposition":                {},
	"err_detect":                 {},
	"fflags":                     {},
	"fifo_size":                  {},
	"filter":                     {},
	"filter_complex":             {},
	"filter_complex_threads":     {},
	"filter_hw_device":           {},
	"filter_threads":             {},
	"flags":                      {},
	"flags2":                     {},
	"flush_packets":              {},
	"fps_mode":                   {},
	"frag_duration":              {},
	"frame_duration":             {},
	"frames":                     {},
	"fs":                         {},
	"g":                          {},
	"headers":                    {},
	"hls_flags":                  {},
	"hls_list_size":              {},
	"hls_segment_type":           {},
	"hls_time":                   {},
	"http_proxy":                 {},
	"hwaccel":                    {},
	"hwaccel_device":             {},
	"hwaccel_output_format":      {},
	"icy":                        {},
	"id3v2_version":              {},
	"init_hw_device":             {},
	"itsoffset":                  {},
	"itsscale":                   {},
	"keyint_min":                 {},
	"lavfi":                      {},
	"level":                      {},
	"loglevel":                   {},
	"map":                        {},
	"map_channel":                {},
	"map_chapters":               {},
	"map_metadata":               {},
	"max_alloc":                  {},
	"max_error_rate":             {},
	"max_interleave_delta":       {},
	"max_muxing_queue_size":      {},
	"maxrate":                    {},
	"metadata":                   {},
	"method":                     {},
	"min_frag_duration":          {},
	"minrate":                    {},
	"movflags":                   {},
	"multiple_requests":          {},
	"muxdelay":                   {},
	"muxpreload":                 {},
	"overrun_nonfatal":           {},
	"pass":                       {},
	"pix_fmt":                    {},
	"pkt_size":                   {},
	"post_data":                  {},
	"preset":                     {},
	"probesize":                  {},
	"profile":                    {},
	"program":                    {},
	"protocol_whitelist":         {},
	"q":                          {},
	"qp":                         {},
	"qscale":                     {},
	"quality":                    {},
	"r":                          {},
	"readrate":                   {},
	"readrate_initial_burst":     {},
	"reconnect":                  {},
	"reconnect_at_eof":           {},
	"reconnect_delay_max":        {},
	"reconnect_on_http_error":    {},
	"reconnect_on_network_error": {},
	"reconnect_streamed":         {},
	"referer":                    {},
	"refs":                       {},
	"reinit_filter":              {},
	"reset_timestamps":           {},
	"rtsp_flags":                 {},
	"rtsp_transport":             {},
	"rw_timeout":                 {},
	"s":                          {},
	"sample_fmt":                 {},
	"sbsf":                       {},
	"scodec":                     {},
	"seekable":                   {},
	"seg_duration":               {},
	"segment_format":             {},
	"segment_list_size":          {},
	"segment_time":               {},
	"sframes":                    {},
	"shortest_buf_duration":      {},
	"speed":                      {},
	"ss":                         {},
	"sseof":                      {},
	"stag":                       {},
	"start_number":               {},
	"stats_period":               {},
	"stimeout":                   {},
	"stream_loop":                {},
	"streamid":                   {},
	"strict":                     {},
	"svtav1-params":              {},
	"sws_flags":                  {},
	"t":                          {},
	"tag":                        {},
	"target":                     {},
	"threads":                    {},
	"timecode":                   {},
	"timelimit":                  {},
	"timeout":                    {},
	"timestamp":                  {},
	"tls_verify":                 {},
	"to":                         {},
	"tune":                       {},
	"user_agent":                 {},
	"v":                          {},
	"vb":                         {},
	"vbr":                        {},
	"vbsf":                       {},
	"vcodec":                     {},
	"vf":                         {},
	"vframes":                    {},
	"vol":                        {},
	"vq":                         {},
	"vsync":                      {},
	"vtag":                       {},
	"window_size":                {},
	"write_id3v1":                {},
	"x264-params":                {},
	"x264opts":                   {},
	"x265-params":                {},
}

// validateFFmpegArgs rejects ffmpeg arguments that write outside allowed paths.
// It returns an error message, or an empty string when the arguments pass.
func validateFFmpegArgs(binary string, argv []string, cwd string) string {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(binary)), ".exe")
	if base != "ffmpeg" {
		return ""
	}
	return validateFFmpegArgsWith(argv, cwd, isWriteAllowed)
}

func validateFFmpegArgsWith(argv []string, cwd string, writeAllowed func(string) bool) string {
	positionalOnly := false
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if !positionalOnly && arg == "--" {
			positionalOnly = true
			continue
		}
		if !positionalOnly && strings.HasPrefix(arg, "-") && arg != "-" {
			flagPart, eqVal, hasEq := strings.Cut(arg, "=")
			rawName := strings.TrimPrefix(flagPart, "--")
			if rawName == flagPart {
				rawName = strings.TrimPrefix(flagPart, "-")
			}
			consumeValue := func() (string, bool) {
				if hasEq {
					return eqVal, true
				}
				if i+1 >= len(argv) {
					return "", false
				}
				i++
				return argv[i], true
			}
			// ffmpeg loads an option value from a file when the flag starts with "/".
			// Reject unconditionally because the file's contents can name any path.
			if strings.HasPrefix(rawName, "/") {
				return "flag not allowed for ffmpeg: " + flagPart
			}
			baseFlag, _, _ := strings.Cut(rawName, ":")
			baseFlag = strings.ToLower(baseFlag)

			if _, blocked := ffmpegBlockedFlags[baseFlag]; blocked {
				return "flag not allowed for ffmpeg: " + flagPart
			}
			if _, isFilter := ffmpegFilterFlags[baseFlag]; isFilter {
				val, ok := consumeValue()
				if !ok {
					return "missing value for ffmpeg flag: " + flagPart
				}
				if ffmpegFilterHasFileOption(val) {
					return "file option not allowed in ffmpeg filter flag: " + flagPart
				}
				continue
			}

			if baseFlag == "i" {
				consumeValue()
				continue
			}
			if rule, ok := ffmpegWriteFlags[baseFlag]; ok {
				val, hasVal := consumeValue()
				if !hasVal {
					return "missing path for ffmpeg flag: " + flagPart
				}
				if val == "" && rule.allowEmptyToCWD {
					if !isFFmpegCWDAllowed(cwd, writeAllowed) {
						return "output path not allowed for ffmpeg flag " + flagPart
					}
					continue
				}
				if !isFFmpegTargetAllowed(val, cwd, rule.allowStdio, writeAllowed) {
					return "output path not allowed for ffmpeg flag " + flagPart + ": " + val
				}
				if rule.passLogPrefix && !isFFmpegTargetAllowed(val+"-0.log", cwd, false, writeAllowed) {
					return "output path not allowed for ffmpeg flag " + flagPart + ": " + val
				}
				continue
			}
			if _, ok := ffmpegCWDWriteFlags[baseFlag]; ok {
				if !isFFmpegCWDAllowed(cwd, writeAllowed) {
					return "flag not allowed for ffmpeg without writable cwd: " + flagPart
				}
				continue
			}
			if baseFlag == "f" || baseFlag == "format" {
				val, ok := consumeValue()
				if ok && strings.EqualFold(strings.TrimSpace(val), "tee") {
					return "output format not allowed for ffmpeg: tee"
				}
				continue
			}
			if _, ok := ffmpegValueFlags[baseFlag]; ok {
				consumeValue()
			}
			continue
		}

		if !isFFmpegTargetAllowed(arg, cwd, true, writeAllowed) {
			return "output path not allowed for ffmpeg: " + arg
		}
	}
	return ""
}

func isFFmpegCWDAllowed(cwd string, writeAllowed func(string) bool) bool {
	if cwd != "" {
		return writeAllowed(cwd)
	}
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	return writeAllowed(wd)
}

func isFFmpegTargetAllowed(target, cwd string, allowStdio bool, writeAllowed func(string) bool) bool {
	if target == "" {
		return false
	}
	if allowStdio && target == "-" {
		return true
	}
	if target == "/dev/null" || strings.EqualFold(target, "nul") {
		return true
	}
	if scheme, rest, ok := splitFFmpegScheme(target); ok {
		switch scheme {
		case "pipe":
			return allowStdio && (rest == "" || rest == "1" || rest == "2")
		case "file":
			path, valid := stripFileSchemePath(rest)
			if !valid {
				return false
			}
			return isFFmpegLocalPathAllowed(path, cwd, writeAllowed)
		default:
			return false
		}
	}
	return isFFmpegLocalPathAllowed(target, cwd, writeAllowed)
}

// splitFFmpegScheme extracts a protocol prefix before the first colon. It
// treats a single drive letter before a colon as a Windows path.
func splitFFmpegScheme(s string) (string, string, bool) {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return "", "", false
	}
	prefix := s[:colon]
	if strings.ContainsAny(prefix, `/\`) {
		return "", "", false
	}
	if len(prefix) == 1 && isASCIIAlpha(prefix[0]) {
		return "", "", false
	}
	// ffmpeg accepts protocol options separated by commas before the colon.
	scheme, _, _ := strings.Cut(prefix, ",")
	if len(scheme) < 2 {
		return "", "", false
	}
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		valid := isASCIIAlpha(c) || (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'
		if !valid {
			return "", "", false
		}
	}
	return strings.ToLower(scheme), s[colon+1:], true
}

func isASCIIAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func stripFileSchemePath(rest string) (string, bool) {
	if rest == "" {
		return "", false
	}
	path := rest
	if strings.HasPrefix(path, "//") {
		switch {
		case strings.HasPrefix(path, "///"):
			path = path[2:]
		case strings.HasPrefix(strings.ToLower(path), "//localhost/"):
			path = path[len("//localhost"):]
		default:
			return "", false
		}
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && isASCIIAlpha(path[1]) && path[2] == ':' {
		path = path[1:]
	}
	if _, _, nested := splitFFmpegScheme(path); nested {
		return "", false
	}
	return path, true
}

// ffmpegFilterHasFileOption reports whether a filtergraph expression uses a
// file-writing filter or sets a filter option that writes files.
func ffmpegFilterHasFileOption(graph string) bool {
	tokens := strings.FieldsFunc(graph, func(r rune) bool {
		return r == ':' || r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	isFileKey := func(k string) bool {
		k = strings.ToLower(strings.Trim(strings.ReplaceAll(k, "\\", ""), "'\""))
		switch k {
		case "file", "filename", "stats_file", "log_path", "dumpfile", "output_file", "out_file", "result":
			return true
		}
		return strings.HasSuffix(k, "_file") || strings.HasSuffix(k, "_filename")
	}
	isFileWriterFilter := func(name string) bool {
		// Strip leading pad labels such as "[0:a]ametadata".
		if i := strings.LastIndexByte(name, ']'); i >= 0 {
			name = name[i+1:]
		}
		name = strings.ToLower(strings.Trim(strings.ReplaceAll(name, "\\", ""), "'\""))
		switch name {
		case "ametadata", "metadata", "ssim", "psnr", "libvmaf", "vidstabdetect", "signature":
			return true
		}
		return false
	}
	for _, tok := range tokens {
		lhs, rhs, hasEq := strings.Cut(tok, "=")
		if isFileWriterFilter(lhs) {
			return true
		}
		if !hasEq {
			continue
		}
		if isFileKey(lhs) {
			return true
		}
		if subKey, _, hasSubEq := strings.Cut(rhs, "="); hasSubEq && isFileKey(subKey) {
			return true
		}
	}
	return false
}

func isFFmpegLocalPathAllowed(path, cwd string, writeAllowed func(string) bool) bool {
	if path == "" {
		return false
	}
	if path == "/dev/null" || strings.EqualFold(path, "nul") {
		return true
	}
	// Preserve ".." segments without filepath.Join lexical cleaning so
	// canonicalExistingPath resolves intermediate symlinks first.
	sep := string(os.PathSeparator)
	if strings.HasPrefix(path, "~") {
		if path == "~" {
			path = homeEnv()
		} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
			path = homeEnv() + sep + path[2:]
		} else {
			return false
		}
	} else if !filepath.IsAbs(path) && cwd != "" {
		path = cwd + sep + path
	}
	return writeAllowed(path)
}

// Default binaries plugins may invoke via cliamp.exec.run(). Users can widen
// this via [plugins] allowed_binaries = "yt-dlp,ffmpeg,ffprobe" in config.toml.
var defaultAllowedBinaries = []string{"yt-dlp", "ffmpeg"}

// Per-process output cap (stdout+stderr). Once exceeded, further output is
// dropped and the process is still allowed to run to completion. Prevents a
// chatty subprocess from OOMing the player.
const execMaxOutputBytes = 4 << 20 // 4 MiB

// Per-plugin concurrency cap. Runaway plugins can't fork-bomb past this.
const execMaxPerPlugin = 4

// Hard timeout cap. Plugins may pass a smaller value; larger values clamp.
const execMaxTimeout = 30 * time.Minute

// execPipeGrace is the time that the output pipes stay open after a cancel or
// a timeout. A process that left the process group of the binary can hold
// them open. Then the exec manager closes them, so the readers end and
// stopAll does not wait for that process.
const execPipeGrace = 500 * time.Millisecond

// execEntry tracks a single running subprocess.
type execEntry struct {
	id     int64
	plugin *Plugin
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}
}

// execManager owns all running plugin subprocesses.
type execManager struct {
	mu      sync.Mutex
	entries map[int64]*execEntry
	nextID  atomic.Int64
	perPlug map[*Plugin]int
	allowed map[string]struct{}
	allowMu sync.RWMutex
}

func newExecManager(allowed []string) *execManager {
	em := &execManager{
		entries: make(map[int64]*execEntry),
		perPlug: make(map[*Plugin]int),
		allowed: make(map[string]struct{}),
	}
	em.setAllowed(allowed)
	return em
}

func (em *execManager) setAllowed(names []string) {
	em.allowMu.Lock()
	defer em.allowMu.Unlock()
	em.allowed = make(map[string]struct{}, len(names))
	for _, n := range names {
		em.allowed[n] = struct{}{}
	}
}

func (em *execManager) isAllowed(name string) bool {
	em.allowMu.RLock()
	defer em.allowMu.RUnlock()
	_, ok := em.allowed[name]
	return ok
}

func (em *execManager) add(e *execEntry) {
	em.mu.Lock()
	em.entries[e.id] = e
	em.perPlug[e.plugin]++
	em.mu.Unlock()
}

func (em *execManager) remove(e *execEntry) {
	em.mu.Lock()
	if _, ok := em.entries[e.id]; ok {
		delete(em.entries, e.id)
		em.perPlug[e.plugin]--
		if em.perPlug[e.plugin] <= 0 {
			delete(em.perPlug, e.plugin)
		}
	}
	em.mu.Unlock()
}

// canStart returns true if the plugin is under its concurrency cap.
func (em *execManager) canStart(p *Plugin) bool {
	em.mu.Lock()
	defer em.mu.Unlock()
	return em.perPlug[p] < execMaxPerPlugin
}

// stopPlugin cancels every process owned by the given plugin and blocks
// until each one has exited. Called from Manager.cleanupPlugin.
func (em *execManager) stopPlugin(p *Plugin) {
	em.mu.Lock()
	var victims []*execEntry
	for _, e := range em.entries {
		if e.plugin == p {
			victims = append(victims, e)
		}
	}
	em.mu.Unlock()

	for _, e := range victims {
		e.cancel()
	}
	for _, e := range victims {
		<-e.done
	}
}

// stopAll cancels every process. Called from Manager.Close.
func (em *execManager) stopAll() {
	em.mu.Lock()
	var all []*execEntry
	for _, e := range em.entries {
		all = append(all, e)
	}
	em.mu.Unlock()

	for _, e := range all {
		e.cancel()
	}
	for _, e := range all {
		<-e.done
	}
}

// registerExecAPI adds cliamp.exec.run(binary, args, opts?) -> handle, err.
// The exec API is only functional for plugins declaring permissions = {"exec"}.
// Without the permission, cliamp.exec is a no-op table that logs once.
// The output and exit callbacks go through m.call.
func (m *Manager) registerExecAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin) {
	em := m.execs
	tbl := L.NewTable()

	L.SetField(tbl, "run", L.NewFunction(func(L *lua.LState) int {
		if !p.permitted(PermExec, "cliamp.exec.run") {
			return pushErr(L, "exec permission required")
		}

		binary := L.CheckString(1)
		argsTbl := L.CheckTable(2)
		optsTbl := L.OptTable(3, nil)

		if !em.isAllowed(binary) {
			return pushErr(L, "binary not in allowlist: "+binary)
		}

		// Read argv sequentially from 1..Len so order is deterministic and
		// every element must be a string.
		n := argsTbl.Len()
		argv := make([]string, 0, n)
		for i := 1; i <= n; i++ {
			v := argsTbl.RawGetInt(i)
			if v.Type() != lua.LTString {
				return pushErr(L, "args must all be strings")
			}
			argv = append(argv, string(v.(lua.LString)))
		}

		if flag := blockedExecFlag(binary, argv); flag != "" {
			return pushErr(L, "flag not allowed for "+binary+": "+flag)
		}

		var onStdout, onStderr, onExit *lua.LFunction
		cwd := ""
		timeout := execMaxTimeout

		if optsTbl != nil {
			if fn, ok := optsTbl.RawGetString("on_stdout").(*lua.LFunction); ok {
				onStdout = fn
			}
			if fn, ok := optsTbl.RawGetString("on_stderr").(*lua.LFunction); ok {
				onStderr = fn
			}
			if fn, ok := optsTbl.RawGetString("on_exit").(*lua.LFunction); ok {
				onExit = fn
			}
			if s, ok := optsTbl.RawGetString("cwd").(lua.LString); ok {
				cwd = string(s)
			}
			if n, ok := optsTbl.RawGetString("timeout").(lua.LNumber); ok {
				secs := float64(n)
				if !math.IsNaN(secs) && secs > 0 {
					if secs < timeout.Seconds() {
						timeout = time.Duration(secs * float64(time.Second))
					}
				}
			}
		}

		if cwd != "" {
			resolvedCwd, ok := resolveAllowedWritePath(cwd)
			if !ok {
				return pushErr(L, "cwd not in write allowlist")
			}
			cwd = resolvedCwd
		}

		if reason := validateFFmpegArgs(binary, argv, cwd); reason != "" {
			return pushErr(L, reason)
		}

		path, err := exec.LookPath(binary)
		if err != nil {
			return pushErr(L, "binary not found on PATH: "+binary)
		}

		if !em.canStart(p) {
			return pushErr(L, "per-plugin exec concurrency cap reached")
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, path, argv...)
		if cwd != "" {
			cmd.Dir = cwd
		}
		// A minimal env gives each binary the same known environment, so
		// variables of the user's shell, such as LD_PRELOAD, do not change
		// how it runs. It does not hide secrets from the plugin, which can
		// read any variable with os.getenv and pass it in argv. yt-dlp and
		// ffmpeg both run fine with a minimal env.
		cmd.Env = minimalExecEnv()
		killProcessGroup(cmd)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			return pushErr(L, err.Error())
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			cancel()
			return pushErr(L, err.Error())
		}
		if err := cmd.Start(); err != nil {
			cancel()
			return pushErr(L, err.Error())
		}

		id := em.nextID.Add(1)
		entry := &execEntry{
			id:     id,
			plugin: p,
			cmd:    cmd,
			cancel: cancel,
			done:   make(chan struct{}),
		}
		em.add(entry)

		// Shared output budget across stdout+stderr.
		var outUsed atomic.Int64

		pipeStream := func(r io.Reader, fn *lua.LFunction, label string) {
			// Drain what the scan leaves: the rest after the output budget
			// ends, or after a line longer than the buffer stops the scan.
			// A pipe that is not drained blocks the process until its
			// timeout.
			defer io.Copy(io.Discard, r)
			scanner := bufio.NewScanner(r)
			// Allow longer lines than default 64KiB for noisy tools like ffmpeg.
			scanner.Buffer(make([]byte, 64*1024), 1<<20)
			for scanner.Scan() {
				if ctx.Err() != nil {
					return
				}
				line := scanner.Text()
				if outUsed.Add(int64(len(line)+1)) > execMaxOutputBytes {
					return // The budget is used up. Drop the rest silently.
				}
				if fn == nil {
					continue
				}
				m.call(p, label, hookTimeout, 0, fixedArgs(fn, lua.LString(line)))
			}
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); pipeStream(stdout, onStdout, "exec on_stdout") }()
		go func() { defer wg.Done(); pipeStream(stderr, onStderr, "exec on_stderr") }()

		readersDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
			case <-readersDone:
				return
			}
			select {
			case <-time.After(execPipeGrace):
				stdout.Close()
				stderr.Close()
			case <-readersDone:
			}
		}()

		go func() {
			wg.Wait()
			close(readersDone)
			waitErr := cmd.Wait()
			ctxErr := ctx.Err()
			cancel()
			em.remove(entry)

			code := 0
			if waitErr != nil {
				if ctxErr != nil {
					code = -1 // cancelled or timed out
				} else {
					var exitErr *exec.ExitError
					if errors.As(waitErr, &exitErr) {
						code = exitErr.ExitCode()
					} else {
						code = -2 // other error
					}
				}
			}

			if onExit != nil {
				m.call(p, "exec on_exit", hookTimeout, 0, fixedArgs(onExit, lua.LNumber(code)))
			}
			close(entry.done)
		}()

		// Build a Lua handle with :cancel() and :alive().
		handle := L.NewTable()
		L.SetField(handle, "cancel", L.NewFunction(func(L *lua.LState) int {
			entry.cancel()
			return 0
		}))
		L.SetField(handle, "alive", L.NewFunction(func(L *lua.LState) int {
			select {
			case <-entry.done:
				L.Push(lua.LFalse)
			default:
				L.Push(lua.LTrue)
			}
			return 1
		}))
		L.SetField(handle, "id", lua.LNumber(id))

		L.Push(handle)
		return 1
	}))

	L.SetField(cliamp, "exec", tbl)
}

// homeEnv returns the user's home directory for subprocess HOME, preferring
// $HOME, then os.UserHomeDir(), falling back to os.TempDir() when unset.
// yt-dlp and ffmpeg both read HOME (~/.cache, ~/.config).
func homeEnv() string {
	if home, ok := os.LookupEnv("HOME"); ok && home != "" {
		return home
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.TempDir()
}
