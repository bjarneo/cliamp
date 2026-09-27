package luaplugin

import (
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// newQueueState builds an LState with cliamp.queue registered against the given
// providers and permission set. logger is a discard logger.
func newQueueState(t *testing.T, state *StateProvider, ctrl *ControlProvider, perms map[string]bool) *lua.LState {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	cliamp := L.NewTable()
	p := &Plugin{Name: "q", perms: perms}
	registerQueueAPI(L, cliamp, state, ctrl, p, newPluginLogger(""))
	L.SetGlobal("cliamp", cliamp)
	return L
}

func TestQueueReads(t *testing.T) {
	state := &StateProvider{
		PlaylistCount: func() int { return 3 },
		CurrentIndex:  func() int { return 1 },
		HasNext:       func() bool { return true },
		QueueList: func() []QueueEntry {
			return []QueueEntry{
				{Title: "A", Artist: "X", Path: "/a.mp3", Index: 0, Queued: false},
				{Title: "B", Artist: "Y", Path: "/b.mp3", Index: 1, Queued: true},
			}
		},
	}
	L := newQueueState(t, state, &ControlProvider{}, nil)

	if err := L.DoString(`
		_G.count = cliamp.queue.count()
		_G.cur = cliamp.queue.current()
		_G.hasnext = cliamp.queue.has_next()
		local list = cliamp.queue.list()
		_G.n = #list
		_G.title2 = list[2].title
		_G.queued2 = list[2].queued
		_G.idx1 = list[1].index
	`); err != nil {
		t.Fatal(err)
	}

	if got := float64(L.GetGlobal("count").(lua.LNumber)); got != 3 {
		t.Errorf("count = %v", got)
	}
	if got := float64(L.GetGlobal("cur").(lua.LNumber)); got != 1 {
		t.Errorf("current = %v", got)
	}
	if got := bool(L.GetGlobal("hasnext").(lua.LBool)); !got {
		t.Errorf("has_next = %v, want true", got)
	}
	if got := float64(L.GetGlobal("n").(lua.LNumber)); got != 2 {
		t.Errorf("list len = %v", got)
	}
	if got := L.GetGlobal("title2").String(); got != "B" {
		t.Errorf("list[2].title = %q", got)
	}
	if got := bool(L.GetGlobal("queued2").(lua.LBool)); !got {
		t.Errorf("list[2].queued = %v", got)
	}
	if got := float64(L.GetGlobal("idx1").(lua.LNumber)); got != 0 {
		t.Errorf("list[1].index = %v (want 0-based)", got)
	}
}

func TestQueueHasNextDefaultsFalse(t *testing.T) {
	L := newQueueState(t, &StateProvider{}, &ControlProvider{}, nil)
	if err := L.DoString(`_G.hasnext = cliamp.queue.has_next()`); err != nil {
		t.Fatal(err)
	}
	if got := bool(L.GetGlobal("hasnext").(lua.LBool)); got {
		t.Errorf("has_next without a provider = %v, want false", got)
	}
}

func TestQueueMutatorsRequireControl(t *testing.T) {
	var calls []string
	ctrl := &ControlProvider{
		QueueAdd:    func(string) { calls = append(calls, "add") },
		QueueJump:   func(int) { calls = append(calls, "jump") },
		QueueRemove: func(int) { calls = append(calls, "remove") },
		QueueMove:   func(int, int) { calls = append(calls, "move") },
	}

	// Without the control permission, every mutator is a no-op.
	L := newQueueState(t, &StateProvider{}, ctrl, nil)
	if err := L.DoString(`
		cliamp.queue.add("/x.mp3")
		cliamp.queue.jump(2)
		cliamp.queue.remove(0)
		cliamp.queue.move(1, 0)
	`); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("mutators ran without control permission: %v", calls)
	}

	// With the control permission, they dispatch to the provider.
	calls = nil
	L2 := newQueueState(t, &StateProvider{}, ctrl, map[string]bool{PermControl: true})
	if err := L2.DoString(`
		cliamp.queue.add("/x.mp3")
		cliamp.queue.jump(2)
		cliamp.queue.remove(0)
		cliamp.queue.move(1, 0)
	`); err != nil {
		t.Fatal(err)
	}
	want := []string{"add", "jump", "remove", "move"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
}

func TestQueueMutatorArgsForwarded(t *testing.T) {
	var (
		gotPath     string
		gotJump     int
		gotFrom, to int
	)
	ctrl := &ControlProvider{
		QueueAdd:  func(p string) { gotPath = p },
		QueueJump: func(i int) { gotJump = i },
		QueueMove: func(f, t int) { gotFrom, to = f, t },
	}
	L := newQueueState(t, &StateProvider{}, ctrl, map[string]bool{PermControl: true})
	if err := L.DoString(`
		cliamp.queue.add("https://example.com/song.mp3")
		cliamp.queue.jump(5)
		cliamp.queue.move(3, 1)
	`); err != nil {
		t.Fatal(err)
	}
	if gotPath != "https://example.com/song.mp3" {
		t.Errorf("add path = %q", gotPath)
	}
	if gotJump != 5 {
		t.Errorf("jump index = %d", gotJump)
	}
	if gotFrom != 3 || to != 1 {
		t.Errorf("move = (%d,%d), want (3,1)", gotFrom, to)
	}
}

func TestQueueAddTrackTable(t *testing.T) {
	var got []QueueTrack
	var gotPaths []string
	ctrl := &ControlProvider{
		QueueAdd:      func(p string) { gotPaths = append(gotPaths, p) },
		QueueAddTrack: func(tr QueueTrack) { got = append(got, tr) },
	}
	L := newQueueState(t, &StateProvider{}, ctrl, map[string]bool{PermControl: true})
	if err := L.DoString(`
		_G.ok = cliamp.queue.add({
			path = "spotify:track:69kOkLUCkxIZYexIgSG8rq", title = "Get Lucky",
			artist = "Daft Punk", album = "Random Access Memories", genre = "Disco",
			year = 2013, duration = 369, stream = false,
			index = 4, queued = true, -- extra keys from queue.list rows are ignored
		})
		_G.minimal = cliamp.queue.add({ path = "tidal://track/1" })
	`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("ok") != lua.LTrue || L.GetGlobal("minimal") != lua.LTrue {
		t.Fatalf("add returned %v, %v; want true, true", L.GetGlobal("ok"), L.GetGlobal("minimal"))
	}
	want := []QueueTrack{
		{Path: "spotify:track:69kOkLUCkxIZYexIgSG8rq", Title: "Get Lucky", Artist: "Daft Punk",
			Album: "Random Access Memories", Genre: "Disco", Year: 2013, Duration: 369},
		{Path: "tidal://track/1"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tracks = %+v, want %+v", got, want)
	}
	if len(gotPaths) != 0 {
		t.Fatalf("table form went through path resolution: %v", gotPaths)
	}
}

func TestQueueAddTrackTableRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"missing path":   `{ title = "x" }`,
		"empty path":     `{ path = "  " }`,
		"numeric path":   `{ path = 5 }`,
		"title type":     `{ path = "/a.mp3", title = 5 }`,
		"year type":      `{ path = "/a.mp3", year = "2013" }`,
		"negative dur":   `{ path = "/a.mp3", duration = -1 }`,
		"huge year":      `{ path = "/a.mp3", year = 1e100 }`,
		"huge duration":  `{ path = "/a.mp3", duration = 1e10 }`,
		"NaN duration":   `{ path = "/a.mp3", duration = 0/0 }`,
		"stream type":    `{ path = "/a.mp3", stream = "yes" }`,
		"artist is list": `{ path = "/a.mp3", artist = { "a" } }`,
	}
	for name, table := range cases {
		t.Run(name, func(t *testing.T) {
			called := false
			ctrl := &ControlProvider{QueueAddTrack: func(QueueTrack) { called = true }}
			L := newQueueState(t, &StateProvider{}, ctrl, map[string]bool{PermControl: true})
			if err := L.DoString(`_G.ok, _G.err = cliamp.queue.add(` + table + `)`); err != nil {
				t.Fatal(err)
			}
			if L.GetGlobal("ok") != lua.LNil {
				t.Errorf("ok = %v, want nil", L.GetGlobal("ok"))
			}
			if msg, _ := L.GetGlobal("err").(lua.LString); msg == "" {
				t.Errorf("err = %v, want a message", L.GetGlobal("err"))
			}
			if called {
				t.Error("bad track was queued")
			}
		})
	}
}

func TestQueueAddTrackTableRequiresControl(t *testing.T) {
	called := false
	ctrl := &ControlProvider{QueueAddTrack: func(QueueTrack) { called = true }}
	L := newQueueState(t, &StateProvider{}, ctrl, nil)
	if err := L.DoString(`_G.ok, _G.err = cliamp.queue.add({ path = "/a.mp3" })`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("ok") != lua.LNil || !strings.Contains(L.GetGlobal("err").String(), "control") {
		t.Fatalf("add = %v, %v; want nil and a permission error", L.GetGlobal("ok"), L.GetGlobal("err"))
	}
	if called {
		t.Fatal("track queued without control permission")
	}
}

// A queue.list() row carries the event-table track fields, so passing it back
// to queue.add keeps the stream flag and metadata of provider tracks such as
// Tidal, whose non-HTTP paths would otherwise be queued as non-streams.
func TestQueueListRowRoundTripsThroughAdd(t *testing.T) {
	state := &StateProvider{QueueList: func() []QueueEntry {
		return []QueueEntry{{
			Title: "Song", Artist: "X", Album: "Y", Genre: "Jazz", Year: 1959,
			Path: "tidal://track/1", Duration: 200, Stream: true, Index: 0,
		}}
	}}
	var got []QueueTrack
	ctrl := &ControlProvider{QueueAddTrack: func(tr QueueTrack) { got = append(got, tr) }}
	L := newQueueState(t, state, ctrl, map[string]bool{PermControl: true})
	if err := L.DoString(`_G.ok, _G.err = cliamp.queue.add(cliamp.queue.list()[1])`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("ok") != lua.LTrue {
		t.Fatalf("add(list row) = %v, %v", L.GetGlobal("ok"), L.GetGlobal("err"))
	}
	want := QueueTrack{Path: "tidal://track/1", Title: "Song", Artist: "X", Album: "Y", Genre: "Jazz", Year: 1959, Duration: 200, Stream: true}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("queued %+v, want %+v", got, want)
	}
}
