package applemusic

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bjarneo/cliamp/applog"
)

const (
	scriptState = `tell application "Music"
	if player state is stopped then return "stopped"
	set t to current track
	return (player state as text) & tab & (name of t) & tab & (player position as text) & tab & (duration of t as text)
end tell`
	scriptLibrary = `tell application "Music"
	set l to library playlist 1
	set trackIDs to persistent ID of every track of l
	set trackNames to name of every track of l
	set trackArtists to artist of every track of l
	set trackAlbums to album of every track of l
	set trackSeconds to duration of every track of l
end tell
set out to {}
repeat with i from 1 to count of trackIDs
	set end of out to (item i of trackIDs) & tab & (item i of trackNames) & tab & (item i of trackArtists) & tab & (item i of trackAlbums) & tab & ((item i of trackSeconds) as text)
end repeat
set AppleScript's text item delimiters to linefeed
return out as text`
	scriptFront = `tell application "System Events" to get name of first process whose frontmost is true`
	scriptFocus = `on run argv
	tell application "System Events" to set frontmost of process (item 1 of argv) to true
end run`
	scriptVisible = `on run argv
	tell application "System Events" to set visible of process "Music" to ((item 1 of argv) is "true")
end run`
	scriptHasTrack  = `tell application "Music" to get exists current track`
	scriptSetVolume = `on run argv
	tell application "Music" to set sound volume to ((item 1 of argv) as integer)
end run`
	scriptSeek = `on run argv
	tell application "Music" to set player position to ((item 1 of argv) as real)
end run`
)

const (
	queueAttempts = 3
	queueSettle   = 800 * time.Millisecond
	seedSettle    = 400 * time.Millisecond
	focusHold     = 300 * time.Millisecond
	scriptTimeout = 30 * time.Second
)

// engine owns the tap and serializes control of Music.app.
type engine struct {
	dir string

	tapMu   sync.Mutex
	conn    net.Conn
	rate    int
	sink    atomic.Pointer[streamer]
	sockDir string

	mu       sync.Mutex // held while a command sequence drives Music
	owner    *streamer
	volume   int
	hasSaved bool
}

func newEngine(dir string) *engine { return &engine{dir: dir} }

func osascript(ctx context.Context, script string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, scriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", append([]string{"-e", script}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func music(ctx context.Context, verb string) error {
	_, err := osascript(ctx, `tell application "Music" to `+verb)
	return err
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
}

