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
	pTrack := pluginTrack(track)
	pTrack.Artist, pTrack.Title = m.resolveTrackDisplay(track)
	// Providers resolve the cover on demand, so the playlist row may not have it.
	pTrack.ArtURL = m.artworkURL(track)
	pTrack.Live = m.currentPlaybackIsLive(track)

	meta := m.playlist.Meta()
	status := m.playerStatus()
	volume := m.player.Volume()
	speed := m.player.Speed()
	mono := m.player.Mono()
	repeat := meta.Repeat.String()
	shuffle := meta.Shuffled
	eqBands := m.player.EQBands()
	count := meta.Len
	index := meta.Index
	hasNext := meta.HasNext
	revision := meta.Revision

	prev := m.pluginState.Load()
	if prev != nil &&
		prev.Status == status &&
		prev.Volume == volume &&
		prev.Speed == speed &&
		prev.Mono == mono &&
		prev.Repeat == repeat &&
		prev.Shuffle == shuffle &&
		prev.EQBands == eqBands &&
		prev.Track == pTrack &&
		prev.Count == count &&
		prev.Index == index &&
		prev.HasNext == hasNext &&
		prev.revision == revision {
		return
	}

	state := &PluginState{
		Status:   status,
		Volume:   volume,
		Speed:    speed,
		Mono:     mono,
		Repeat:   repeat,
		Shuffle:  shuffle,
		EQBands:  eqBands,
		Track:    pTrack,
		Count:    count,
		Index:    index,
		HasNext:  hasNext,
		revision: revision,
	}
	if prev != nil && prev.revision == state.revision {
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
