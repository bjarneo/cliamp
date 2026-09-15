package model

import (
	"errors"
	"testing"
)

func TestShareCopiedMsg(t *testing.T) {
	link := "https://open.spotify.com/track/abc123"
	tests := []struct {
		name string
		msg  shareCopiedMsg
		want string
	}{
		{
			name: "success",
			msg:  shareCopiedMsg{link: link},
			want: "Link copied: " + link,
		},
		{
			name: "failure keeps link",
			msg:  shareCopiedMsg{link: link, err: errors.New("no clipboard backend found")},
			want: "Copy failed, link: " + link + " (no clipboard backend found)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{}
			nextModel, cmd := m.Update(tt.msg)
			if cmd != nil {
				t.Fatalf("Update() cmd = %v, want nil", cmd)
			}
			next, ok := nextModel.(Model)
			if !ok {
				t.Fatalf("Update() model = %T, want ui.Model", nextModel)
			}
			if got := next.status.text; got != tt.want {
				t.Fatalf("status.text = %q, want %q", got, tt.want)
			}
		})
	}
}
