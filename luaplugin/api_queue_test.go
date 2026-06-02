package luaplugin

import (
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
