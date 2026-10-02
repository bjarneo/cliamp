package main

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui/model"
)

// Each Lua control reaches the Model as a message, so the Update loop makes
// every player change.
func TestLuaControlProviderSendsMessages(t *testing.T) {
	bands := [10]float64{1, 2}
	tests := []struct {
		name string
		call func(luaplugin.ControlProvider)
		want tea.Msg
	}{
		{"set_volume", func(c luaplugin.ControlProvider) { c.SetVolume(-40) }, playback.SetVolumeMsg{VolumeDB: -40}},
		{"set_speed", func(c luaplugin.ControlProvider) { c.SetSpeed(1.5) }, playback.SetSpeedMsg{Ratio: 1.5}},
		{"toggle_mono", func(c luaplugin.ControlProvider) { c.ToggleMono() }, playback.ToggleMonoMsg{}},
		{"set_eq_band", func(c luaplugin.ControlProvider) { c.SetEQBand(2, 3) }, model.SetEQBandMsg{Band: 2, Gain: 3}},
		{"set_eq_preset", func(c luaplugin.ControlProvider) { c.SetEQPreset("Rock", &bands) }, model.SetEQPresetMsg{Name: "Rock", Bands: &bands}},
		{"play_pause", func(c luaplugin.ControlProvider) { c.TogglePause() }, playback.PlayPauseMsg{}},
		{"stop", func(c luaplugin.ControlProvider) { c.Stop() }, playback.StopMsg{}},
		{"seek", func(c luaplugin.ControlProvider) { c.Seek(1.5) }, playback.SeekMsg{Offset: 1500 * time.Millisecond}},
		{"seek back", func(c luaplugin.ControlProvider) { c.Seek(-10) }, playback.SeekMsg{Offset: -10 * time.Second}},
		{"next", func(c luaplugin.ControlProvider) { c.Next() }, playback.NextMsg{}},
		{"prev", func(c luaplugin.ControlProvider) { c.Prev() }, playback.PrevMsg{}},
		{"queue.add path", func(c luaplugin.ControlProvider) { c.QueueAdd("/a.mp3") }, model.PluginQueueMsg{Op: "add", Path: "/a.mp3"}},
		{
			"queue.add track",
			func(c luaplugin.ControlProvider) {
				c.QueueAddTrack(luaplugin.Track{Path: "/a.mp3", Title: "A", Artist: "B", Album: "C", Genre: "D", Year: 1999, Duration: 60, Stream: true})
			},
			model.PluginQueueMsg{Op: "add_track", Track: playlist.Track{Path: "/a.mp3", Title: "A", Artist: "B", Album: "C", Genre: "D", Year: 1999, DurationSecs: 60, Stream: true}},
		},
		{"queue.jump", func(c luaplugin.ControlProvider) { c.QueueJump(3) }, model.PluginQueueMsg{Op: "jump", Index: 3}},
		{"queue.remove", func(c luaplugin.ControlProvider) { c.QueueRemove(1) }, model.PluginQueueMsg{Op: "remove", Index: 1}},
		{"queue.move", func(c luaplugin.ControlProvider) { c.QueueMove(1, 4) }, model.PluginQueueMsg{Op: "move", Index: 1, To: 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []tea.Msg
			tt.call(luaControlProvider(func(msg tea.Msg) { got = append(got, msg) }))
			if want := []tea.Msg{tt.want}; !reflect.DeepEqual(got, want) {
				t.Fatalf("sent %#v, want %#v", got, want)
			}
		})
	}
}

// blockingSend is a send func that waits until release is closed, as
// prog.Send waits for the event loop. It records the messages in order.
type blockingSend struct {
	release chan struct{}
	got     chan tea.Msg
}

func newBlockingSend(n int) *blockingSend {
	return &blockingSend{release: make(chan struct{}), got: make(chan tea.Msg, n)}
}

func (b *blockingSend) send(msg tea.Msg) {
	<-b.release
	b.got <- msg
}

