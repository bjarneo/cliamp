package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui/model"
)

func TestV2Operations(t *testing.T) {
	appearance := []string{"theme", "vis"}
	plugins := []string{"plugin.call", "plugin.commands"}
	for _, tc := range []struct {
		name     string
		headless bool
		plugins  bool
		missing  []string
	}{
		{name: "TUI with plugins", plugins: true},
		{name: "TUI without plugins", missing: plugins},
		{name: "headless with plugins", headless: true, plugins: true, missing: appearance},
		{name: "headless without plugins", headless: true, missing: append(slices.Clone(appearance), plugins...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operations := v2Operations(tc.headless, tc.plugins)
			for _, name := range append(append([]string{"play", "queue.list", "provider.search"}, appearance...), plugins...) {
				_, ok := operations.Lookup(name)
				if want := !slices.Contains(tc.missing, name); ok != want {
					t.Errorf("%s registered = %v, want %v", name, ok, want)
				}
			}
		})
	}
}

// newTestPlugins loads the trusted plugins in sources, keyed by name, from a
// new config directory. With no sources the manager has no plugins.
func newTestPlugins(t *testing.T, sources map[string]string) *luaplugin.Manager {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	pluginDir := filepath.Join(dir, "plugins")
	for name, src := range sources {
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(pluginDir, name+".lua")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := plugintrust.Approve(pluginDir, name, path); err != nil {
			t.Fatal(err)
		}
	}
	mgr, err := luaplugin.New(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Close)
	return mgr
}

// A plugin job lists the plugin commands or runs one. It fails with no
// plugin manager, with bad parameters and when the command fails. A canceled
// job does not run.
func TestRunV2PluginJob(t *testing.T) {
	empty := newTestPlugins(t, nil)
	echo := newTestPlugins(t, map[string]string{"echo": `local p = plugin.register({name = "echo", type = "hook"})
p:command("say", function(args) return "said " .. args[1] end)`})
	for _, tt := range []struct {
		name      string
		plugins   *luaplugin.Manager
		operation string
		params    string
		cancel    bool
		want      ipc.JobState
		result    ipc.Response
		code      string
	}{
		{name: "no plugin manager", operation: "plugin.commands", want: ipc.JobFailed, code: ipc.V2ErrorCodeUnavailable},
		{name: "no plugins", plugins: empty, operation: "plugin.commands", want: ipc.JobSucceeded, result: ipc.Response{OK: true}},
		{name: "commands", plugins: echo, operation: "plugin.commands", want: ipc.JobSucceeded, result: ipc.Response{OK: true, Items: []string{"echo say"}}},
		{name: "call", plugins: echo, operation: "plugin.call", params: `{"name":"echo","sub":"say","args":["hi"]}`, want: ipc.JobSucceeded, result: ipc.Response{OK: true, Output: "said hi"}},
		{name: "unknown command", plugins: echo, operation: "plugin.call", params: `{"name":"echo","sub":"shout"}`, want: ipc.JobFailed, code: ipc.V2ErrorCodeInternal},
		{name: "call without plugins", plugins: empty, operation: "plugin.call", params: `{"name":"echo","sub":"say"}`, want: ipc.JobFailed, code: ipc.V2ErrorCodeInternal},
		{name: "no command name", plugins: echo, operation: "plugin.call", params: `{"name":"echo"}`, want: ipc.JobFailed, code: ipc.V2ErrorCodeInvalidParams},
		{name: "bad params", plugins: echo, operation: "plugin.call", params: `[1]`, want: ipc.JobFailed, code: ipc.V2ErrorCodeInvalidParams},
		{name: "canceled", plugins: echo, operation: "plugin.commands", cancel: true, want: ipc.JobCanceled, code: ipc.V2ErrorCodeCanceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			jobs := ipc.NewJobStore()
			job, err := jobs.Create(tt.operation)
			if err != nil {
				t.Fatal(err)
			}
			if tt.cancel {
				if err := jobs.Cancel(job.ID); err != nil {
					t.Fatal(err)
				}
			}
			runV2PluginJob(jobs, job.ID, ipc.V2Request{Operation: tt.operation, Params: json.RawMessage(tt.params)}, tt.plugins)

			got, ok := jobs.Get(job.ID)
			if !ok {
				t.Fatal("the job is gone")
			}
			if got.State != tt.want {
				t.Fatalf("state = %s, want %s (error %+v)", got.State, tt.want, got.Error)
			}
			if tt.code != "" {
				if got.Error == nil || got.Error.Code != tt.code {
					t.Fatalf("error = %+v, want code %s", got.Error, tt.code)
				}
				return
			}
			var result ipc.Response
			if err := json.Unmarshal(got.Result, &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result, tt.result) {
				t.Fatalf("result = %+v, want %+v", result, tt.result)
			}
		})
	}
}

