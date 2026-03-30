package model

import "testing"

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

func TestYTDLSavedMsgKeepsSaveActivityWhileDownloadsRemain(t *testing.T) {
	m := Model{
		save: saveState{
			pendingDownloads: 2,
		},
	}

	nextModel, cmd := m.Update(ytdlSavedMsg{path: "/tmp/song.mp3"})
	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}

	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() model = %T, want ui.Model", nextModel)
	}
	if next.save.pendingDownloads != 1 {
		t.Fatalf("pendingDownloads after ytdlSavedMsg = %d, want 1", next.save.pendingDownloads)
	}
	if got := next.save.activityText(); got != "Downloading..." {
		t.Fatalf("activityText() after ytdlSavedMsg = %q, want %q", got, "Downloading...")
	}
	if got := next.status.text; got != "Saved to /tmp/song.mp3" {
		t.Fatalf("status.text after ytdlSavedMsg = %q, want %q", got, "Saved to /tmp/song.mp3")
	}
}