// A plugin calls a control or cliamp.message while it holds its lock. The
// call must return while the event loop is busy, and the Model must get the
// messages in the order that the plugin sent them.
func TestLuaSenderDoesNotBlockAndKeepsOrder(t *testing.T) {
	sink := newBlockingSend(3)
	queue, stop := newOrderedSender(sink.send)
	defer stop()
	ctrl := luaControlProvider(queue)
	ui := luaUIProvider(queue)

	done := make(chan struct{})
	go func() {
		ctrl.SetVolume(-20)
		ui.ShowMessage("hello", time.Second)
		ctrl.Next()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a Lua control waited for the event loop")
	}

	close(sink.release)
	want := []tea.Msg{
		playback.SetVolumeMsg{VolumeDB: -20},
		model.ShowStatusMsg{Text: "hello", Duration: time.Second},
		playback.NextMsg{},
	}
	for i, w := range want {
		select {
		case got := <-sink.got:
			if !reflect.DeepEqual(got, w) {
				t.Fatalf("message %d = %#v, want %#v", i, got, w)
			}
		case <-time.After(time.Second):
			t.Fatalf("message %d did not arrive", i)
		}
	}
}

// A plugin that adds a long list calls a control many times while the
// event loop is busy. Every call returns at once, and the Model gets every
// message in the order of the calls.
func TestLuaSenderKeepsEveryMessage(t *testing.T) {
	const n = 1000
	sink := newBlockingSend(n)
	queue, stop := newOrderedSender(sink.send)
	defer stop()

	done := make(chan struct{})
	go func() {
		for i := range n {
			queue(playback.SeekMsg{Offset: time.Duration(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a Lua control waited for the event loop")
	}

	close(sink.release)
	for i := range n {
		select {
		case msg := <-sink.got:
			if got := int(msg.(playback.SeekMsg).Offset); got != i {
				t.Fatalf("message %d = %d, want %d", i, got, i)
			}
		case <-time.After(time.Second):
			t.Fatalf("message %d did not arrive", i)
		}
	}
}

type fakeClock struct{ position, duration time.Duration }

func (c fakeClock) Position() time.Duration { return c.position }
func (c fakeClock) Duration() time.Duration { return c.duration }

// The Lua state reads the state that the Model published, and the engine
// clock for the position and the duration.
func TestLuaStateProviderReadsPublishedState(t *testing.T) {
	queue := []luaplugin.QueueEntry{{Track: luaplugin.Track{Title: "A"}}, {Track: luaplugin.Track{Title: "B"}, Index: 1, Queued: true}}
	published := model.PluginState{
		Status: "paused", Volume: -12, Speed: 1.25, Mono: true, Repeat: "All", Shuffle: true,
		EQBands: [10]float64{3}, Track: luaplugin.Track{Title: "B", Live: true},
		Count: 2, Index: 1, HasNext: true, Queue: func() []luaplugin.QueueEntry { return queue },
	}
	sp := luaStateProvider(fakeClock{position: 90 * time.Second, duration: 180 * time.Second},
		func() model.PluginState { return published })
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"state", sp.PlayerState(), "paused"},
		{"position", sp.Position(), 90.0},
		{"duration", sp.Duration(), 180.0},
		{"volume", sp.Volume(), -12.0},
		{"speed", sp.Speed(), 1.25},
		{"mono", sp.Mono(), true},
		{"repeat", sp.RepeatMode(), "All"},
		{"shuffle", sp.Shuffle(), true},
		{"eq bands", sp.EQBands(), [10]float64{3}},
		{"track", sp.CurrentTrack(), luaplugin.Track{Title: "B", Live: true}},
		{"count", sp.PlaylistCount(), 2},
		{"index", sp.CurrentIndex(), 1},
		{"has next", sp.HasNext(), true},
		{"queue", sp.QueueList(), queue},
	}
	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s = %#v, want %#v", tt.name, tt.got, tt.want)
		}
	}
	empty := luaStateProvider(fakeClock{}, func() model.PluginState { return model.PluginState{} })
	if got := empty.QueueList(); got != nil {
		t.Errorf("queue of a state with no Queue = %#v, want nil", got)
	}
}
