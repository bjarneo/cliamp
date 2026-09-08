package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

const ipcPodcastRSS = `<rss><channel><title>Podcast</title>
<item><title>Zulu</title><guid>z</guid><enclosure url="https://example.com/z.mp3" type="audio/mpeg"/></item>
<item><title>Alpha</title><guid>a</guid><enclosure url="https://example.com/a.mp3" type="audio/mpeg"/></item>
</channel></rss>`

func ipcPodcastTestModel() (Model, *playbackFakeEngine) {
	engine := &playbackFakeEngine{playing: true}
	pl := playlist.New()
	pl.Add(
		playlist.Track{Path: "/music/current.flac", Title: "Current"},
		playlist.Track{Path: "/music/one.flac", Title: "One"},
		playlist.Track{Path: "/music/two.flac", Title: "Two"},
	)
	pl.Queue(2)
	pl.Queue(1)
	return Model{player: engine, playlist: pl, loadedPlaylist: "Saved"}, engine
}

func TestIPCPodcastTrackActions(t *testing.T) {
	tests := []struct {
		name, body, wantError string
		status                int
	}{
		{name: "episodes", body: ipcPodcastRSS},
		{name: "empty", body: `<rss><channel><item><title>No audio</title></item></channel></rss>`, wantError: "no playable episodes"},
		{name: "malformed after episodes", body: strings.TrimSuffix(ipcPodcastRSS, "</channel></rss>"), wantError: "parsing feed"},
		{name: "not RSS", body: `<html>Not a feed</html>`, wantError: "parsing feed"},
		{name: "HTTP error", status: http.StatusBadGateway, wantError: "502"},
	}
	for _, transport := range []string{"legacy", "v2"} {
		for _, op := range []string{"track.queue", "track.play"} {
			for _, tt := range tests {
				t.Run(transport+"/"+op+"/"+tt.name, func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet || r.URL.Path != "/show" {
							t.Errorf("feed request = %s %s, want GET /show", r.Method, r.URL.Path)
						}
						if tt.status != 0 {
							w.WriteHeader(tt.status)
						}
						io.WriteString(w, tt.body)
					}))
					defer srv.Close()
					feedURL := srv.URL + "/show"
					info := ipcTrackInfo(playlist.Track{
						Path: feedURL, Title: "Podcast", Feed: true,
						ProviderMeta: map[string]string{"kind": "album", "albumID": feedURL},
					}, 0, 0)
					params, err := json.Marshal(ipc.Request{Track: &info})
					if err != nil {
						t.Fatal(err)
					}
					m, engine := ipcPodcastTestModel()
					before, revision, engineBefore := m.playlist.Snapshot(), m.playlist.Revision(), *engine
					original := m.playlist.Tracks()
					queued := m.playlist.QueueTracks()
					var cmd tea.Cmd
					var jobs *ipc.JobStore
					var job ipc.Job
					if transport == "legacy" {
						var request ipc.Request
						if err := json.Unmarshal(params, &request); err != nil {
							t.Fatal(err)
						}
						reply := make(chan ipc.Response, 1)
						updated, command := m.Update(ipc.QueueRequestMsg{Op: op, Track: request.Track, Reply: reply})
						m, cmd = updated.(Model), command
						if len(reply) != 0 {
							t.Fatal("replied before resolving the feed")
						}
					} else {
						jobs = ipc.NewJobStore()
						defer jobs.CancelAll()
						job, err = jobs.Create(op)
						if err != nil {
							t.Fatal(err)
						}
						updated, command := m.Update(V2RequestMsg{
							Request: ipc.V2Request{Operation: op, Params: params}, Jobs: jobs, JobID: job.ID,
						})
						m, cmd = updated.(Model), command
					}
					if cmd == nil {
						t.Fatal("feed action did not return an async command")
					}
					message := cmd()
					result, ok := message.(ipcFeedLoadResult)
					if !ok {
						t.Fatalf("command result = %T, want ipcFeedLoadResult", message)
					}
					if len(result.request.Reply) != 0 {
						t.Fatal("replied before applying the feed result")
					}
					if jobs != nil {
						pending, _ := jobs.Get(job.ID)
						if pending.State != ipc.JobRunning {
							t.Fatalf("job completed before expansion: %+v", pending)
						}
					}
					if !reflect.DeepEqual(m.playlist.Snapshot(), before) || m.playlist.Revision() != revision || !reflect.DeepEqual(*engine, engineBefore) {
						t.Fatal("feed command mutated playback before its result was applied")
					}
					updated, playbackCmd := m.Update(result)
					m = updated.(Model)
					var response ipc.Response
					if jobs == nil {
						if len(result.request.Reply) != 1 {
							t.Fatal("feed result did not send exactly one reply")
						}
						response = <-result.request.Reply
					} else {
						if result.request.Reply != nil {
							t.Fatal("V2 feed result uses a deferred reply channel")
						}
						completed, _ := jobs.Get(job.ID)
						wantState := ipc.JobSucceeded
						if tt.wantError != "" {
							wantState = ipc.JobFailed
							if completed.Error == nil || !strings.Contains(completed.Error.Detail, tt.wantError) {
								t.Fatalf("job error = %+v, want %q", completed.Error, tt.wantError)
							}
							response.Error = completed.Error.Detail
						}
						if completed.State != wantState {
							t.Fatalf("job state = %s, want %s", completed.State, wantState)
						}
						if wantState == ipc.JobSucceeded {
							if err := json.Unmarshal(completed.Result, &response); err != nil {
								t.Fatal(err)
							}
						}
					}
					if tt.wantError != "" {
						if response.OK || !strings.Contains(response.Error, tt.wantError) {
							t.Fatalf("response = %+v, want error containing %q", response, tt.wantError)
						}
						if playbackCmd != nil || !reflect.DeepEqual(m.playlist.Snapshot(), before) || m.playlist.Revision() != revision || !reflect.DeepEqual(*engine, engineBefore) || m.loadedPlaylist != "Saved" {
							t.Fatal("invalid feed changed the playlist, queue, or playback")
						}
						return
					}
					if !response.OK || response.Error != "" || response.Total != len(original)+2 || len(response.Tracks) != response.Total {
						t.Fatalf("response = %+v, want expanded playlist", response)
					}
					wantEpisodes := []playlist.Track{
						{Path: "https://example.com/z.mp3", Title: "Zulu", Artist: "Podcast", Album: "Podcast", Stream: true, ProviderMeta: map[string]string{"podcast.feed": feedURL, "podcast.guid": "z"}},
						{Path: "https://example.com/a.mp3", Title: "Alpha", Artist: "Podcast", Album: "Podcast", Stream: true, ProviderMeta: map[string]string{"podcast.feed": feedURL, "podcast.guid": "a"}},
					}
					wantTracks := append(original, wantEpisodes...)
					wantIndex := 0
					if op == "track.queue" {
						queued = append(queued, wantEpisodes...)
					} else {
						wantIndex = len(original)
					}
					if !reflect.DeepEqual(m.playlist.Tracks(), wantTracks) || !reflect.DeepEqual(m.playlist.QueueTracks(), queued) || m.playlist.Index() != wantIndex {
						t.Fatalf("playlist = %+v, queue = %+v, index = %d; want original tracks/queue plus feed-order episodes at index %d", m.playlist.Tracks(), m.playlist.QueueTracks(), m.playlist.Index(), wantIndex)
					}
					for _, track := range response.Tracks {
						if track.Feed || track.Path == feedURL || track.ProviderMeta["kind"] == "album" {
							t.Fatalf("reply still contains a feed placeholder: %+v", track)
						}
					}
					if playbackCmd != nil {
						message := playbackCmd()
						if _, ok := message.(feedTrackResolvedMsg); ok {
							t.Fatal("playback invoked the legacy playlist-replacing feed resolver")
						}
						updated, _ = m.Update(message)
						m = updated.(Model)
					}
					if op == "track.play" && !reflect.DeepEqual(engine.playCalls, []string{wantEpisodes[0].Path}) {
						t.Fatalf("played = %v, want first episode", engine.playCalls)
					}
					if m.feedLoading || !reflect.DeepEqual(m.playlist.Tracks(), wantTracks) || !reflect.DeepEqual(m.playlist.QueueTracks(), queued) {
						t.Fatal("episode playback changed the existing playlist or queue")
					}
					if op == "track.queue" {
						for _, track := range queued {
							if next := m.nextTrack(); next != nil {
								updated, _ = m.Update(next())
								m = updated.(Model)
							}
							if len(engine.playCalls) == 0 || engine.playCalls[len(engine.playCalls)-1] != track.Path {
								t.Fatalf("played = %v, want next queued track %q", engine.playCalls, track.Path)
							}
						}
						if m.feedLoading || !reflect.DeepEqual(m.playlist.Tracks(), wantTracks) || m.playlist.QueueLen() != 0 {
							t.Fatal("playing queued episodes replaced the playlist or lost queue order")
						}
					}
				})
			}
		}
	}
}

