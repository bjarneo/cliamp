package model

import (
	"errors"
	"testing"
)

func TestShareCopiedMsgSuccess(t *testing.T) {
	m := Model{}
	link := "https://open.spotify.com/track/abc123"

	nextModel, cmd := m.Update(shareCopiedMsg{link: link})
	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() model = %T, want ui.Model", nextModel)
	}
	if got := next.status.text; got != "Link copied: "+link {
		t.Fatalf("status.text after shareCopiedMsg = %q, want %q", got, "Link copied: "+link)
	}
}

func TestShareCopiedMsgFailureKeepsLink(t *testing.T) {
	m := Model{}
	link := "https://open.spotify.com/track/abc123"

	nextModel, cmd := m.Update(shareCopiedMsg{link: link, err: errors.New("no clipboard backend found")})
	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() model = %T, want ui.Model", nextModel)
	}
	want := "Copy failed, link: " + link + " (no clipboard backend found)"
	if got := next.status.text; got != want {
		t.Fatalf("status.text after failed shareCopiedMsg = %q, want %q", got, want)
	}
}
