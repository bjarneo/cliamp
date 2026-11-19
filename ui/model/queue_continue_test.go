package model

import (
	"errors"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// continueProvider continues after its batch's last track.
type continueProvider struct {
	commandsTestProvider
	last  string
	next  []playlist.Track
	err   error
	calls int
}

func (p *continueProvider) CanContinueAfter(track playlist.Track) bool { return track.Path == p.last }

func (p *continueProvider) ContinueAfter(playlist.Track) ([]playlist.Track, error) {
	p.calls++
	return p.next, p.err
}

func TestContinueQueue(t *testing.T) {
	batch := []playlist.Track{{Path: "plex:track:1"}, {Path: "plex:track:2"}}
	next := []playlist.Track{{Path: "plex:track:3"}, {Path: "plex:track:4"}}
	tests := []struct {
		name     string
		track    playlist.Track
		replace  []playlist.Track // queue after the request, nil keeps the batch
		err      error
		wantCmd  bool
		wantPath []string
	}{
		{name: "last track appends", track: batch[1], wantCmd: true,
			wantPath: []string{"plex:track:1", "plex:track:2", "plex:track:3", "plex:track:4"}},
		{name: "earlier track asks nothing", track: batch[0], wantPath: []string{"plex:track:1", "plex:track:2"}},
		{name: "replaced queue is kept", track: batch[1], wantCmd: true,
			replace: []playlist.Track{{Path: "/music/a.mp3"}}, wantPath: []string{"/music/a.mp3"}},
		{name: "error appends nothing", track: batch[1], err: errors.New("offline"), wantCmd: true,
			wantPath: []string{"plex:track:1", "plex:track:2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Replace(batch)
			m := New(&playbackFakeEngine{}, pl, nil, "", nil, nil, nil, nil, nil, nil)
			prov := &continueProvider{commandsTestProvider: commandsTestProvider{name: "Plex"}, last: batch[1].Path, next: next, err: tt.err}
			m.provider = prov

			cmd := m.continueQueueCmd(tt.track)
			if (cmd != nil) != tt.wantCmd {
				t.Fatalf("command = %v, want %v", cmd != nil, tt.wantCmd)
			}
			if cmd != nil {
				msg := cmd().(queueContinuedMsg)
				if tt.replace != nil {
					m.playlist.Replace(tt.replace)
				}
				m.handleQueueContinued(msg)
			}
			var got []string
			for _, track := range m.playlist.Tracks() {
				got = append(got, track.Path)
			}
			if len(got) != len(tt.wantPath) {
				t.Fatalf("queue = %v, want %v", got, tt.wantPath)
			}
			for i := range got {
				if got[i] != tt.wantPath[i] {
					t.Fatalf("queue = %v, want %v", got, tt.wantPath)
				}
			}
		})
	}
}