func musicPID(ctx context.Context) (string, error) {
	find := func() string {
		out, err := exec.CommandContext(ctx, "pgrep", "-x", "Music").Output()
		if err != nil {
			return ""
		}
		pid, _, _ := strings.Cut(string(out), "\n")
		return pid
	}
	if pid := find(); pid != "" {
		return pid, nil
	}
	if err := exec.CommandContext(ctx, "open", "-g", "-a", "Music").Run(); err != nil {
		return "", fmt.Errorf("apple music: launch Music: %w", err)
	}
	for range 50 {
		if pid := find(); pid != "" {
			return pid, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", errors.New("apple music: Music did not start")
}

// ensureTap starts the tap once and returns its sample rate. The first run can
// wait on the macOS system audio recording prompt.
func (e *engine) ensureTap() (int, error) {
	e.tapMu.Lock()
	defer e.tapMu.Unlock()
	if e.conn != nil {
		return e.rate, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pid, err := musicPID(ctx)
	if err != nil {
		return 0, err
	}
	dir, err := os.MkdirTemp("", "cliamp-am")
	if err != nil {
		return 0, fmt.Errorf("apple music: socket directory: %w", err)
	}
	sock := filepath.Join(dir, "tap.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		os.RemoveAll(dir)
		return 0, fmt.Errorf("apple music: listen: %w", err)
	}
	defer listener.Close()

	app := filepath.Join(e.dir, "CliampTap.app")
	if err := exec.CommandContext(ctx, "open", "-n", "-g", app, "--args", sock, pid).Run(); err != nil {
		os.RemoveAll(dir)
		return 0, fmt.Errorf("apple music: start tap: %w", err)
	}
	if ul, ok := listener.(*net.UnixListener); ok {
		_ = ul.SetDeadline(time.Now().Add(30 * time.Second))
	}
	conn, err := listener.Accept()
	if err != nil {
		os.RemoveAll(dir)
		return 0, fmt.Errorf("apple music: tap did not connect: %w", err)
	}
	var header [8]byte
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		conn.Close()
		os.RemoveAll(dir)
		return 0, fmt.Errorf("apple music: tap header: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	e.conn, e.sockDir = conn, dir
	e.rate = int(math.Float64frombits(binary.LittleEndian.Uint64(header[:])))
	go e.readTap(conn)
	return e.rate, nil
}

// readTap moves samples from the tap to the active streamer. Samples with no
// streamer are dropped.
func (e *engine) readTap(conn net.Conn) {
	buf := make([]byte, 32*1024)
	frames := make([]float32, len(buf)/4)
	pending := 0
	for {
		n, err := conn.Read(buf[pending:])
		if n > 0 {
			total := pending + n
			whole := total - total%4
			count := whole / 4
			for i := range count {
				frames[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
			}
			if s := e.sink.Load(); s != nil {
				s.push(frames[:count-count%2])
			}
			pending = copy(buf, buf[whole:total])
		}
		if err != nil {
			e.tapMu.Lock()
			if e.conn == conn {
				e.conn = nil
			}
			e.tapMu.Unlock()
			if s := e.sink.Load(); s != nil {
				s.fail(errors.New("apple music: tap stopped"))
			}
			return
		}
	}
}

func (e *engine) ctl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(e.dir, "cliamp-am-ctl"), args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// takeOver makes s the active streamer and sets Music to unity gain, so every
// level change happens in cliamp. Caller holds e.mu.
func (e *engine) takeOver(ctx context.Context, s *streamer) error {
	e.owner = s
	e.sink.Store(s)
	if !e.hasSaved {
		out, err := osascript(ctx, `tell application "Music" to get sound volume`)
		if err != nil {
			return fmt.Errorf("apple music: read volume: %w", err)
		}
		v, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			return fmt.Errorf("apple music: read volume: %w", err)
		}
		e.volume, e.hasSaved = v, true
	}
	_, err := osascript(ctx, scriptSetVolume, "100")
	return err
}

// playCatalog starts a song through Music's interface, the one route that
// reaches songs outside the library and leaves the library unchanged. Links only
// navigate while Music is active, so Music appears for about half a second. It
// hides as soon as focus is back, and queueing works on the hidden window. arm
// runs just before the song starts, so nothing played to set up the queue
// reaches the speakers. Caller holds e.mu.
func (e *engine) playCatalog(ctx context.Context, link, title string, arm func()) error {
	pid, err := musicPID(ctx)
	if err != nil {
		return err
	}
	front, err := osascript(ctx, scriptFront)
	if err != nil {
		return fmt.Errorf("apple music: Accessibility access is needed: %w", err)
	}
	if front == "Music" {
		front = ""
	}
	hasTrack, err := osascript(ctx, scriptHasTrack)
	if err != nil {
		return fmt.Errorf("apple music: read queue: %w", err)
	}
	seeded := strings.TrimSpace(hasTrack) == "true"

	var queued error
	for range queueAttempts {
		if err := e.openPage(ctx, link, front); err != nil {
			return err
		}
		// An empty queue ignores Play Next. The album's own Play seeds it.
		if !seeded {
			if _, err := e.ctl(ctx, "albumplay", pid); err != nil {
				queued = err
				continue
			}
			if err := wait(ctx, seedSettle); err != nil {
				return err
			}
			if err := music(ctx, "pause"); err != nil {
				return fmt.Errorf("apple music: pause: %w", err)
			}
			seeded = true
		}
		if _, queued = e.ctl(ctx, "playnext", pid, title); queued == nil {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if queued != nil {
		return fmt.Errorf("apple music: queue song: %w", queued)
	}
	// Music applies Play Next a moment after the press. Skipping sooner plays
	// the old next song.
	if err := wait(ctx, queueSettle); err != nil {
		return err
	}
	arm()
	if err := music(ctx, "next track"); err != nil {
		return fmt.Errorf("apple music: skip to song: %w", err)
	}
	if err := music(ctx, "play"); err != nil {
		return fmt.Errorf("apple music: play: %w", err)
	}
	return nil
}

func (e *engine) openPage(ctx context.Context, link, front string) error {
	if front == "" {
		return exec.CommandContext(ctx, "open", link).Run()
	}
	hide := func() {
		if _, err := osascript(ctx, scriptVisible, "false"); err != nil {
			applog.Warn("apple music: hide Music: %v", err)
		}
	}
	hide()
	if err := exec.CommandContext(ctx, "open", link).Run(); err != nil {
		return fmt.Errorf("apple music: open song: %w", err)
	}
	// Music drops the link if it loses focus before it navigates.
	if err := wait(ctx, focusHold); err != nil {
		return err
	}
	if _, err := osascript(ctx, scriptFocus, front); err != nil {
		applog.Warn("apple music: restore focus: %v", err)
	}
	hide()
	return nil
}

func wait(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// state is one reading of Music's transport.
type state struct {
	status        string
	name          string
	position, dur float64
}

func readState(ctx context.Context) (state, error) {
	out, err := osascript(ctx, scriptState)
	if err != nil {
		return state{}, err
	}
	f := strings.Split(out, "\t")
	if len(f) < 4 {
		return state{status: strings.TrimSpace(out)}, nil
	}
	st := state{status: f[0], name: f[1]}
	st.position, _ = parseFloat(f[2])
	st.dur, _ = parseFloat(f[3])
	return st, nil
}

// release pauses Music and restores its volume if s still owns it.
func (e *engine) release(s *streamer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.owner != s {
		return
	}
	e.releaseLocked()
}

func (e *engine) releaseLocked() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.sink.Store(nil)
	e.owner = nil
	if err := music(ctx, "pause"); err != nil {
		applog.Warn("apple music: pause: %v", err)
	}
	if e.hasSaved {
		if _, err := osascript(ctx, scriptSetVolume, strconv.Itoa(e.volume)); err != nil {
			applog.Warn("apple music: restore volume: %v", err)
		}
		e.hasSaved = false
	}
}

func (e *engine) shutdown() {
	e.mu.Lock()
	if e.owner != nil {
		e.releaseLocked()
	}
	e.mu.Unlock()

	e.tapMu.Lock()
	defer e.tapMu.Unlock()
	if e.conn != nil {
		e.conn.Close()
		e.conn = nil
	}
	if e.sockDir != "" {
		os.RemoveAll(e.sockDir)
		e.sockDir = ""
	}
}
