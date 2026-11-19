package main

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/ytdlbin"
)

func TestPinnedYtdlpMissingNotice(t *testing.T) {
	tests := []struct {
		name     string
		selected string
		want     bool
	}{
		{name: "default PATH lookup keeps the install flow", selected: ytdlbin.DefaultName},
		{name: "configured path is reported instead", selected: "/opt/yt-dlp", want: true},
		{name: "expanded home path is reported instead", selected: "/home/user/.local/bin/yt-dlp", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notice := pinnedYtdlpMissingNotice(tt.selected)
			if got := notice != ""; got != tt.want {
				t.Fatalf("pinnedYtdlpMissingNotice(%q) = %q, want notice = %v", tt.selected, notice, tt.want)
			}
			if !tt.want {
				return
			}
			if !strings.Contains(notice, tt.selected) {
				t.Errorf("notice = %q, want the selected binary", notice)
			}
			if !strings.Contains(notice, "ytdlp_path") || !strings.Contains(notice, ytdlbin.EnvVar) {
				t.Errorf("notice = %q, want both override mechanisms", notice)
			}
		})
	}
}
