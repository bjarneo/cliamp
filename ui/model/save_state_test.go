package model

import (
	"errors"
	"testing"
)

func TestSaveStateActivityTextTracksPendingDownloads(t *testing.T) {
	var save saveState

	if got := save.activityText(); got != "" {
		t.Fatalf("activityText() = %q, want empty", got)
	}

	save.startDownload()
	if got := save.activityText(); got != "Downloading..." {
		t.Fatalf("activityText() after first start = %q, want %q", got, "Downloading...")
	}

	save.startDownload()
	if got := save.activityText(); got != "Downloading... (2)" {
		t.Fatalf("activityText() after second start = %q, want %q", got, "Downloading... (2)")
	}

	save.finishDownload()
	if got := save.activityText(); got != "Downloading..." {
		t.Fatalf("activityText() after first finish = %q, want %q", got, "Downloading...")
	}

	save.finishDownload()
	if got := save.activityText(); got != "" {
		t.Fatalf("activityText() after second finish = %q, want empty", got)
	}
}

// Only a yt-dlp save counts as a pending download, so only its result
// finishes one.
func TestTrackSavedMsgKeepsSaveActivityWhileDownloadsRemain(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name        string
		msg         trackSavedMsg
		wantPending int
		wantKind    feedbackKind
		wantStatus  string
	}{
		{name: "download saved", msg: trackSavedMsg{path: "/tmp/song.mp3", download: true}, wantPending: 1, wantKind: feedbackSuccess, wantStatus: "Saved to /tmp/song.mp3"},
		{name: "download failed", msg: trackSavedMsg{err: boom, download: true}, wantPending: 1, wantKind: feedbackError, wantStatus: "Download failed: boom"},
		{name: "copy saved", msg: trackSavedMsg{path: "/music/song.mp3"}, wantPending: 2, wantKind: feedbackSuccess, wantStatus: "Saved to /music/song.mp3"},
		{name: "copy failed", msg: trackSavedMsg{err: boom}, wantPending: 2, wantKind: feedbackError, wantStatus: "Save failed: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{save: saveState{pendingDownloads: 2}}

			nextModel, cmd := m.Update(tc.msg)
			if cmd != nil {
				t.Fatalf("Update() cmd = %v, want nil", cmd)
			}
			next, ok := nextModel.(Model)
			if !ok {
				t.Fatalf("Update() model = %T, want ui.Model", nextModel)
			}
			if next.save.pendingDownloads != tc.wantPending {
				t.Fatalf("pendingDownloads = %d, want %d", next.save.pendingDownloads, tc.wantPending)
			}
			if next.status.kind != tc.wantKind || next.status.text != tc.wantStatus {
				t.Fatalf("status = %v %q, want %v %q", next.status.kind, next.status.text, tc.wantKind, tc.wantStatus)
			}
		})
	}
}
