package model

import (
	"testing"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/resolve"
)

func TestInitYTDLBatchTrigger(t *testing.T) {
	tests := []struct {
		name string
		urls []string
		want string
	}{
		{name: "music playlist", urls: []string{"https://music.youtube.com/playlist?list=PL1"}, want: "https://music.youtube.com/playlist?list=PL1"},
		{name: "music host with www and upper case", urls: []string{"https://WWW.Music.YouTube.com/watch?v=a&list=PL1"}, want: "https://WWW.Music.YouTube.com/watch?v=a&list=PL1"},
		{name: "music host with m prefix", urls: []string{"https://m.music.youtube.com/watch?v=a&list=PL1"}, want: "https://m.music.youtube.com/watch?v=a&list=PL1"},
		{name: "youtube radio mix", urls: []string{"https://www.youtube.com/watch?v=a&list=RDa"}, want: "https://www.youtube.com/watch?v=a&list=RDa"},
		{name: "youtube plain playlist", urls: []string{"https://www.youtube.com/playlist?list=PL1"}},
		{name: "music url without list", urls: []string{"https://music.youtube.com/watch?v=a"}},
		{name: "other host", urls: []string{"https://example.com/music.youtube.com?list=PL1"}},
		{name: "first match wins", urls: []string{"https://youtube.com/playlist?list=PL1", "https://music.youtube.com/playlist?list=PL2", "https://youtube.com/watch?list=RDx"}, want: "https://music.youtube.com/playlist?list=PL2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Model
			cmd := m.initYTDLBatch(tt.urls)
			if (cmd != nil) != (tt.want != "") {
				t.Fatalf("cmd = %v, want trigger %v", cmd != nil, tt.want != "")
			}
			if m.ytdlBatch.url != tt.want {
				t.Fatalf("batch url = %q, want %q", m.ytdlBatch.url, tt.want)
			}
			if tt.want == "" {
				return
			}
			if !m.ytdlBatch.loading || m.ytdlBatch.offset != resolve.YTDLRadioInitialItems {
				t.Fatalf("batch state = %+v, want loading at offset %d", m.ytdlBatch, resolve.YTDLRadioInitialItems)
			}
		})
	}
}

// A replace of the queue ends the batch load of a YouTube radio playlist, so
// a batch that was in flight appends nothing to the new queue.
func TestQueueReplaceEndsTheRadioBatch(t *testing.T) {
	episode := playlist.Track{Path: "/music/e.mp3"}
	for _, tc := range []struct {
		name    string
		replace func(t *testing.T, m *Model)
		want    string
	}{
		{name: "IPC provider.load", want: "a b c", replace: func(t *testing.T, m *Model) {
			if response := runV2(t, m, "provider.load", ipc.Request{Provider: "local", Playlist: "Mix"}); !response.OK {
				t.Fatalf("provider.load = %+v", response)
			}
		}},
		{name: "IPC queue.clear", replace: func(t *testing.T, m *Model) {
			if response := runV2(t, m, "queue.clear", ipc.Request{}); !response.OK {
				t.Fatalf("queue.clear = %+v", response)
			}
		}},
		{name: "feed resolve", want: "e", replace: func(_ *testing.T, m *Model) {
			next, _ := m.Update(feedTrackResolvedMsg{tracks: []playlist.Track{episode}, gen: m.requests.stream, queue: m.requests.queue})
			*m = next.(Model)
		}},
		{name: "manager load", want: "a b c", replace: func(_ *testing.T, m *Model) {
			m.plManager = plManagerState{selPlaylist: "Mix", tracks: m.playlist.Tracks()}
			m.plMgrLoadAndPlay(0)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := queueOpModel(t, false, "Mix", 0)
			m.ytdlBatch = ytdlBatchState{gen: 7, url: "https://www.youtube.com/watch?v=a&list=RDa", offset: 25, loading: true}
			tc.replace(t, &m)
			loaded := m.loadedPlaylist
			batch := make([]playlist.Track, ytdlBatchSize)
			for i := range batch {
				batch[i] = playlist.Track{Path: "https://www.youtube.com/watch?v=r"}
			}
			next, cmd := m.Update(ytdlBatchMsg{gen: 7, tracks: batch})
			m = next.(Model)
			if got := queueOpPaths(m.playlist.Tracks()); got != tc.want {
				t.Fatalf("queue = %q, want %q", got, tc.want)
			}
			if m.loadedPlaylist != loaded || cmd != nil {
				t.Fatalf("loadedPlaylist = %q, want %q, next batch = %v", m.loadedPlaylist, loaded, cmd != nil)
			}
		})
	}
}