// A job that is canceled while its plugin command runs stops the command.
// Before, the command kept the plugin lock for up to 5 minutes.
func TestRunV2PluginJobCancelStopsCommand(t *testing.T) {
	plugins := newTestPlugins(t, map[string]string{"spin": `local p = plugin.register({name = "spin", type = "hook"})
p:command("run", function() while true do cliamp.sleep(10) end end)
p:command("ping", function() return "pong" end)`})
	jobs := ipc.NewJobStore()
	job, err := jobs.Create("plugin.call")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		runV2PluginJob(jobs, job.ID, ipc.V2Request{Operation: "plugin.call", Params: json.RawMessage(`{"name":"spin","sub":"run"}`)}, plugins)
		close(done)
	}()
	for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
		if got, _ := jobs.Get(job.ID); got.State == ipc.JobRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the job did not start")
		}
	}
	if err := jobs.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runV2PluginJob did not return after the cancel")
	}
	if got, _ := jobs.Get(job.ID); got.State != ipc.JobCanceled {
		t.Fatalf("state = %s, want %s", got.State, ipc.JobCanceled)
	}
	// The command stopped, so the next command of the plugin runs at once.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if out, err := plugins.EmitCommand(ctx, "spin", "ping", nil); err != nil || out != "pong" {
		t.Fatalf("EmitCommand(ping) = %q, %v, want pong", out, err)
	}
}

// The dispatcher sends state.get and spectrum.get to the Model and returns
// its reply. It stops the wait when the request ends or the Model does not
// answer in time.
func TestV2DispatcherReads(t *testing.T) {
	timeout := v2ReplyTimeout
	v2ReplyTimeout = 50 * time.Millisecond
	t.Cleanup(func() { v2ReplyTimeout = timeout })

	snapshot := ipc.RuntimeSnapshot{State: "playing"}
	answer := func(msg tea.Msg) {
		if request, ok := msg.(model.V2RequestMsg); ok && request.Reply != nil {
			request.Reply <- model.V2RequestResult{Result: ipc.V2Result{Snapshot: &snapshot}}
		}
	}
	for _, tt := range []struct {
		name     string
		method   string
		send     func(tea.Msg)
		canceled bool
		code     string
	}{
		{name: "state", method: "state.get", send: answer},
		{name: "spectrum", method: "spectrum.get", send: answer},
		{name: "no answer", method: "state.get", send: func(tea.Msg) {}, code: ipc.V2ErrorCodeUnavailable},
		{name: "canceled", method: "spectrum.get", send: func(tea.Msg) {}, canceled: true, code: ipc.V2ErrorCodeCanceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			dispatcher := newV2Dispatcher(tt.send, ipc.NewJobStore(), nil)
			result, v2Err := dispatcher.DispatchV2(ctx, ipc.V2Request{Method: tt.method})
			if tt.code != "" {
				if v2Err == nil || v2Err.Code != tt.code {
					t.Fatalf("error = %+v, want code %s", v2Err, tt.code)
				}
				return
			}
			if v2Err != nil {
				t.Fatalf("error = %+v", v2Err)
			}
			if result.Snapshot == nil || result.Snapshot.State != "playing" {
				t.Fatalf("snapshot = %+v, want the reply of the Model", result.Snapshot)
			}
		})
	}
}

