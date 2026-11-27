package model

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/theme"
)

func TestIPCIfRevisionZeroDetectsConflict(t *testing.T) {
	eng := &playbackFakeEngine{}
	p := playlist.New()
	p.Add(playlist.Track{Path: "a.mp3", Title: "A"}) // bumps revision from 0 -> 1
	m := Model{
		player:      eng,
		playlist:    p,
		configSaver: &recordingSaver{},
	}
	jobs := ipc.NewJobStore()
	defer jobs.CancelAll()
	job, err := jobs.Create("queue.clear")
	if err != nil {
		t.Fatal(err)
	}
	params := json.RawMessage(`{"if_revision":0}`)
	updated, _ := m.Update(V2RequestMsg{
		Request: ipc.V2Request{Operation: "queue.clear", Params: params},
		Jobs:    jobs,
		JobID:   job.ID,
	})
	m = updated.(Model)
	done, ok := jobs.Get(job.ID)
	if !ok || done.State != ipc.JobFailed || done.Error == nil || done.Error.Code != ipc.V2ErrorCodeConflict {
		t.Fatalf("job = %+v, want conflict failure for stale if_revision: 0", done)
	}
	if m.playlist.Len() != 1 {
		t.Fatalf("playlist len = %d after rejected if_revision: 0 clear, want 1", m.playlist.Len())
	}
}

func TestIPCProviderLoadEmptyStopsPlayback(t *testing.T) {
	eng := &playbackFakeEngine{playing: true}
	p := playlist.New()
	p.Add(playlist.Track{Path: "a.mp3", Title: "A"})
	p.SetIndex(0)
	m := Model{
		player:             eng,
		playlist:           p,
		ipcProviderLoadGen: 1,
	}
	startedPlaybackTrack(&m, p.Tracks()[0])
	jobs := ipc.NewJobStore()
	defer jobs.CancelAll()
	job, err := jobs.Create("provider.load")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Start(job.ID); err != nil {
		t.Fatal(err)
	}

	m.handleIPCProviderLoad(ipcProviderLoadResult{
		request: ipcLibraryRequest{Jobs: jobs, JobID: job.ID},
		gen:     1,
		tracks:  nil,
		start:   0,
	})
	if eng.playing {
		t.Fatal("handleIPCProviderLoad left old track playing after loading empty playlist")
	}
}

func TestIPCV2ThemeUnknownDoesNotCorruptActiveThemeIndex(t *testing.T) {
	m := Model{
		playlist: playlist.New(),
		themes: []theme.Theme{
			{Name: "CustomA"},
			{Name: "CustomB"},
		},
		themeIdx: 1,
	}
	jobs := ipc.NewJobStore()
	defer jobs.CancelAll()
	job, err := jobs.Create("theme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Start(job.ID); err != nil {
		t.Fatal(err)
	}

	m.handleV2Theme(jobs, job.ID, ipc.Request{Name: "NonExistentThemeXYZ"})
	if m.ThemeName() != "CustomB" {
		t.Fatalf("ThemeName() = %q after unknown theme request, want CustomB", m.ThemeName())
	}
}

type recordingLimitSearchProvider struct {
	gotLimit int
}

func (r *recordingLimitSearchProvider) Name() string                                { return "Lim" }
func (r *recordingLimitSearchProvider) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (r *recordingLimitSearchProvider) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (r *recordingLimitSearchProvider) SearchTracks(_ context.Context, _ string, limit int) ([]playlist.Track, error) {
	r.gotLimit = limit
	return nil, nil
}

func TestIPCProviderSearchDoesNotOverflowLargeOffset(t *testing.T) {
	prov := &recordingLimitSearchProvider{}
	m := Model{
		playlist:  playlist.New(),
		providers: []provider.Entry{{Key: "lim", Provider: prov}},
	}
	reply := make(chan ipc.Response, 1)
	cmd := m.handleIPCLibrary(ipcLibraryRequest{
		Op:       "provider.search",
		Provider: "lim",
		Query:    "test",
		Offset:   math.MaxInt,
		Limit:    25,
		Reply:    reply,
	})
	if cmd == nil {
		t.Fatal("handleIPCLibrary returned nil cmd")
	}
	cmd()
	resp := <-reply
	if !resp.OK {
		t.Fatalf("response = %+v, want OK", resp)
	}
	if prov.gotLimit != 100 {
		t.Fatalf("Search limit = %d, want 100 (no signed int overflow)", prov.gotLimit)
	}
}
