package model

import (
	"testing"
	"time"
)

func TestStatusShowAtSetsTextAndDeadline(t *testing.T) {
	var status statusMsg
	now := time.Date(2026, time.March, 28, 12, 0, 0, 0, time.UTC)

	status.ShowAt(now, "Saved", statusTTLMedium)

	if status.text != "Saved" {
		t.Fatalf("text = %q, want %q", status.text, "Saved")
	}
	if status.kind != feedbackSuccess {
		t.Fatalf("kind = %v, want %v", status.kind, feedbackSuccess)
	}
	want := now.Add(time.Duration(statusTTLMedium))
	if !status.expiresAt.Equal(want) {
		t.Fatalf("expiresAt = %v, want %v", status.expiresAt, want)
	}
}

func TestStatusExpiresAtDeadline(t *testing.T) {
	var status statusMsg
	now := time.Date(2026, time.March, 28, 12, 0, 0, 0, time.UTC)

	status.ShowAt(now, "Saved", statusTTLMedium)

	if status.Expired(now.Add(time.Duration(statusTTLMedium) - time.Nanosecond)) {
		t.Fatal("Expired() = true before deadline, want false")
	}
	if !status.Expired(now.Add(time.Duration(statusTTLMedium))) {
		t.Fatal("Expired() = false at deadline, want true")
	}
}

func TestStatusClearResetsMessage(t *testing.T) {
	var status statusMsg
	now := time.Date(2026, time.March, 28, 12, 0, 0, 0, time.UTC)

	status.ShowAt(now, "Saved", statusTTLMedium)
	status.Clear()

	if status.text != "" {
		t.Fatalf("text after Clear() = %q, want empty", status.text)
	}
	if !status.expiresAt.IsZero() {
		t.Fatalf("expiresAt after Clear() = %v, want zero", status.expiresAt)
	}
}

func TestStatusSemanticKinds(t *testing.T) {
	tests := []struct {
		name string
		show func(*statusMsg)
		want feedbackKind
	}{
		{name: "activity", show: func(s *statusMsg) { s.Activity("Loading...", statusTTLShort) }, want: feedbackActivity},
		{name: "warning", show: func(s *statusMsg) { s.Warning("Unavailable", statusTTLShort) }, want: feedbackWarning},
		{name: "error", show: func(s *statusMsg) { s.Error("Load failed", statusTTLShort) }, want: feedbackError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var status statusMsg
			tt.show(&status)
			if status.kind != tt.want {
				t.Fatalf("kind = %v, want %v", status.kind, tt.want)
			}
			if status.expiresAt.IsZero() {
				t.Fatal("expiresAt is zero, want timed feedback")
			}
		})
	}
}

func TestStatusErrorf(t *testing.T) {
	var status statusMsg

	status.Errorf(statusTTLShort, "Load failed: %s", "offline")
	if status.text != "Load failed: offline" {
		t.Fatalf("text = %q, want %q", status.text, "Load failed: offline")
	}
	if status.kind != feedbackError {
		t.Fatalf("kind = %v, want %v", status.kind, feedbackError)
	}
	if status.expiresAt.IsZero() {
		t.Fatal("expiresAt is zero, want timed error")
	}
}
