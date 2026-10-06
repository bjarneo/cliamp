package main

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/ui"
)

// socketDir returns a new config directory for a test that binds the
// socket. On macOS t.TempDir is deep enough that a long test name pushes the
// socket path past the 104-byte limit, so the directory goes in /tmp there.
func socketDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("/tmp", "c")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startTestIPC serves the default socket of a new config directory. It
// answers state.get with snapshot and hands each submitted job to finish.
// finish runs on its own goroutine, as the Model finishes a job later.
func startTestIPC(t *testing.T, snapshot ipc.RuntimeSnapshot, finish func(jobs *ipc.JobStore, id string, request ipc.V2Request)) {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", socketDir(t))
	srv, err := ipc.NewServer(ipc.DefaultSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.SetOperationRegistry(ipc.DefaultOperationRegistry())
	jobs := srv.JobStore()
	srv.SetV2Dispatcher(ipc.V2DispatcherFunc(func(ctx context.Context, request ipc.V2Request) (ipc.V2Result, *ipc.V2Error) {
		if request.Method == "state.get" {
			return ipc.V2Result{Snapshot: &snapshot}, nil
		}
		job, err := jobs.CreateWithContext(ctx, request.Operation)
		if err != nil {
			return ipc.V2Result{}, &ipc.V2Error{Code: ipc.V2ErrorCodeConflict, Message: ipc.V2MessageConflict}
		}
		go finish(jobs, job.ID, request)
		return ipc.V2Result{Job: &job}, nil
	}))
}

// The CLI client submits an operation and waits for the job. A job result
// with ok false, a failed job and a job that does not finish in time each
// give an error. The wait never outlasts its timeout.
func TestIPCSendWithin(t *testing.T) {
	visParams := make(chan ipc.Request, 1)
	startTestIPC(t, ipc.RuntimeSnapshot{}, func(jobs *ipc.JobStore, id string, request ipc.V2Request) {
		if _, err := jobs.Start(id); err != nil {
			t.Errorf("Start: %v", err)
			return
		}
		switch request.Operation {
		case "next":
			_ = jobs.Succeed(id, json.RawMessage(`{"ok":true,"state":"playing"}`))
		case "vis":
			var params ipc.Request
			_ = json.Unmarshal(request.Params, &params)
			visParams <- params
			_ = jobs.Succeed(id, json.RawMessage(`{"ok":true,"visualizer":"Wave"}`))
		case "eq":
			_ = jobs.Succeed(id, json.RawMessage(`{"ok":false,"error":"unknown EQ preset \"Rokc\""}`))
		case "device":
			_ = jobs.Fail(id, ipc.V2Error{Code: ipc.V2ErrorCodeNotFound, Message: ipc.V2MessageNotFound, Detail: "no device DAC"})
		case "load":
			// The job never finishes.
		}
	})

	for _, tt := range []struct {
		name      string
		operation string
		params    ipc.Request
		timeout   time.Duration
		want      ipc.Response
		wantErr   string
	}{
		{name: "success", operation: "next", timeout: ipcWait, want: ipc.Response{OK: true, State: "playing"}},
		{name: "params", operation: "vis", params: ipc.Request{Name: "wave"}, timeout: ipcWait, want: ipc.Response{OK: true, Visualizer: "Wave"}},
		{name: "result error", operation: "eq", timeout: ipcWait, wantErr: `unknown EQ preset "Rokc"`},
		{name: "failed job", operation: "device", timeout: ipcWait, wantErr: "job failed (not_found): resource not found (no device DAC)"},
		{name: "no result in time", operation: "load", timeout: 300 * time.Millisecond, wantErr: "load did not finish within 300ms"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			got, err := ipcSendWithin(tt.operation, tt.params, tt.timeout)
			if elapsed := time.Since(start); elapsed > tt.timeout+2*time.Second {
				t.Fatalf("ipcSendWithin took %s, want at most about %s", elapsed, tt.timeout)
			}
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("response = %+v, want %+v", got, tt.want)
			}
		})
	}
	if params := <-visParams; params.Name != "wave" {
		t.Errorf("vis params = %+v, want the name wave", params)
	}
}

// cliamp status reads the snapshot and prints it in the Response shape.
func TestIPCStateResult(t *testing.T) {
	shuffle, mono := true, false
	snapshot := ipc.RuntimeSnapshot{
		Revision: 7, State: "paused", Track: &ipc.TrackInfo{Title: "Song", Path: "/music/song.mp3"},
		Position: 12, Duration: 180, Volume: -6, Playlist: "Mix", Index: 2, Total: 9,
		Visualizer: "Bars", Shuffle: &shuffle, Repeat: "all", Mono: &mono, Speed: 1.5,
		EQPreset: "Rock", EQBands: []float64{1, 2}, Theme: &ipc.ThemeInfo{Name: "Nord"}, Device: "DAC",
	}
	startTestIPC(t, snapshot, func(*ipc.JobStore, string, ipc.V2Request) {})

	got, err := ipcState()
	if err != nil {
		t.Fatal(err)
	}
	want := ipc.Response{
		OK: true, State: "paused", Track: snapshot.Track, Position: 12, Duration: 180,
		Volume: -6, Playlist: "Mix", Index: 2, Total: 9, Visualizer: "Bars",
		Shuffle: &shuffle, Repeat: "all", Mono: &mono, Speed: 1.5, EQPreset: "Rock",
		Theme: snapshot.Theme, EQBands: []float64{1, 2},
	}
	if result := stateResult(got); !reflect.DeepEqual(result, want) {
		t.Fatalf("stateResult = %+v, want %+v", result, want)
	}
}

