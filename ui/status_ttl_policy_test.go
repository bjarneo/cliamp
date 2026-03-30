package ui

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
