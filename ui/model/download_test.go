package model

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

type downloadTestProvider struct {
	commandsTestProvider
	got       playlist.Track
	directory string
	err       error
}

func (p *downloadTestProvider) CanDownload(track playlist.Track) bool {
	return track.Path == "test:track:1"
}
func (p *downloadTestProvider) DownloadTrack(ctx context.Context, track playlist.Track, dir string) (string, error) {
	p.got = track
	p.directory = dir
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return filepath.Join(dir, "song.mp3"), p.err
}

func TestSaveTrackCapability(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			p := &downloadTestProvider{}
			if failed {
				p.err = errors.New("unavailable")
			}
			tracks := playlist.New()
			track := playlist.Track{Path: "test:track:1", Stream: true}
			tracks.Add(track)
			dir := t.TempDir()
			m := Model{playlist: tracks, providers: []ProviderEntry{{Provider: p}}, downloadsDirectory: dir}
			cmd := m.saveTrack()
			if cmd == nil {
				t.Fatal("no command")
			}
			if m.saveTrack() != nil {
				t.Fatal("duplicate download accepted")
			}
			msg := cmd().(providerSavedMsg)
			if p.got.Path != track.Path || p.directory != dir {
				t.Fatal("wrong track or directory")
			}
			next, _ := m.Update(msg)
			result := next.(Model)
			if result.downloadCancel != nil || result.save.pendingDownloads != 0 {
				t.Fatal("download state not cleared")
			}
			if failed && msg.err == nil {
				t.Fatal("error lost")
			}
			if tracks.Len() != 1 {
				t.Fatal("playback playlist changed")
			}
		})
	}
}

func TestSaveTrackUnsupported(t *testing.T) {
	tracks := playlist.New()
	tracks.Add(playlist.Track{Path: "https://radio.example/live", Stream: true})
	m := Model{playlist: tracks}
	if m.saveTrack() != nil || m.status.text != "This provider does not support downloads" {
		t.Fatalf("status=%q", m.status.text)
	}
}

func TestSaveTrackCancellation(t *testing.T) {
	p := &downloadTestProvider{}
	tracks := playlist.New()
	tracks.Add(playlist.Track{Path: "test:track:1", Stream: true})
	m := Model{playlist: tracks, provider: p, downloadsDirectory: t.TempDir()}
	cmd := m.saveTrack()
	m.downloadCancel()
	msg := cmd().(providerSavedMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("err=%v", msg.err)
	}
}

func TestYTDLSaveCompletionDoesNotCancelProviderDownload(t *testing.T) {
	cancelled := false
	m := Model{downloadCancel: func() { cancelled = true }, save: saveState{pendingDownloads: 2}}
	next, _ := m.Update(ytdlSavedMsg{path: "/tmp/youtube.mp3"})
	result := next.(Model)
	if cancelled || result.downloadCancel == nil || result.save.pendingDownloads != 1 {
		t.Fatal("yt-dlp completion changed the active provider download")
	}
	next, _ = result.Update(providerSavedMsg{path: "/tmp/yandex.mp3"})
	result = next.(Model)
	if !cancelled || result.downloadCancel != nil || result.save.pendingDownloads != 0 {
		t.Fatal("provider completion did not clear its own download state")
	}
}
