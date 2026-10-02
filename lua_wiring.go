package main

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
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
func luaUIProvider(send func(tea.Msg)) luaplugin.UIProvider {
	return luaplugin.UIProvider{
		ShowMessage: func(text string, duration time.Duration) {
			send(model.ShowStatusMsg{Text: text, Duration: duration})
		},
	}
}
