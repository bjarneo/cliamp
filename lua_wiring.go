package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui/model"
)

// luaStateProvider gives Lua plugins read access to the playback state. The
// values come from the state that the Model publishes before each plugin
// event and after each Update, so cliamp.track.* reports the track that
// plays. Position and Duration read the engine clock, which moves between
// Updates.
func luaStateProvider(clock engineClock, load func() model.PluginState) luaplugin.StateProvider {
	return luaplugin.StateProvider{
		PlayerState:   func() string { return load().Status },
		Position:      func() float64 { return clock.Position().Seconds() },
		Duration:      func() float64 { return clock.Duration().Seconds() },
		Volume:        func() float64 { return load().Volume },
		Speed:         func() float64 { return load().Speed },
		Mono:          func() bool { return load().Mono },
		RepeatMode:    func() string { return load().Repeat },
		Shuffle:       func() bool { return load().Shuffle },
		EQBands:       func() [10]float64 { return load().EQBands },
		CurrentTrack:  func() luaplugin.Track { return load().Track },
		PlaylistCount: func() int { return load().Count },
		CurrentIndex:  func() int { return load().Index },
		HasNext:       func() bool { return load().HasNext },
		QueueList: func() []luaplugin.QueueEntry {
			if queue := load().Queue; queue != nil {
				return queue()
			}
			return nil
		},
	}
}

// engineClock is the part of the player that luaStateProvider reads live.
type engineClock interface {
	Position() time.Duration
	Duration() time.Duration
}

// luaControlProvider gives Lua plugins control of playback. Each control
// sends a message to the Model through send, so the Update loop makes every
// player change, tells the media controls and saves the settings.
func luaControlProvider(send func(tea.Msg)) luaplugin.ControlProvider {
	return luaplugin.ControlProvider{
		SetVolume:   func(db float64) { send(playback.SetVolumeMsg{VolumeDB: db}) },
		SetSpeed:    func(ratio float64) { send(playback.SetSpeedMsg{Ratio: ratio}) },
		SetEQBand:   func(band int, db float64) { send(model.SetEQBandMsg{Band: band, Gain: db}) },
		ToggleMono:  func() { send(playback.ToggleMonoMsg{}) },
		TogglePause: func() { send(playback.PlayPauseMsg{}) },
		Stop:        func() { send(playback.StopMsg{}) },
		Seek: func(secs float64) {
			send(playback.SeekMsg{Offset: time.Duration(secs * float64(time.Second))})
		},
		SetEQPreset: func(name string, bands *[10]float64) {
			send(model.SetEQPresetMsg{Name: name, Bands: bands})
		},
		Next: func() { send(playback.NextMsg{}) },
		Prev: func() { send(playback.PrevMsg{}) },
		QueueAdd: func(path string) {
			send(model.PluginQueueMsg{Op: "add", Path: path})
		},
		QueueAddTrack: func(t luaplugin.Track) {
			send(model.PluginQueueMsg{Op: "add_track", Track: playlist.Track{
				Path: t.Path, Title: t.Title, Artist: t.Artist, Album: t.Album,
				Genre: t.Genre, Year: t.Year, DurationSecs: t.Duration, Stream: t.Stream,
			}})
		},
		QueueJump: func(index int) {
			send(model.PluginQueueMsg{Op: "jump", Index: index})
		},
		QueueRemove: func(index int) {
			send(model.PluginQueueMsg{Op: "remove", Index: index})
		},
		QueueMove: func(from, to int) {
			send(model.PluginQueueMsg{Op: "move", Index: from, To: to})
		},
		QueueInsert: func(index int, t luaplugin.Track) {
			send(model.PluginQueueMsg{Op: "insert", Index: index, Track: playlist.Track{
				Path: t.Path, Title: t.Title, Artist: t.Artist, Album: t.Album,
				Genre: t.Genre, Year: t.Year, DurationSecs: t.Duration, Stream: t.Stream,
				AlbumArtURL: t.ArtURL,
			}})
		},
	}
}

// newOrderedSender returns a send func for the Lua providers and the V2 jobs
// of the socket, and a stop func. A plugin calls a control or cliamp.message
// while it holds its own lock, and the socket acknowledges a job before the
// Model runs it. send, which is prog.Send, waits until the event loop reads
// the message. The returned func only queues the message and returns at
// once. One goroutine passes the queued messages to send in order, as
// mediactl does for D-Bus calls. The queue has no bound, so no message is
// lost.
func newOrderedSender(send func(tea.Msg)) (queue func(tea.Msg), stop func()) {
	var (
		mu      sync.Mutex
		pending []tea.Msg
	)
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-wake:
			}
			mu.Lock()
			msgs := pending
			pending = nil
			mu.Unlock()
			for _, msg := range msgs {
				send(msg)
			}
		}
	}()
	queue = func(msg tea.Msg) {
		mu.Lock()
		pending = append(pending, msg)
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	return queue, sync.OnceFunc(func() { close(done) })
}

