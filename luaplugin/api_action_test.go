package luaplugin

import "testing"

func TestActionRegisters(t *testing.T) {
	m := newTestManager()
	loadTestPlugin(t, m, "action-test", `
		local p = plugin.register({name = "action-test", type = "hook", permissions = {"ui"}})
		p:action{key = "!", id = "say", label = "Say", prompt = "Message", on_submit = function(text) end}
	`)
	actions := m.Actions("!")
	if len(actions) != 1 {
		t.Fatalf("actions = %#v", actions)
	}
	if actions[0].ID != "say" || actions[0].Label != "Say" || actions[0].Prompt != "Message" {
		t.Fatalf("action = %#v", actions[0])
	}
}

func TestFailedPluginActionsRemoved(t *testing.T) {
	m := newTestManager()
	loadTestPluginExpectError(t, m, "bad-action", `
		local p = plugin.register({name = "bad-action", type = "hook", permissions = {"ui"}})
		p:action{key = "!", id = "ghost", label = "Ghost", on_submit = function() end}
		error("boom")
	`)
	if got := m.Actions("!"); len(got) != 0 {
		t.Fatalf("actions = %#v, want none after a failed load", got)
	}
}
