//go:build windows

package player

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	audioDeviceCmdTimeout   = 5 * time.Second
	listAudioDevicesScript  = `Get-CimInstance Win32_SoundDevice | ForEach-Object { $_.Name + '|' + $_.DeviceID }`
	switchAudioDeviceScript = `Get-AudioDevice -PlaybackCommunication | Out-Null; Set-AudioDevice -ID $env:CLIAMP_DEVICE -ErrorAction Stop`
)

// ListAudioDevices lists audio output devices via PowerShell on Windows.
func ListAudioDevices() ([]AudioDevice, error) {
	ctx, cancel := context.WithTimeout(context.Background(), audioDeviceCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", listAudioDevicesScript).Output()
	if err != nil {
		return nil, fmt.Errorf("powershell: %w", err)
	}

	var devices []AudioDevice
	for i, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, id, _ := strings.Cut(line, "|")
		devices = append(devices, AudioDevice{
			Index:       i,
			Name:        strings.TrimSpace(id),
			Description: strings.TrimSpace(name),
			Active:      false,
		})
	}
	return devices, nil
}

// PrepareAudioDevice is a no-op on Windows. It returns a no-op cleanup.
func PrepareAudioDevice(device string) func() {
	return func() {}
}

// SwitchAudioDevice changes the Windows system default output device.
// The running audio stream keeps its original device until restart.
func SwitchAudioDevice(deviceName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), audioDeviceCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", switchAudioDeviceScript)
	cmd.Env = append(os.Environ(), "CLIAMP_DEVICE="+deviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fall back to nircmd when AudioDeviceCmdlets is not installed.
		ctx2, cancel2 := context.WithTimeout(context.Background(), audioDeviceCmdTimeout)
		defer cancel2()
		fallback := exec.CommandContext(ctx2, "nircmd", "setdefaultsounddevice", deviceName)
		if out2, err2 := fallback.CombinedOutput(); err2 != nil {
			return fmt.Errorf("failed to set output device (install AudioDeviceCmdlets or nircmd): %s / %s",
				strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	return nil
}