// luaUIProvider lets Lua plugins show a status message.
func luaUIProvider(send func(tea.Msg), headless bool) luaplugin.UIProvider {
	return luaplugin.UIProvider{
		ShowMessage: func(text string, duration time.Duration) {
			send(model.ShowStatusMsg{Text: text, Duration: duration})
		},
		ShowSelect:  showSelectFor(send, headless),
		ShowInput:   showInputFor(send, headless),
		UpdateInput: updateInputFor(send, headless),
		ShowText:    showTextFor(send, headless),
		SetVis: func(name string) {
			send(model.SetVisMsg{Name: name})
		},
	}
}

// showSelectFor returns nil in headless mode: there is no UI to answer a
// selector, and the API reports it as unavailable instead of accepting a
// request nobody can complete.
func showTextFor(send func(tea.Msg), headless bool) func(string, string, func()) {
	if headless {
		return nil
	}
	return func(title, text string, onClose func()) {
		send(model.LuaTextMsg{Title: title, Text: text, OnClose: onClose})
	}
}

func showInputFor(send func(tea.Msg), headless bool) func(string, string, string, []luaplugin.SelectOption, func(string), func(string), func(int), func()) {
	if headless {
		return nil
	}
	return func(title, placeholder, value string, suggestions []luaplugin.SelectOption, onChange, onSubmit func(string), onSelect func(int), onCancel func()) {
		send(model.LuaInputMsg{
			Title:       title,
			Placeholder: placeholder,
			Value:       value,
			Suggestions: suggestions,
			OnChange:    onChange,
			OnSubmit:    onSubmit,
			OnSelect:    onSelect,
			OnCancel:    onCancel,
		})
	}
}

func updateInputFor(send func(tea.Msg), headless bool) func([]luaplugin.SelectOption, bool) {
	if headless {
		return nil
	}
	return func(options []luaplugin.SelectOption, live bool) {
		send(model.LuaInputSuggestionsMsg{Options: options, Live: live})
	}
}

func showSelectFor(send func(tea.Msg), headless bool) func(string, string, []luaplugin.SelectOption, int, func(int), func()) {
	if headless {
		return nil
	}
	return func(title, body string, options []luaplugin.SelectOption, cursor int, onDone func(index int), onCancel func()) {
		send(model.LuaSelectMsg{Title: title, Body: body, Options: options, Cursor: cursor, OnDone: onDone, OnCancel: onCancel})
	}
}

// luaProviderProvider exposes the provider context and the chat actions a
// provider supports. The dispatcher keeps the Lua API generic: a plugin asks
// for "telegram.send" and the provider that implements ChatCommander runs it.
// The provider call runs on the caller's goroutine, so a blocking reply never
// holds the UI.
func luaProviderProvider(entries []provider.Entry, load func() model.PluginState, send func(tea.Msg)) luaplugin.ProviderProvider {
	return luaplugin.ProviderProvider{
		Current: func() luaplugin.ProviderInfo { return load().Provider },
		Open: func(info luaplugin.ProviderInfo) error {
			if send == nil {
				return fmt.Errorf("ui: not available")
			}
			send(model.PluginOpenProviderMsg{
				ProviderName: info.Name,
				Playlist: playlist.PlaylistInfo{
					ID:      info.PlaylistID,
					Name:    info.PlaylistName,
					Section: info.PlaylistSection,
				},
			})
			return nil
		},
		Call: func(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
			name, verb, ok := strings.Cut(action, ".")
			if !ok || name == "" || verb == "" {
				return nil, fmt.Errorf("unknown provider action %q", action)
			}
			var prov playlist.Provider
			for _, entry := range entries {
				if strings.EqualFold(entry.Name, name) || strings.EqualFold(entry.Key, name) {
					prov = entry.Provider
					break
				}
			}
			if prov == nil {
				return nil, fmt.Errorf("provider %q is not available", name)
			}
			chat, _ := params["chat"].(string)
			if chat == "" && verb != "resolve" {
				return nil, fmt.Errorf("%s: chat is required", action)
			}
			commander, ok := prov.(provider.ChatCommander)
			if !ok {
				return nil, fmt.Errorf("provider %q does not support chat actions", name)
			}
			switch verb {
			case "commands":
				commands, err := commander.BotCommands(ctx, chat)
				if err != nil {
					return nil, err
				}
				rows := make([]any, 0, len(commands))
				for _, command := range commands {
					rows = append(rows, map[string]any{
						"command":     command.Command,
						"description": command.Description,
					})
				}
				return map[string]any{"commands": rows}, nil
			case "send":
				text, _ := params["text"].(string)
				id, err := commander.SendCommand(ctx, chat, text)
				if err != nil {
					return nil, err
				}
				return map[string]any{"id": id}, nil
			case "ask":
				text, _ := params["text"].(string)
				id, err := commander.SendCommand(ctx, chat, text)
				if err != nil {
					return nil, err
				}
				return waitReplyResult(ctx, commander, chat, id, params)
			case "reply":
				return waitReplyResult(ctx, commander, chat, intParam(params, "after"), params)
			case "resolve":
				info, err := commander.ResolveChat(ctx, stringParam(params, "username"))
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"id":      info.ID,
					"name":    info.Name,
					"section": info.Section,
				}, nil
			case "add_chat":
				filter, ok := prov.(provider.ChatFilter)
				if !ok {
					return nil, fmt.Errorf("provider %q has no chat filter", name)
				}
				chat := stringParam(params, "chat")
				chats := filter.Chats()
				found := false
				for _, existing := range chats {
					if existing == chat {
						found = true
						break
					}
				}
				added := false
				if !found && len(chats) > 0 {
					chats = append(chats, chat)
					if err := filter.SetChats(chats); err != nil {
						return nil, err
					}
					added = true
				}
				return map[string]any{"added": added, "filtered": len(chats) > 0}, nil
			case "inline":
				bot := stringParam(params, "bot")
				if bot == "" {
					bot = chat
				}
				inline, err := commander.InlineResults(ctx, chat, bot, stringParam(params, "query"), stringParam(params, "offset"))
				if err != nil {
					return nil, err
				}
				rows := make([]any, 0, len(inline.Results))
				for _, result := range inline.Results {
					rows = append(rows, map[string]any{
						"id":          result.ID,
						"type":        result.Type,
						"kind":        result.Kind,
						"title":       result.Title,
						"description": result.Description,
						"url":         result.URL,
						"thumb_url":   result.ThumbURL,
					})
				}
				return map[string]any{
					// Lua 5.1 numbers are float64 and cannot hold a 64-bit
					// query id exactly. Keep it a string across the boundary.
					"query_id":    fmt.Sprintf("%d", inline.QueryID),
					"next_offset": inline.NextOffset,
					"results":     rows,
				}, nil
			case "send_inline":
				bot := stringParam(params, "bot")
				if bot == "" {
					bot = chat
				}
				id, err := commander.SendInlineResult(ctx, chat, bot, int64Param(params, "query_id"), stringParam(params, "result_id"))
				if err != nil {
					return nil, err
				}
				return map[string]any{"id": id}, nil
			case "message":
				reply, err := commander.Message(ctx, chat, intParam(params, "message_id"))
				if err != nil {
					return nil, err
				}
				return replyResult(reply), nil
			case "callback":
				data, err := base64.StdEncoding.DecodeString(stringParam(params, "data_b64"))
				if err != nil {
					return nil, fmt.Errorf("%s: data_b64: %w", action, err)
				}
				if err := commander.BotCallback(ctx, chat, intParam(params, "message_id"), data); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			default:
				return nil, fmt.Errorf("unknown provider action %q", action)
			}
		},
	}
}

