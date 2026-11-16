package luaplugin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func TestProviderCurrent(t *testing.T) {
	m := newTestManager()
	m.SetProviderProvider(ProviderProvider{
		Current: func() ProviderInfo {
			return ProviderInfo{
				Name:             "Telegram",
				PlaylistID:       "u:1:2",
				PlaylistName:     "DeezLoad",
				PlaylistSection:  "Bots",
				SelectionID:      "u:3:4",
				SelectionName:    "Other",
				SelectionSection: "Chats",
			}
		},
	})
	loadTestPlugin(t, m, "provider-current", `
		local p = plugin.register({name = "provider-current", type = "hook", permissions = {"provider"}})
		local c = cliamp.provider.current()
		_G.name = c.name
		_G.playlist = c.playlist_name
		_G.playlist_section = c.playlist_section
		_G.selection = c.selection_name
		_G.selection_section = c.selection_section
	`)

	p := m.plugins[0]
	if got := p.L.GetGlobal("name").String(); got != "Telegram" {
		t.Errorf("name = %q", got)
	}
	if got := p.L.GetGlobal("playlist").String(); got != "DeezLoad" {
		t.Errorf("playlist = %q", got)
	}
	if got := p.L.GetGlobal("selection").String(); got != "Other" {
		t.Errorf("selection = %q", got)
	}
	if got := p.L.GetGlobal("playlist_section").String(); got != "Bots" {
		t.Errorf("playlist_section = %q", got)
	}
	if got := p.L.GetGlobal("selection_section").String(); got != "Chats" {
		t.Errorf("selection_section = %q", got)
	}
}

func TestProviderCallRead(t *testing.T) {
	m := newTestManager()
	var gotAction string
	m.SetProviderProvider(ProviderProvider{
		Current: func() ProviderInfo { return ProviderInfo{Name: "Telegram"} },
		Call: func(_ context.Context, action string, params map[string]any) (map[string]any, error) {
			gotAction = action
			if params["chat"] != "u:1:2" {
				t.Errorf("chat = %v", params["chat"])
			}
			return map[string]any{"commands": []any{
				map[string]any{"command": "/search", "description": "Find"},
			}}, nil
		},
	})
	loadTestPlugin(t, m, "provider-read", `
		local p = plugin.register({name = "provider-read", type = "hook", permissions = {"provider"}})
		local res = cliamp.provider.call("telegram.commands", {chat = "u:1:2"})
		_G.command = res.commands[1].command
		_G.description = res.commands[1].description
	`)
	if gotAction != "telegram.commands" {
		t.Errorf("action = %q", gotAction)
	}
	p := m.plugins[0]
	if got := p.L.GetGlobal("command").String(); got != "/search" {
		t.Errorf("command = %q", got)
	}
	if got := p.L.GetGlobal("description").String(); got != "Find" {
		t.Errorf("description = %q", got)
	}
}

func TestProviderCallWriteNeedsPermission(t *testing.T) {
	m := newTestManager()
	m.SetProviderProvider(ProviderProvider{
		Call: func(context.Context, string, map[string]any) (map[string]any, error) {
			t.Fatal("the call should not run without provider-send")
			return nil, nil
		},
	})
	loadTestPlugin(t, m, "provider-denied", `
		local p = plugin.register({name = "provider-denied", type = "hook", permissions = {"provider"}})
		local ok, err = cliamp.provider.call("telegram.send", {chat = "u:1:2", text = "/start"})
		_G.ok = ok
		_G.err = err
	`)
	p := m.plugins[0]
	if p.L.GetGlobal("ok") != lua.LNil {
		t.Error("call should return nil")
	}
	if got := p.L.GetGlobal("err").String(); !strings.Contains(got, "provider-send") {
		t.Errorf("err = %q", got)
	}
}

func TestProviderCallAsyncReply(t *testing.T) {
	m := newTestManager()
	messages := make(chan string, 1)
	m.SetUIProvider(UIProvider{
		ShowMessage: func(text string, _ time.Duration) { messages <- text },
	})
	m.SetProviderProvider(ProviderProvider{
		Call: func(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
			if action != "telegram.ask" {
				return nil, errors.New("unexpected action " + action)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(30 * time.Millisecond):
			}
			return map[string]any{
				"id":   10,
				"kind": "audio",
				"track": map[string]any{
					"title": "Walk", "artist": "Foo Fighters", "path": "telegram:u:1:2:10",
				},
			}, nil
		},
	})
	loadTestPlugin(t, m, "provider-async", `
		local p = plugin.register({name = "provider-async", type = "hook", permissions = {"provider", "provider-send"}})
		cliamp.provider.call("telegram.ask", {chat = "u:1:2", text = "/search walk"}, {
			on_reply = function(reply)
				cliamp.message(reply.kind .. ":" .. reply.track.title)
			end,
		})
	`)
	select {
	case got := <-messages:
		if got != "audio:Walk" {
			t.Fatalf("message = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the on_reply callback never ran")
	}
}

func TestProviderCallWriteRejectsMixedCaseAction(t *testing.T) {
	m := newTestManager()
	m.SetProviderProvider(ProviderProvider{
		Call: func(context.Context, string, map[string]any) (map[string]any, error) {
			t.Fatal("the call should not run without provider-send")
			return nil, nil
		},
	})
	loadTestPlugin(t, m, "provider-mixed", `
		local p = plugin.register({name = "provider-mixed", type = "hook", permissions = {"provider"}})
		local ok, err = cliamp.provider.call("Telegram.send", {chat = "u:1:2", text = "/start"})
		_G.ok = ok
		_G.err = err
	`)
	p := m.plugins[0]
	if p.L.GetGlobal("ok") != lua.LNil {
		t.Error("call should return nil")
	}
	if got := p.L.GetGlobal("err").String(); !strings.Contains(got, "provider-send") {
		t.Errorf("err = %q", got)
	}
}

func TestProviderCallSyncUsesTimeout(t *testing.T) {
	m := newTestManager()
	m.SetProviderProvider(ProviderProvider{
		Call: func(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	loadTestPlugin(t, m, "provider-timeout", `
		local p = plugin.register({name = "provider-timeout", type = "hook", permissions = {"provider"}})
		local res, err = cliamp.provider.call("telegram.commands", {chat = "u:1:2"}, {timeout = 0.05})
		_G.err = err
	`)
	p := m.plugins[0]
	if got := p.L.GetGlobal("err").String(); !strings.Contains(got, "deadline exceeded") {
		t.Errorf("err = %q", got)
	}
}

func TestProviderCallReplyIsRead(t *testing.T) {
	m := newTestManager()
	called := false
	m.SetProviderProvider(ProviderProvider{
		Call: func(context.Context, string, map[string]any) (map[string]any, error) {
			called = true
			return map[string]any{"kind": "text"}, nil
		},
	})
	loadTestPlugin(t, m, "provider-reply", `
		local p = plugin.register({name = "provider-reply", type = "hook", permissions = {"provider"}})
		local res = cliamp.provider.call("telegram.reply", {chat = "u:1:2", after = 5})
		_G.kind = res.kind
	`)
	if !called {
		t.Fatal("telegram.reply should be callable with only the provider permission")
	}
	if got := m.plugins[0].L.GetGlobal("kind").String(); got != "text" {
		t.Errorf("kind = %q", got)
	}
}