// With no socket, every client call says that cliamp is not running.
func TestIPCClientNotRunning(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	want := "cliamp is not running (no socket at " + ipc.DefaultSocketPath() + ")"
	if _, err := ipcSend("next", ipc.Request{}); err == nil || err.Error() != want {
		t.Errorf("ipcSend error = %v, want %q", err, want)
	}
	if _, err := ipcState(); err == nil || err.Error() != want {
		t.Errorf("ipcState error = %v, want %q", err, want)
	}
	if ipcRunning() {
		t.Error("ipcRunning() = true with no socket")
	}
}

func TestV2ResponseError(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response ipc.V2Response
		want     string
	}{
		{name: "ok", response: ipc.V2Response{OK: true}},
		{name: "no error body", response: ipc.V2Response{}, want: "remote operation failed"},
		{
			name:     "error",
			response: ipc.V2Response{Error: &ipc.V2Error{Code: "invalid_params", Message: "invalid parameters"}},
			want:     "remote operation failed (invalid_params): invalid parameters",
		},
		{
			name:     "error with detail",
			response: ipc.V2Response{Error: &ipc.V2Error{Code: "not_found", Message: "resource not found", Detail: "no theme x"}},
			want:     "remote operation failed (not_found): resource not found (no theme x)",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := v2ResponseError(tt.response)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("error = %v, want none", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

// cliamp vis list prints the modes of the running cliamp, with its Lua
// visualizers, and marks the row of the active mode. When cliamp does not
// run, or has no vis operation as in headless mode, it prints the built-in
// modes.
func TestVisModes(t *testing.T) {
	builtIn := ui.VisModeNames()
	withLua := append(slices.Clone(builtIn), "plugin-vis")
	withLuaBars := append(slices.Clone(builtIn), "Bars")
	luaRow := len(builtIn)
	tests := []struct {
		name        string
		serve       bool
		active      string        // the visualizer of the snapshot
		list        *ipc.Response // the vis list result; nil fails the job
		wantNames   []string
		wantActive  int
		wantRunning bool
	}{
		{
			name: "running", serve: true, active: "plugin-vis",
			list:      &ipc.Response{OK: true, Items: withLua, Visualizer: "plugin-vis", Index: luaRow},
			wantNames: withLua, wantActive: luaRow, wantRunning: true,
		},
		{
			name: "Lua mode with a built-in name", serve: true, active: "Bars",
			list:      &ipc.Response{OK: true, Items: withLuaBars, Visualizer: "Bars", Index: luaRow},
			wantNames: withLuaBars, wantActive: luaRow, wantRunning: true,
		},
		{
			name: "built-in mode with a Lua namesake", serve: true, active: "Bars",
			list:      &ipc.Response{OK: true, Items: withLuaBars, Visualizer: "Bars", Index: 0},
			wantNames: withLuaBars, wantActive: 0, wantRunning: true,
		},
		{
			name: "list without the active mode", serve: true, active: "plugin-vis",
			list:      &ipc.Response{OK: true, Items: withLua},
			wantNames: withLua, wantActive: luaRow, wantRunning: true,
		},
		{name: "headless", serve: true, wantNames: builtIn, wantActive: -1, wantRunning: true},
		{name: "not running", wantNames: builtIn, wantActive: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.serve {
				startTestIPC(t, ipc.RuntimeSnapshot{Visualizer: tt.active}, func(jobs *ipc.JobStore, id string, _ ipc.V2Request) {
					_, _ = jobs.Start(id)
					if tt.list == nil {
						_ = jobs.Fail(id, ipc.V2Error{Code: ipc.V2ErrorCodeUnavailable, Message: ipc.V2MessageUnavailable})
						return
					}
					result, _ := json.Marshal(tt.list)
					_ = jobs.Succeed(id, result)
				})
			} else {
				t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			}
			names, active, running := visModes()
			if !slices.Equal(names, tt.wantNames) || active != tt.wantActive || running != tt.wantRunning {
				t.Fatalf("visModes() = %v, %d, %v; want %v, %d, %v", names, active, running, tt.wantNames, tt.wantActive, tt.wantRunning)
			}
		})
	}
}
