package model

import (
	"strings"
	"testing"
)

func TestNetworkStatsText(t *testing.T) {
	engine := &playbackFakeEngine{}
	m := Model{player: engine, network: networkStats{speed: 512 * 1024}}
	engine.streamDownloaded = 4_456_243
	engine.streamTotal = 5_452_595
	got := m.networkStatsText()
	if !strings.Contains(got, "/5.2 MB") || !strings.HasSuffix(got, "↓512 KB/s") {
		t.Fatalf("active download = %q, want progress plus speed", got)
	}
	engine.streamDownloaded = engine.streamTotal
	if got := m.networkSpeedText(); got != "" {
		t.Fatalf("complete download speed = %q, want none", got)
	}
	if got := m.networkStatsText(); got == "" || strings.Contains(got, "KB/s") || strings.Contains(got, "MB/s") {
		t.Fatalf("complete download = %q, want progress only", got)
	}
}
