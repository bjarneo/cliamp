//go:build windows

package player

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// listAudioDevicesScript enumerates installed sound cards. It is a compile-time
// constant with no interpolation, so no external value can reach the script.
const listAudioDevicesScript = `Get-CimInstance Win32_SoundDevice | ForEach-Object { $_.Name + '|' + $_.DeviceID }`

// ListAudioDevices lists audio output devices via PowerShell on Windows.
func ListAudioDevices() ([]AudioDevice, error) {
	out, err := exec.Command("powershell", "-NoProfile", "-Command", listAudioDevicesScript).Output()
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

// PrepareAudioDevice is a no-op on Windows — the system default output
// device is used. Returns a no-op cleanup.
func PrepareAudioDevice(device string) func() {
	return func() {}
}

// switchAudioDeviceScript switches the default playback device. The device ID
// travels via the CLIAMP_AUDIO_DEVICE env var — never interpolated into the
// script — so a device name can't inject PowerShell code. Double quotes are
// required: PowerShell only expands $env: inside double-quoted strings, and
// expansion inserts the value verbatim without re-parsing it.
const switchAudioDeviceScript = `Get-AudioDevice -PlaybackCommunication | Out-Null; ` +
	`Set-AudioDevice -ID "$env:CLIAMP_AUDIO_DEVICE" -ErrorAction Stop`

// SwitchAudioDevice changes the Windows system default output device.
// The running audio stream keeps its original device; the change
// takes full effect on the next app restart.
func SwitchAudioDevice(deviceName string) error {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", switchAudioDeviceScript)
	cmd.Env = append(os.Environ(), "CLIAMP_AUDIO_DEVICE="+deviceName)
	if out, err := cmd.CombinedOutput(); err != nil {
		// AudioDeviceCmdlets may not be installed; fall back to nircmd.
		nircmd := exec.Command("nircmd", "setdefaultsounddevice", deviceName)
		if out2, err2 := nircmd.CombinedOutput(); err2 != nil {
			return fmt.Errorf("failed to set output device (install AudioDeviceCmdlets or nircmd): %s / %s",
				strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	return nil
}