func TestIPCPodcastV2FeedCommit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, ipcPodcastRSS)
	}))
	defer srv.Close()
	for _, op := range []string{"track.queue", "track.play"} {
		for _, tt := range []struct {
			name, change                string
			checkRevision, wantConflict bool
		}{
			{name: "matching revision", checkRevision: true},
			{name: "playlist conflict", change: "playlist", checkRevision: true, wantConflict: true},
			{name: "queue conflict", change: "queue", checkRevision: true, wantConflict: true},
			{name: "unconditional append", change: "playlist"},
		} {
			t.Run(op+"/"+tt.name, func(t *testing.T) {
				m, engine := ipcPodcastTestModel()
				broker := ipc.NewBroker()
				defer broker.Close()
				m.SetIPCBroker(broker)
				info := ipc.TrackInfo{Path: srv.URL, Feed: true, ProviderMeta: map[string]string{"kind": "album", "albumID": srv.URL}}
				request := ipc.Request{Track: &info}
				if tt.checkRevision {
					request.Revision = m.playlist.Revision()
				}
				params, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				jobs := ipc.NewJobStore()
				defer jobs.CancelAll()
				job, err := jobs.Create(op)
				if err != nil {
					t.Fatal(err)
				}
				updated, cmd := m.Update(V2RequestMsg{Request: ipc.V2Request{Operation: op, Params: params}, Jobs: jobs, JobID: job.ID})
				m = updated.(Model)
				if cmd == nil {
					t.Fatal("feed action did not return an async command")
				}
				message := cmd()
				result, ok := message.(ipcFeedLoadResult)
				if !ok || result.err != nil || len(result.tracks) != 2 {
					t.Fatalf("feed result = %T %+v, want resolved episodes", message, message)
				}
				switch tt.change {
				case "playlist":
					m.playlist.Add(playlist.Track{Path: "/music/added.flac", Title: "Added while fetching"})
				case "queue":
					m.playlist.Queue(0)
				}
				before, revision, engineBefore := m.playlist.Snapshot(), m.playlist.Revision(), *engine
				tracks := m.playlist.Tracks()
				updated, cmd = m.Update(result)
				m = updated.(Model)
				completed, _ := jobs.Get(job.ID)
				if tt.wantConflict {
					if completed.State != ipc.JobFailed || completed.Error == nil || completed.Error.Code != ipc.V2ErrorCodeConflict {
						t.Fatalf("job = %+v, want revision conflict", completed)
					}
					if completed.Snapshot != nil || len(completed.Result) != 0 {
						t.Fatalf("conflicting job has a committed result: %+v", completed)
					}
					if cmd != nil || !reflect.DeepEqual(m.playlist.Snapshot(), before) || m.playlist.Revision() != revision || !reflect.DeepEqual(*engine, engineBefore) || m.loadedPlaylist != "Saved" {
						t.Fatal("stale feed result changed the playlist, queue, or playback")
					}
					return
				}
				if completed.State != ipc.JobSucceeded || completed.Error != nil {
					t.Fatalf("job = %+v, want success in the feed result update", completed)
				}
				if !reflect.DeepEqual(m.playlist.Tracks(), append(tracks, result.tracks...)) {
					t.Fatal("feed commit did not preserve the current playlist")
				}
				if completed.Snapshot == nil || completed.Snapshot.Revision == 0 || !reflect.DeepEqual(*completed.Snapshot, m.runtimeSnapshot()) {
					t.Fatalf("job snapshot = %+v, want commit snapshot %+v", completed.Snapshot, m.runtimeSnapshot())
				}
				var response ipc.Response
				if err := json.Unmarshal(completed.Result, &response); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(response, m.v2PlaylistResponse()) {
					t.Fatalf("job result = %+v, want playlist at commit", response)
				}
				committed := m.playlist.Snapshot()
				if err := jobs.Cancel(job.ID); !errors.Is(err, ipc.ErrInvalidJobState) {
					t.Fatalf("Cancel(committed feed) = %v, want invalid job state", err)
				}
				afterCancel, _ := jobs.Get(job.ID)
				if !reflect.DeepEqual(afterCancel, completed) || !reflect.DeepEqual(m.playlist.Snapshot(), committed) {
					t.Fatal("late cancellation changed the committed feed job or playlist")
				}
				nextJob, err := jobs.Create("queue.clear")
				if err != nil {
					t.Fatal(err)
				}
				updated, _ = m.Update(V2RequestMsg{Request: ipc.V2Request{Operation: "queue.clear"}, Jobs: jobs, JobID: nextJob.ID})
				m = updated.(Model)
				nextCompleted, _ := jobs.Get(nextJob.ID)
				if nextCompleted.State != ipc.JobSucceeded || m.playlist.Len() != 0 || m.runtimeSnapshot().Revision <= completed.Snapshot.Revision {
					t.Fatal("next operation did not change the live state")
				}
				afterMutation, _ := jobs.Get(job.ID)
				if !reflect.DeepEqual(afterMutation, completed) {
					t.Fatal("feed job result or snapshot belongs to a later operation")
				}
				updated, cmd = m.Update(result)
				m = updated.(Model)
				if cmd != nil || m.playlist.Len() != 0 {
					t.Fatal("a completed job's feed result was committed again")
				}
			})
		}
	}
}