// Every other operation becomes a queued job. The Model runs it, except
// plugin.call and plugin.commands, which run against the plugin manager. A
// full job store refuses the job.
func TestV2DispatcherJobs(t *testing.T) {
	for _, tt := range []struct {
		name      string
		operation string
		full      bool
		code      string
		toModel   bool
		failure   string
	}{
		{name: "Model job", operation: "next", toModel: true},
		{name: "plugin job", operation: "plugin.commands", failure: ipc.V2ErrorCodeUnavailable},
		{name: "full job store", operation: "next", full: true, code: ipc.V2ErrorCodeConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			jobs := ipc.NewJobStore(ipc.WithJobStoreCapacity(1))
			if tt.full {
				if _, err := jobs.Create("pause"); err != nil {
					t.Fatal(err)
				}
			}
			sent := make(chan model.V2RequestMsg, 1)
			send := func(msg tea.Msg) {
				if request, ok := msg.(model.V2RequestMsg); ok {
					sent <- request
				}
			}
			result, v2Err := newV2Dispatcher(send, jobs, nil).DispatchV2(context.Background(), ipc.V2Request{Operation: tt.operation})
			if tt.code != "" {
				if v2Err == nil || v2Err.Code != tt.code {
					t.Fatalf("error = %+v, want code %s", v2Err, tt.code)
				}
				return
			}
			if v2Err != nil {
				t.Fatalf("error = %+v", v2Err)
			}
			if result.Job == nil || result.Job.State != ipc.JobQueued || result.Job.Operation != tt.operation {
				t.Fatalf("job = %+v, want a queued %s job", result.Job, tt.operation)
			}

			if tt.toModel {
				select {
				case request := <-sent:
					if request.Jobs != jobs || request.JobID != result.Job.ID || request.Request.Operation != tt.operation {
						t.Fatalf("the Model got %+v, want job %s", request, result.Job.ID)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("the Model got no job")
				}
				return
			}
			select {
			case event := <-jobs.Events():
				if event.Job.ID != result.Job.ID || event.Job.Error == nil || event.Job.Error.Code != tt.failure {
					t.Fatalf("job event = %+v, want job %s to fail with %s", event, result.Job.ID, tt.failure)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the plugin job did not finish")
			}
			select {
			case request := <-sent:
				t.Fatalf("the Model got the plugin job %+v", request)
			default:
			}
		})
	}
}

// Each finished job reaches the subscribers of runtime.job until the server
// shuts down.
func TestPublishV2JobEvents(t *testing.T) {
	broker := ipc.NewBroker()
	t.Cleanup(broker.Close)
	subscription, err := broker.Subscribe([]string{"runtime.job"})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	jobs := ipc.NewJobStore()
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		publishV2JobEvents(done, jobs, broker)
		close(stopped)
	}()

	job, err := jobs.Create("next")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Start(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Succeed(job.ID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-subscription.Events():
		var got ipc.JobEvent
		if err := json.Unmarshal(event.Data, &got); err != nil {
			t.Fatal(err)
		}
		if event.Event != "runtime.job" || got.Job.ID != job.ID || got.Job.State != ipc.JobSucceeded {
			t.Fatalf("event %s = %+v, want job %s succeeded", event.Event, got, job.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no runtime.job event")
	}

	close(done)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the publisher did not stop with the server")
	}
}

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stderr := os.Stderr
	os.Stderr = f
	fn()
	os.Stderr = stderr
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// When another instance holds the socket, headless mode cannot start, so
// startIPC returns an error. The TUI prints the error and runs without the
// socket. The message has one ipc: prefix.
func TestStartIPCWithTheSocketInUse(t *testing.T) {
	for _, tt := range []struct {
		name     string
		headless bool
	}{
		{name: "headless", headless: true},
		{name: "TUI"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			startTestIPC(t, ipc.RuntimeSnapshot{}, func(*ipc.JobStore, string, ipc.V2Request) {})
			var stop func()
			var err error
			printed := captureStderr(t, func() {
				stop, err = startIPC(func(tea.Msg) {}, ipc.NewBroker(), nil, tt.headless, nil)
			})
			message := printed
			if tt.headless {
				if err == nil || stop != nil {
					t.Fatalf("startIPC = %v, want an error", err)
				}
				message = err.Error()
			} else if err != nil || stop == nil {
				t.Fatalf("startIPC error = %v, want the TUI to run on", err)
			}
			if !strings.Contains(message, "cliamp is already running") {
				t.Fatalf("message = %q, want the running instance", message)
			}
			if !strings.HasPrefix(message, "ipc: ") || strings.Count(message, "ipc:") != 1 {
				t.Fatalf("message = %q, want one ipc: prefix", message)
			}
			if tt.headless && printed != "" {
				t.Fatalf("stderr = %q, want nothing", printed)
			}
		})
	}
}

// startIPC serves the V2 requests through send and the operations of the
// mode. stop removes the socket.
func TestStartIPCServesTheModel(t *testing.T) {
	for _, tt := range []struct {
		name     string
		headless bool
	}{
		{name: "headless", headless: true},
		{name: "TUI"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", socketDir(t))
			send := func(msg tea.Msg) {
				if request, ok := msg.(model.V2RequestMsg); ok && request.Reply != nil {
					request.Reply <- model.V2RequestResult{Result: ipc.V2Result{Snapshot: &ipc.RuntimeSnapshot{State: "paused"}}}
				}
			}
			stop, err := startIPC(send, ipc.NewBroker(), nil, tt.headless, nil)
			if err != nil {
				t.Fatal(err)
			}
			socket := ipc.DefaultSocketPath()

			response, err := ipc.SendV2(socket, ipc.V2Request{ID: json.RawMessage(`1`), Method: "state.get"})
			if err != nil {
				stop()
				t.Fatal(err)
			}
			if response.Snapshot == nil || response.Snapshot.State != "paused" {
				stop()
				t.Fatalf("state.get = %+v, want the snapshot of the Model", response)
			}
			response, err = ipc.SendV2(socket, ipc.V2Request{ID: json.RawMessage(`2`), Operation: "theme", Params: json.RawMessage(`{"name":"x"}`)})
			if err != nil {
				stop()
				t.Fatal(err)
			}
			if unknown := response.Error != nil && response.Error.Code == ipc.V2ErrorCodeUnknownOperation; unknown != tt.headless {
				stop()
				t.Fatalf("theme error = %+v, want unknown operation %v", response.Error, tt.headless)
			}

			stop()
			if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("socket after stop: %v, want it removed", err)
			}
		})
	}
}

