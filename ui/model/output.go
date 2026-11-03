package model

import (
	"context"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/player"
)

func (m Model) remoteOutput() bool {
	output, ok := m.player.(player.OutputSwitcher)
	return ok && output.RemoteOutput()
}

func outputDevices(engine player.Engine) ([]player.AudioDevice, error) {
	if output, ok := engine.(player.OutputSwitcher); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		return output.OutputDevices(ctx)
	}
	return player.ListAudioDevices()
}

func switchOutput(engine player.Engine, name, path string) error {
	if output, ok := engine.(player.OutputSwitcher); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return output.SwitchOutput(ctx, name, path)
	}
	return player.SwitchAudioDevice(name)
}

func (m *Model) saveNativeOutput(name string) {
	if name == "local" || strings.HasPrefix(name, "tidal:") {
		return
	}
	_ = m.saveConfigString("audio_device", name)
}

func (m *Model) remoteEffect() bool {
	if !m.remoteOutput() {
		return false
	}
	m.status.Warning("Local audio effects are unavailable on Tidal Connect", statusTTLDefault)
	return true
}