func TestIPCPodcastCanceledJob(t *testing.T) {
	for _, op := range []string{"track.queue", "track.play"} {
		for _, stage := range []string{"before dispatch", "before fetch", "during fetch", "after resolution"} {
			t.Run(op+"/"+stage, func(t *testing.T) {
				started, requestCanceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					close(started)
					if stage == "during fetch" {
						select {
						case <-r.Context().Done():
							close(requestCanceled)
							return
						case <-release:
						}
					}
					io.WriteString(w, ipcPodcastRSS)
				}))
				defer srv.Close()
				defer close(release)
				info := ipc.TrackInfo{Path: srv.URL + "/show", Feed: true, ProviderMeta: map[string]string{"kind": "album", "albumID": srv.URL + "/show"}}
				params, err := json.Marshal(ipc.Request{Track: &info})
				if err != nil {
					t.Fatal(err)
				}
				jobs := ipc.NewJobStore()
				defer jobs.CancelAll()
				job, err := jobs.Create(op)
				if err != nil {
					t.Fatal(err)
				}
				cancel := func() {
					t.Helper()
					if err := jobs.Cancel(job.ID); err != nil {
						t.Fatal(err)
					}
				}
				m, engine := ipcPodcastTestModel()
				before, revision, engineBefore := m.playlist.Snapshot(), m.playlist.Revision(), *engine
				if stage == "before dispatch" {
					cancel()
				}
				updated, cmd := m.Update(V2RequestMsg{Request: ipc.V2Request{Operation: op, Params: params}, Jobs: jobs, JobID: job.ID})
				m = updated.(Model)
				if stage == "before dispatch" {
					if cmd != nil {
						t.Fatal("canceled job returned a command")
					}
				} else {
					if cmd == nil {
						t.Fatal("feed action did not return an async command")
					}
					if stage == "before fetch" {
						cancel()
					}
					finished := make(chan tea.Msg, 1)
					go func() { finished <- cmd() }()
					if stage == "during fetch" {
						select {
						case <-started:
						case <-time.After(5 * time.Second):
							t.Fatal("feed request did not start")
						}
						cancel()
						select {
						case <-requestCanceled:
						case <-time.After(5 * time.Second):
							t.Fatal("job cancellation did not cancel the HTTP request")
						}
					}
					var result ipcFeedLoadResult
					select {
					case message := <-finished:
						result = message.(ipcFeedLoadResult)
					case <-time.After(5 * time.Second):
						t.Fatal("feed resolution did not finish")
					}
					if stage == "after resolution" {
						if result.err != nil || len(result.tracks) != 2 {
							t.Fatalf("resolution = %+v, want valid episodes before canceling", result)
						}
						cancel()
					} else if !errors.Is(result.err, context.Canceled) {
						t.Fatalf("resolution error = %v, want context.Canceled", result.err)
					}
					updated, cmd = m.Update(result)
					m = updated.(Model)
					if cmd != nil {
						t.Fatal("canceled feed returned a playback command")
					}
				}
				completed, _ := jobs.Get(job.ID)
				if completed.State != ipc.JobCanceled || completed.Error == nil || completed.Error.Code != ipc.V2ErrorCodeCanceled {
					t.Fatalf("job = %+v, want canceled", completed)
				}
				if !reflect.DeepEqual(m.playlist.Snapshot(), before) || m.playlist.Revision() != revision || !reflect.DeepEqual(*engine, engineBefore) || m.loadedPlaylist != "Saved" {
					t.Fatal("canceled feed changed the playlist, queue, or playback")
				}
			})
		}
	}
}

func TestIPCPodcastResolutionContextErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("expired context made an HTTP request")
		io.WriteString(w, ipcPodcastRSS)
	}))
	defer srv.Close()
	for _, wantErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(wantErr.Error(), func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if wantErr == context.Canceled {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			} else {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			defer cancel()
			m, engine := ipcPodcastTestModel()
			before, engineBefore := m.playlist.Snapshot(), *engine
			reply := make(chan ipc.Response, 1)
			result := ipcFeedLoadCmd(ctx, ipc.QueueRequestMsg{Op: "track.play", Reply: reply}, playlist.Track{Path: srv.URL, Feed: true}, nil, "", 0)().(ipcFeedLoadResult)
			if !errors.Is(result.err, wantErr) {
				t.Fatalf("resolution error = %v, want %v", result.err, wantErr)
			}
			updated, cmd := m.Update(result)
			m = updated.(Model)
			select {
			case response := <-reply:
				if response.OK || !strings.Contains(response.Error, wantErr.Error()) {
					t.Fatalf("response = %+v, want %v", response, wantErr)
				}
			default:
				t.Fatal("missing context error reply")
			}
			if cmd != nil || !reflect.DeepEqual(m.playlist.Snapshot(), before) || !reflect.DeepEqual(*engine, engineBefore) {
				t.Fatal("context error mutated playback")
			}
		})
	}
}

