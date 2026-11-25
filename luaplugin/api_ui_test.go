package luaplugin

import "testing"

func TestUIInputPassesSuggestions(t *testing.T) {
	m := newTestManager()
	var got []SelectOption
	m.SetUIProvider(UIProvider{
		ShowInput: func(_ string, _, _ string, suggestions []SelectOption, _ func(string), onSubmit func(string), _ func(int), _ func()) {
			got = suggestions
			onSubmit("x")
		},
	})
	loadTestPlugin(t, m, "input-suggestions", `
		local p = plugin.register({name = "input-suggestions", type = "hook", permissions = {"ui"}})
		cliamp.ui.input{
			title = "Message",
			suggestions = {"/start", {value = "/help", description = "How to use"}},
			on_submit = function(text) end,
		}
	`)
	if len(got) != 2 {
		t.Fatalf("suggestions = %#v", got)
	}
	if got[0].Title != "/start" || got[0].Description != "" {
		t.Errorf("suggestion 0 = %#v", got[0])
	}
	if got[1].Title != "/help" || got[1].Description != "How to use" {
		t.Errorf("suggestion 1 = %#v", got[1])
	}
}

func TestUITextPassesBody(t *testing.T) {
	m := newTestManager()
	var gotTitle, gotText string
	m.SetUIProvider(UIProvider{
		ShowText: func(title, text string, _ func()) {
			gotTitle, gotText = title, text
		},
	})
	loadTestPlugin(t, m, "ui-text", `
		local p = plugin.register({name = "ui-text", type = "hook", permissions = {"ui"}})
		cliamp.ui.text{title = "Reply", text = "line one\nline two"}
	`)
	if gotTitle != "Reply" || gotText != "line one\nline two" {
		t.Fatalf("title = %q text = %q", gotTitle, gotText)
	}
}

func TestUISelectPassesStructuredOptions(t *testing.T) {
	m := newTestManager()
	var got []SelectOption
	m.SetUIProvider(UIProvider{
		ShowSelect: func(_, _ string, options []SelectOption, _ int, onDone func(int), _ func()) {
			got = options
			onDone(0)
		},
	})
	loadTestPlugin(t, m, "ui-select", `
		local p = plugin.register({name = "ui-select", type = "hook", permissions = {"ui"}})
		cliamp.ui.select{
			title = "Results",
			options = {
				"/start",
				{title = "Get Lucky", subtitle = "Artist: Daft Punk", description = "Album: Random Access Memories"},
			},
			on_select = function(index) end,
		}
	`)
	if len(got) != 2 {
		t.Fatalf("options = %#v", got)
	}
	if got[0].Title != "/start" {
		t.Errorf("option 0 = %#v", got[0])
	}
	if got[1].Title != "Get Lucky" || got[1].Subtitle != "Artist: Daft Punk" || got[1].Description != "Album: Random Access Memories" {
		t.Errorf("option 1 = %#v", got[1])
	}
}

func TestUISuggestionsPassesImage(t *testing.T) {
	m := newTestManager()
	var got []SelectOption
	m.SetUIProvider(UIProvider{
		UpdateInput: func(options []SelectOption, live bool) {
			got = options
		},
	})
	loadTestPlugin(t, m, "ui-suggestions", `
		local p = plugin.register({name = "ui-suggestions", type = "hook", permissions = {"ui"}})
		cliamp.ui.suggestions({
			{title = "Get Lucky", subtitle = "Artist: Daft Punk", image = "IMAGE"},
		}, {live = true})
	`)
	if len(got) != 1 || got[0].Image != "IMAGE" {
		t.Fatalf("options = %#v", got)
	}
}