func replyResult(reply provider.ChatReply) map[string]any {
	out := map[string]any{
		"kind":       reply.Kind,
		"text":       reply.Text,
		"from":       reply.From,
		"message_id": reply.MessageID,
	}
	if reply.Kind == "audio" {
		out["track"] = luaplugin.TrackData(pluginTrackFromPlaylist(reply.Track))
	}
	if len(reply.Links) > 0 {
		out["links"] = reply.Links
	}
	if len(reply.Buttons) > 0 {
		buttons := make([]any, 0, len(reply.Buttons))
		for _, button := range reply.Buttons {
			row := map[string]any{"text": button.Text, "kind": button.Kind}
			if len(button.Data) > 0 {
				row["data_b64"] = base64.StdEncoding.EncodeToString(button.Data)
			}
			if button.DataText != "" {
				row["data_text"] = button.DataText
			}
			if button.URL != "" {
				row["url"] = button.URL
			}
			if button.SendText {
				row["send_text"] = true
			}
			buttons = append(buttons, row)
		}
		out["buttons"] = buttons
	}
	return out
}

func waitReplyResult(ctx context.Context, commander provider.ChatCommander, chat string, after int, params map[string]any) (map[string]any, error) {
	timeout := 20 * time.Second
	if seconds, ok := params["timeout"].(float64); ok && seconds > 0 {
		timeout = time.Duration(seconds * float64(time.Second))
	}
	var (
		reply provider.ChatReply
		err   error
	)
	if audioOnly, _ := params["audio_only"].(bool); audioOnly {
		reply, err = commander.WaitAudio(ctx, chat, after, timeout)
	} else {
		reply, err = commander.WaitReply(ctx, chat, after, timeout)
	}
	if err != nil {
		return nil, err
	}
	out := replyResult(reply)
	out["id"] = after
	return out, nil
}

func pluginTrackFromPlaylist(track playlist.Track) luaplugin.Track {
	return luaplugin.Track{
		Title:    track.Title,
		Artist:   track.Artist,
		Album:    track.Album,
		Genre:    track.Genre,
		Path:     track.Path,
		Year:     track.Year,
		Number:   track.TrackNumber,
		Duration: track.DurationSecs,
		Stream:   track.Stream,
		ArtURL:   track.AlbumArtURL,
	}
}

func stringParam(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return value
}

func int64Param(params map[string]any, key string) int64 {
	switch value := params[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	case string:
		var out int64
		fmt.Sscan(value, &out)
		return out
	}
	return 0
}

func intParam(params map[string]any, key string) int {
	switch value := params[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	}
	return 0
}
