package luaplugin

import (
	"testing"

	"github.com/bjarneo/cliamp/ui"
	lua "github.com/yuin/gopher-lua"
)

// fakeLayout records the calls the layout API makes.
type fakeLayout struct {
	added   []ui.PaneSpec
	removed []string
	weights map[string]int
	builtin map[string]bool
}

func (f *fakeLayout) AddPaneAt(region, pane string, weight int, position string) error {
	f.added = append(f.added, ui.PaneSpec{Region: ui.Region(region), Pane: pane, Weight: weight, Position: position})
	return nil
}

func (f *fakeLayout) AddPane(region, pane string, weight int) error {
	return f.AddPaneAt(region, pane, weight, "after")
}

func (f *fakeLayout) RemovePane(region, pane string) { f.removed = append(f.removed, pane) }

func (f *fakeLayout) Panes() []ui.PaneSpec { return f.added }

func (f *fakeLayout) Builtin(region string, enabled bool) {
	if f.builtin == nil {
		f.builtin = map[string]bool{}
	}
	f.builtin[region] = enabled
}

func (f *fakeLayout) Weight(region, pane string, weight int) {
	if f.weights == nil {
		f.weights = map[string]int{}
	}
	f.weights[pane] = weight
}

func layoutTestState(t *testing.T, perms ...string) (*lua.LState, *fakeLayout) {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	p := &Plugin{Name: "cover", perms: map[string]bool{}}
	for _, perm := range perms {
		p.perms[perm] = true
	}
	mgr := &Manager{}
	fake := &fakeLayout{}
	mgr.SetLayoutProvider(fake)
	cliamp := L.NewTable()
	registerLayoutAPI(L, cliamp, p, mgr)
	L.SetGlobal("cliamp", cliamp)
	return L, fake
}

func TestLayoutAPIRequiresPermission(t *testing.T) {
	L, fake := layoutTestState(t)
	if err := L.DoString(`_G.res, _G.err = cliamp.layout.add({region = "visualizer", weight = 1})`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("res") != lua.LNil {
		t.Fatalf("add without permission returned %v, want nil", L.GetGlobal("res"))
	}
	if len(fake.added) != 0 {
		t.Fatalf("add without permission reached the provider: %+v", fake.added)
	}
}

func TestLayoutAPIAddsItsOwnPane(t *testing.T) {
	L, fake := layoutTestState(t, PermLayout)
	if err := L.DoString(`cliamp.layout.add({region = "visualizer", weight = 2, position = "before"})`); err != nil {
		t.Fatal(err)
	}
	if len(fake.added) != 1 || fake.added[0].Pane != "cover" || fake.added[0].Weight != 2 || fake.added[0].Position != "before" {
		t.Fatalf("add = %+v, want cover 2 before", fake.added)
	}
}

func TestLayoutAPIRejectsForeignPane(t *testing.T) {
	L, fake := layoutTestState(t, PermLayout)
	if err := L.DoString(`_G.res, _G.err = cliamp.layout.remove({region = "visualizer", pane = "other"})`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("res") != lua.LNil {
		t.Fatalf("remove of a foreign pane returned %v, want nil", L.GetGlobal("res"))
	}
	if len(fake.removed) != 0 {
		t.Fatalf("remove of a foreign pane reached the provider: %v", fake.removed)
	}
}

func TestLayoutAPIWeightsBuiltinAndItself(t *testing.T) {
	L, fake := layoutTestState(t, PermLayout)
	if err := L.DoString(`
		cliamp.layout.set_weight({region = "visualizer", pane = "builtin", weight = 60})
		cliamp.layout.set_weight({region = "visualizer", weight = 40})
	`); err != nil {
		t.Fatal(err)
	}
	if fake.weights["builtin"] != 60 || fake.weights["cover"] != 40 {
		t.Fatalf("weights = %+v, want builtin 60 and cover 40", fake.weights)
	}
}