// A headless cliamp fails cliamp theme <name> and cliamp vis <name|next>.
// The list forms still work, as headless.md says.
func TestHeadlessAppearanceCommands(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", socketDir(t))
	send := func(msg tea.Msg) {
		if request, ok := msg.(model.V2RequestMsg); ok && request.Reply != nil {
			request.Reply <- model.V2RequestResult{Result: ipc.V2Result{Snapshot: &ipc.RuntimeSnapshot{State: "paused", Visualizer: "Wave"}}}
		}
	}
	stop, err := startIPC(send, ipc.NewBroker(), nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)

	for _, tt := range []struct {
		args    []string
		wantErr bool
		want    string // a line of the output
	}{
		{args: []string{"theme", "list"}, want: "  " + theme.DefaultName},
		{args: []string{"theme", "Nord"}, wantErr: true},
		{args: []string{"vis", "list"}, want: "* Wave"},
		{args: []string{"vis", "Bars"}, wantErr: true},
		{args: []string{"vis", "next"}, wantErr: true},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var runErr error
			stdout, _ := captureOutput(t, func() {
				runErr = buildApp().Run(t.Context(), append([]string{"cliamp"}, tt.args...))
			})
			switch {
			case tt.wantErr && (runErr == nil || !strings.Contains(runErr.Error(), "unknown operation")):
				t.Fatalf("error = %v, want unknown operation", runErr)
			case !tt.wantErr && runErr != nil:
				t.Fatal(runErr)
			}
			if tt.want != "" && !slices.Contains(strings.Split(stdout, "\n"), tt.want) {
				t.Errorf("output = %q, want the line %q", stdout, tt.want)
			}
		})
	}
}

