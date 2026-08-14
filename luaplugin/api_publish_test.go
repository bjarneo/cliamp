package luaplugin

import (
	"encoding/json"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

type capturedEvent struct {
	topic  string
	data   json.RawMessage
	retain bool
}

type capturePublisher struct{ events []capturedEvent }

func (p *capturePublisher) Publish(topic string, data json.RawMessage, retain bool) error {
	p.events = append(p.events, capturedEvent{topic: topic, data: append(json.RawMessage(nil), data...), retain: retain})
	return nil
}

func (p *capturePublisher) ClearPrefix(string) {}

func TestPluginPublishUsesInstalledNamespaceAndRetention(t *testing.T) {
	manager := newTestManager()
	publisher := &capturePublisher{}
	manager.SetEventPublisher(publisher)

	loadTestPlugin(t, manager, "installed-name", `
		local p = plugin.register({name = "spoofed-name", type = "hook"})
		local ok, err = p:publish("playback", {status = "playing", position = 12}, {retain = true})
		if not ok then error(err) end
	`)

	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(publisher.events))
	}
	event := publisher.events[0]
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
