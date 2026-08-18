package luaplugin

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

type capturedEvent struct {
	topic  string
	data   json.RawMessage
	retain bool
}

// capturePublisher records calls in order and is safe for concurrent use, since
// async hooks publish from their own goroutines.
type capturePublisher struct {
	mu     sync.Mutex
	events []capturedEvent
	calls  []string
}

func (p *capturePublisher) Publish(topic string, data json.RawMessage, retain bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, capturedEvent{topic: topic, data: append(json.RawMessage(nil), data...), retain: retain})
	p.calls = append(p.calls, "publish "+topic)
	return nil
}

func (p *capturePublisher) ClearPrefix(prefix string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "clear "+prefix)
}

func (p *capturePublisher) captured() ([]capturedEvent, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.events, p.calls
}

func TestPluginPublishUsesInstalledNamespaceAndRetention(t *testing.T) {
	manager := newTestManager()
	publisher := &capturePublisher{}
	manager.SetEventPublisher(publisher)

	loadTestPlugin(t, manager, "installed-name", `
		local p = plugin.register({name = "spoofed-name", type = "hook"})
		local ok, err = p:publish("playback", {status = "playing", position = 12}, {retain = true})
		if not ok then error(err) end
	`)

	events, _ := publisher.captured()
	if len(events) != 1 {
		t.Fatalf("published events = %d, want 1", len(events))
	}
	event := events[0]
	if event.topic != "plugin.installed-name.playback" || !event.retain {
		t.Fatalf("event = %#v", event)
	}
	var payload map[string]any
	if err := json.Unmarshal(event.data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "playing" || payload["position"] != float64(12) {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestPluginPublishReportsUnavailablePublisher(t *testing.T) {
	manager := newTestManager()
	plugin := loadTestPlugin(t, manager, "test", `
		local p = plugin.register({name = "test", type = "hook"})
		_G.ok, _G.err = p:publish("playback", {})
	`)
	if plugin.L.GetGlobal("ok") != lua.LNil {
		t.Fatal("publish unexpectedly succeeded")
	}
	if plugin.L.GetGlobal("err").String() != "plugin event publisher is unavailable" {
		t.Fatalf("error = %q", plugin.L.GetGlobal("err").String())
	}
}

// A dotted install name must not be able to produce the same topic as another
// plugin publishing a dotted topic.
func TestPluginPublishNamespaceAvoidsDottedNameCollision(t *testing.T) {
	manager := newTestManager()
	publisher := &capturePublisher{}
	manager.SetEventPublisher(publisher)

	loadTestPlugin(t, manager, "foo.bar", `
		local p = plugin.register({name = "foo.bar", type = "hook"})
		p:publish("playback", {})
	`)
	loadTestPlugin(t, manager, "foo", `
		local p = plugin.register({name = "foo", type = "hook"})
		p:publish("bar.playback", {})
	`)

	events, _ := publisher.captured()
	if len(events) != 2 {
		t.Fatalf("published events = %d, want 2", len(events))
	}
	if events[0].topic != "plugin.foo_bar.playback" {
		t.Errorf("dotted plugin topic = %q, want plugin.foo_bar.playback", events[0].topic)
	}
	if events[1].topic != "plugin.foo.bar.playback" {
		t.Errorf("dotted topic = %q, want plugin.foo.bar.playback", events[1].topic)
	}
	if events[0].topic == events[1].topic {
		t.Fatalf("namespaces collided on %q", events[0].topic)
	}
}

// Folding "." to "_" is lossy, so two names that fold alike must not end up
// sharing a namespace: the first loaded keeps it, the second cannot publish.
func TestPluginPublishRejectsFoldedNamespaceClash(t *testing.T) {
	manager := newTestManager()
	publisher := &capturePublisher{}
	manager.SetEventPublisher(publisher)

	loadTestPlugin(t, manager, "my.plugin", `
		local p = plugin.register({name = "my.plugin", type = "hook"})
		local ok, err = p:publish("playback", {})
		if not ok then error(err) end
	`)
	late := loadTestPlugin(t, manager, "my_plugin", `
		local p = plugin.register({name = "my_plugin", type = "hook"})
		_G.ok, _G.err = p:publish("playback", {})
	`)

	if late.L.GetGlobal("ok") != lua.LNil {
		t.Fatal("second plugin published into a namespace it does not own")
	}
	if got := late.L.GetGlobal("err").String(); !strings.Contains(got, `already used by plugin "my.plugin"`) {
		t.Errorf("error = %q, want the owning plugin named", got)
	}
	events, _ := publisher.captured()
	if len(events) != 1 {
		t.Fatalf("published events = %d, want 1", len(events))
	}
	if events[0].topic != "plugin.my_plugin.playback" {
		t.Errorf("topic = %q, want plugin.my_plugin.playback", events[0].topic)
	}
}

// A file that never registers must release its namespace for the next plugin,
// including when plugin.register() renamed it before the chunk failed.
func TestNamespaceReleasedWhenPluginFailsToLoad(t *testing.T) {
	manager := newTestManager()
	publisher := &capturePublisher{}
	manager.SetEventPublisher(publisher)

	loadTestPluginExpectError(t, manager, "my.plugin", `error("boom")`)
	loadTestPlugin(t, manager, "my_plugin", `
		local p = plugin.register({name = "my_plugin", type = "hook"})
		local ok, err = p:publish("playback", {})
		if not ok then error(err) end
	`)

	events, _ := publisher.captured()
	if len(events) != 1 || events[0].topic != "plugin.my_plugin.playback" {
		t.Fatalf("events = %#v, want one plugin.my_plugin.playback", events)
	}
}

// plugin.register() renames Plugin.Name, so namespace release must key off the
// installed filename instead.
func TestNamespaceReleasedAfterRegisterRenamesPlugin(t *testing.T) {
	manager := newTestManager()
	publisher := &capturePublisher{}
	manager.SetEventPublisher(publisher)

	loadTestPluginExpectError(t, manager, "my.plugin", `
		plugin.register({name = "renamed-after-claim", type = "hook"})
		error("boom")
	`)
	if owner, taken := manager.namespaces["my_plugin"]; taken {
		t.Fatalf("namespace still claimed by %q after failed load", owner)
	}

	late := loadTestPlugin(t, manager, "my_plugin", `
		local p = plugin.register({name = "my_plugin", type = "hook"})
		_G.ok, _G.err = p:publish("playback", {})
	`)
	if late.L.GetGlobal("ok") == lua.LNil {
		t.Fatalf("publish failed: %s", late.L.GetGlobal("err").String())
	}
	events, _ := publisher.captured()
	if len(events) != 1 || events[0].topic != "plugin.my_plugin.playback" {
		t.Fatalf("events = %#v, want one plugin.my_plugin.playback", events)
	}
}

func TestManagerCloseClearsRetainedAfterPublishersStop(t *testing.T) {
	manager := newTestManager()
	publisher := &capturePublisher{}
	manager.SetEventPublisher(publisher)
	loadTestPlugin(t, manager, "late", `plugin.register({name = "late", type = "hook"})`)

	// Stand in for an async hook goroutine that is still running when Close
	// starts and publishes a retained event just before it finishes. Close must
	// wait for it, then clear the namespace — not the other way around.
	manager.wg.Add(1)
	go func() {
		defer manager.wg.Done()
		time.Sleep(20 * time.Millisecond)
		_ = publisher.Publish("plugin.late.playback", json.RawMessage(`{}`), true)
	}()

	manager.Close()

	_, calls := publisher.captured()
	want := []string{"publish plugin.late.playback", "clear plugin.late."}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i, call := range calls {
		if call != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
}