// Headless mode has no screen, so it keeps the default visualizer that
// spectrum.get uses. The TUI applies the configured visualizer.
func TestConfigureModel(t *testing.T) {
	for _, tt := range []struct {
		name     string
		headless bool
		want     string
	}{
		{name: "headless", headless: true, want: "Bars"},
		{name: "TUI", want: "Wave"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := model.New(&player.Player{}, playlist.New(), nil, "cliamp", nil, nil, nil, nil, nil, config.SaveFunc{})
			configureModel(&m, config.Config{Visualizer: "Wave"}, tt.headless, false)
			if got := m.VisualizerName(); got != tt.want {
				t.Fatalf("visualizer = %q, want %q", got, tt.want)
			}
		})
	}
}

// A second headless instance stops before it builds the providers, opens
// the audio device or loads the plugins. The app.quit hook of a plugin
// would otherwise change the files of the running instance.
func TestHeadlessSecondInstance(t *testing.T) {
	startTestIPC(t, ipc.RuntimeSnapshot{}, func(*ipc.JobStore, string, ipc.V2Request) {})
	dir := os.Getenv("CLIAMP_CONFIG_DIR")
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(name, filepath.Join(home, name))
	}
	// Keep a regression off the session bus and the sound card.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(home, "bus"))
	if err := os.WriteFile(filepath.Join(home, ".asoundrc"), []byte("pcm.!default { type null }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "loaded")
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pluginDir, "marker.lua")
	src := `plugin.register({name = "marker", type = "hook"})
cliamp.fs.write(` + strconv.Quote(marker) + `, "loaded")`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugintrust.Approve(pluginDir, "marker", path); err != nil {
		t.Fatal(err)
	}

	err := run(config.Overrides{}, nil, true, false)
	if err == nil || !strings.Contains(err.Error(), "cliamp is already running") || !strings.Contains(err.Error(), ipc.DefaultSocketPath()) {
		t.Fatalf("run error = %v, want the running instance and its socket", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plugin marker: %v, want no plugin loaded", err)
	}
	log, err := os.ReadFile(filepath.Join(dir, "cliamp.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "provider registered") {
		t.Fatalf("cliamp.log = %q, want no provider built", log)
	}
}

// A client can send jobs back to back without a wait for each result. The
// dispatcher acknowledges each job while the event loop is busy, and the
// Model gets the jobs in the order that they came in.
func TestV2JobsReachTheModelInOrder(t *testing.T) {
	const n = 50
	sink := newBlockingSend(n)
	queue, stop := newOrderedSender(sink.send)
	defer stop()
	dispatcher := newV2Dispatcher(queue, ipc.NewJobStore(), nil)

	ids := make([]string, n)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range n {
			result, v2Err := dispatcher.DispatchV2(context.Background(), ipc.V2Request{Operation: "queue.move"})
			if v2Err != nil || result.Job == nil {
				t.Errorf("job %d = %+v, %+v", i, result, v2Err)
				return
			}
			ids[i] = result.Job.ID
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a job waited for the event loop")
	}

	close(sink.release)
	for i := range n {
		select {
		case msg := <-sink.got:
			if request := msg.(model.V2RequestMsg); request.JobID != ids[i] {
				t.Fatalf("message %d is job %s, want %s", i, request.JobID, ids[i])
			}
		case <-time.After(time.Second):
			t.Fatalf("message %d did not arrive", i)
		}
	}
}
