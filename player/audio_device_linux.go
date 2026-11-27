//go:build linux

package player

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const audioDeviceCmdTimeout = 5 * time.Second

func pactlCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "pactl", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	return cmd
}

// ListAudioDevices returns available output sinks via pactl.
// Works on PulseAudio and PipeWire.
func ListAudioDevices() ([]AudioDevice, error) {
	ctxDefault, cancelDefault := context.WithTimeout(context.Background(), audioDeviceCmdTimeout)
	defer cancelDefault()
	defaultSink := ""
	if out, err := pactlCommand(ctxDefault, "get-default-sink").Output(); err == nil {
		defaultSink = strings.TrimSpace(string(out))
	}

	ctxList, cancelList := context.WithTimeout(context.Background(), audioDeviceCmdTimeout)
	defer cancelList()
	out, err := pactlCommand(ctxList, "list", "sinks").Output()
	if err != nil {
		return nil, fmt.Errorf("pactl: %w (is PulseAudio/PipeWire running?)", err)
	}

	var devices []AudioDevice
	var cur *AudioDevice

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Sink #") {
			idx, _ := strconv.Atoi(strings.TrimPrefix(line, "Sink #"))
			devices = append(devices, AudioDevice{Index: idx})
			cur = &devices[len(devices)-1]
		} else if cur != nil {
			if key, val, ok := strings.Cut(line, ": "); ok {
				switch key {
				case "Name":
					cur.Name = val
					cur.Active = val == defaultSink
				case "Description":
					cur.Description = val
				}
			}
		}
	}

	return devices, nil
}

// PrepareAudioDevice sets PIPEWIRE_NODE so the PipeWire ALSA plugin
// routes this process's audio to the named device.
// Must be called before player.New(). Returns a cleanup that restores the env.
func PrepareAudioDevice(device string) func() {
	prev, hadPrev := os.LookupEnv("PIPEWIRE_NODE")
	os.Setenv("PIPEWIRE_NODE", device)
	return func() {
		if hadPrev {
			os.Setenv("PIPEWIRE_NODE", prev)
		} else {
			os.Unsetenv("PIPEWIRE_NODE")
		}
	}
}

// SwitchAudioDevice moves cliamp's audio stream to a different sink.
// It falls back to changing the system default sink when no stream matches.
func SwitchAudioDevice(deviceName string) error {
	ctxList, cancelList := context.WithTimeout(context.Background(), audioDeviceCmdTimeout)
	defer cancelList()
	out, err := pactlCommand(ctxList, "list", "sink-inputs").Output()
	if err != nil {
		return fmt.Errorf("pactl: %w", err)
	}

	sinkInputIdx := findCliampSinkInput(string(out), strconv.Itoa(os.Getpid()))
	if sinkInputIdx >= 0 {
		ctxMove, cancelMove := context.WithTimeout(context.Background(), audioDeviceCmdTimeout)
		defer cancelMove()
		cmd := pactlCommand(ctxMove, "--", "move-sink-input",
			strconv.Itoa(sinkInputIdx), deviceName)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("move-sink-input: %s (%w)", strings.TrimSpace(string(out)), err)
		}
		return nil
	}

	// Change the system default sink when no cliamp stream is active.
	ctxDefault, cancelDefault := context.WithTimeout(context.Background(), audioDeviceCmdTimeout)
	defer cancelDefault()
	cmd := pactlCommand(ctxDefault, "--", "set-default-sink", deviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("set-default-sink: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// findCliampSinkInput scans pactl sink-input output and prioritizes an exact
// PID match over a binary or application name fallback match.
func findCliampSinkInput(output, pidStr string) int {
	fallbackIdx := -1
	currentIdx := 0
	hasEntry := false
	props := map[string]string{}

	checkEntry := func() int {
		if !hasEntry {
			return -1
		}
		if props["application.process.id"] == pidStr {
			return currentIdx
		}
		if fallbackIdx < 0 && matchCliampFallback(props) {
			fallbackIdx = currentIdx
		}
		return -1
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Sink Input #") {
			if idx := checkEntry(); idx >= 0 {
				return idx
			}
			currentIdx, _ = strconv.Atoi(strings.TrimPrefix(line, "Sink Input #"))
			props = map[string]string{}
			hasEntry = true
			continue
		}
		if key, val, ok := strings.Cut(line, "="); ok {
			props[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"`)
		}
	}
	if idx := checkEntry(); idx >= 0 {
		return idx
	}
	return fallbackIdx
}

func matchCliampFallback(props map[string]string) bool {
	if strings.EqualFold(props["application.process.binary"], "cliamp") {
		return true
	}
	return strings.Contains(strings.ToLower(props["application.name"]), "cliamp")
}
