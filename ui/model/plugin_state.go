package model

import (
	"sync"

	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
)

// PluginState is the playback state that Lua plugins read. The Model
// publishes a new PluginState before it sends a plugin event and at the end
// of each Update, so an event hook sees the change that its event reports.
// Track is the track that plays, as the plugin events and the media controls
// report it. The engine position is not in it, because it moves between
// Updates.
type PluginState struct {
	Status  string // "playing", "paused" or "stopped"
	Volume  float64
	Speed   float64
	Mono    bool
	Repeat  string
	Shuffle bool
	EQBands [10]float64
	Track   luaplugin.Track
	Count   int
	Index   int
	HasNext bool
	// Queue returns the playlist. The first call builds the rows on the
	// goroutine of the caller, from the playlist at that time, so an Update
	// does not copy a long playlist that no plugin reads. The states share
	// the rows until the playlist changes, so a caller must not change
	// them. A state from PluginStateLoader always has a Queue.
	Queue func() []luaplugin.QueueEntry

	revision uint64 // the playlist revision at publish
}

// PluginStateLoader returns a func that loads the state that the Model
// published last. With no plugin loaded, the func returns a stopped state.
// The func is safe to call from any goroutine. Every copy of the Model
// publishes to the store that it reads.
func (m *Model) PluginStateLoader() func() PluginState {
	store := m.pluginState
	return func() PluginState {
		if store != nil {
			if state := store.Load(); state != nil {
				return *state
			}
		}
		return PluginState{Status: "stopped", Speed: 1, Queue: func() []luaplugin.QueueEntry { return nil }}
	}
}

// publishPluginState stores the state that Lua plugins read. It makes a new
// Queue only when the playlist revision changed.
func (m *Model) publishPluginState() {
	if m.pluginState == nil || m.player == nil || m.playlist == nil {
		return
	}
	track, _ := m.currentPlaybackTrack()
	state := &PluginState{
		Status:   m.playerStatus(),
		Volume:   m.player.Volume(),
		Speed:    m.player.Speed(),
		Mono:     m.player.Mono(),
		Repeat:   m.playlist.Repeat().String(),
		Shuffle:  m.playlist.Shuffled(),
		EQBands:  m.player.EQBands(),
		Track:    pluginTrack(track),
		Count:    m.playlist.Len(),
		Index:    m.playlist.Index(),
		HasNext:  m.playlist.HasNext(),
		revision: m.playlist.Revision(),
	}
	state.Track.Artist, state.Track.Title = m.resolveTrackDisplay(track)
	state.Track.Live = m.currentPlaybackIsLive(track)
	if prev := m.pluginState.Load(); prev != nil && prev.revision == state.revision {
		state.Queue = prev.Queue
	} else {
		pl := m.playlist
		state.Queue = sync.OnceValue(func() []luaplugin.QueueEntry { return pluginQueue(pl) })
	}
	m.pluginState.Store(state)
}

// pluginQueue returns pl as cliamp.queue.list reports it.
func pluginQueue(pl *playlist.Playlist) []luaplugin.QueueEntry {
	tracks, playNext := pl.TracksAndQueue()
	queued := make(map[int]bool, len(playNext))
	for _, index := range playNext {
		queued[index] = true
	}
	queue := make([]luaplugin.QueueEntry, len(tracks))
	for i, track := range tracks {
		queue[i] = luaplugin.QueueEntry{Track: pluginTrack(track), Index: i, Queued: queued[i]}
	}
	return queue
}