func TestIPCTrackActionsNonFeed(t *testing.T) {
	for _, transport := range []string{"legacy", "v2"} {
		for _, op := range []string{"track.queue", "track.play"} {
			for _, info := range []ipc.TrackInfo{
				{Title: "Single track", Path: "/music/new.flac"},
				{Title: "Episode", Path: "https://example.com/episode.mp3", ProviderMeta: map[string]string{"podcast.feed": "https://example.com/feed"}},
				{Title: "Non-feed album", Path: "https://example.com/album", ProviderMeta: map[string]string{"kind": "album", "albumID": "album-id"}},
			} {
				t.Run(transport+"/"+op+"/"+info.Title, func(t *testing.T) {
					m, _ := ipcPodcastTestModel()
					original, queued := m.playlist.Tracks(), m.playlist.QueueTracks()
					if transport == "legacy" {
						reply := make(chan ipc.Response, 1)
						updated, _ := m.Update(ipc.QueueRequestMsg{Op: op, Track: &info, Reply: reply})
						m = updated.(Model)
						select {
						case response := <-reply:
							if !response.OK {
								t.Fatalf("response = %+v", response)
							}
						default:
							t.Fatal("non-feed action did not reply immediately")
						}
					} else {
						jobs := ipc.NewJobStore()
						defer jobs.CancelAll()
						job, err := jobs.Create(op)
						if err != nil {
							t.Fatal(err)
						}
						params, err := json.Marshal(ipc.Request{Track: &info})
						if err != nil {
							t.Fatal(err)
						}
						updated, _ := m.Update(V2RequestMsg{Request: ipc.V2Request{Operation: op, Params: params}, Jobs: jobs, JobID: job.ID})
						m = updated.(Model)
						completed, _ := jobs.Get(job.ID)
						if completed.State != ipc.JobSucceeded {
							t.Fatalf("job = %+v, want immediate success", completed)
						}
					}
					track := ipcTrackFromInfo(info)
					wantIndex := len(original)
					if op == "track.queue" {
						queued = append(queued, track)
						wantIndex = 0
					} else {
						// track.play fills the "play now" slot, which the next
						// play-now (or autoplay cleanup) may reclaim.
						track.Ephemeral = true
					}
					if !reflect.DeepEqual(m.playlist.Tracks(), append(original, track)) || !reflect.DeepEqual(m.playlist.QueueTracks(), queued) || m.playlist.Index() != wantIndex {
						t.Fatal("non-feed single-track behavior changed")
					}
				})
			}
		}
	}
}
