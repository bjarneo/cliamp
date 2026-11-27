//go:build linux

package player

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFindCliampSinkInputPrioritizesExactPID(t *testing.T) {
	tests := []struct {
		name   string
		output string
		pid    string
		want   int
	}{
		{
			name: "exact PID wins over earlier cliamp binary match",
			output: `Sink Input #12
	application.name = "cliamp"
	application.process.binary = "cliamp"
	application.process.id = "111"
Sink Input #45
	application.name = "cliamp"
	application.process.binary = "cliamp"
	application.process.id = "222"
`,
			pid:  "222",
			want: 45,
		},
		{
			name: "falls back to binary name when PID does not match",
			output: `Sink Input #7
	application.name = "other"
	application.process.binary = "firefox"
	application.process.id = "111"
Sink Input #19
	application.name = "ALSA plug-in"
	application.process.binary = "cliamp"
	application.process.id = "333"
`,
			pid:  "222",
			want: 19,
		},
		{
			name: "falls back to application name when binary differs",
			output: `Sink Input #23
	application.name = "Cliamp Audio"
	application.process.binary = "go"
	application.process.id = "333"
`,
			pid:  "222",
			want: 23,
		},
		{
			name: "no matching sink input returns -1",
			output: `Sink Input #8
	application.name = "Firefox"
	application.process.binary = "firefox"
	application.process.id = "444"
`,
			pid:  "222",
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findCliampSinkInput(tt.output, tt.pid); got != tt.want {
				t.Fatalf("findCliampSinkInput() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSwitchAudioDeviceLinuxPassesDoubleDashBeforeDevice(t *testing.T) {
	myPID := strconv.Itoa(os.Getpid())
	tests := []struct {
		name       string
		sinkInputs string
		deviceName string
		wantArgs   string
	}{
		{
			name: "move-sink-input passes double dash before leading-dash device",
			sinkInputs: "Sink Input #3\n\tapplication.process.binary = \"cliamp\"\n\tapplication.process.id = \"999999\"\n" +
				"Sink Input #9\n\tapplication.process.id = \"" + myPID + "\"\n",
			deviceName: "-weird-sink",
			wantArgs:   "-- move-sink-input 9 -weird-sink",
		},
		{
			name:       "set-default-sink passes double dash before leading-dash device",
			sinkInputs: "Sink Input #1\n\tapplication.name = \"other\"\n\tapplication.process.id = \"999999\"\n",
			deviceName: "-default-sink",
			wantArgs:   "-- set-default-sink -default-sink",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, "pactl.log")
			inputsPath := filepath.Join(dir, "sink-inputs")
			if err := os.WriteFile(inputsPath, []byte(tt.sinkInputs), 0o600); err != nil {
				t.Fatal(err)
			}
			writeExecutable(t, filepath.Join(dir, "pactl"), `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "sink-inputs" ]; then
	cat "$TEST_SINK_INPUTS"
	exit 0
fi
printf '%s\n' "$*" > "$TEST_PACTL_LOG"
`)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TEST_SINK_INPUTS", inputsPath)
			t.Setenv("TEST_PACTL_LOG", logPath)

			if err := SwitchAudioDevice(tt.deviceName); err != nil {
				t.Fatalf("SwitchAudioDevice() error = %v", err)
			}
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read pactl log: %v", err)
			}
			if got := strings.TrimSpace(string(raw)); got != tt.wantArgs {
				t.Fatalf("pactl args = %q, want %q", got, tt.wantArgs)
			}
		})
	}
}

func TestListAudioDevicesSetsCLocaleForPactl(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "pactl"), `#!/bin/sh
if [ "$LC_ALL" != "C" ]; then
	echo "locale not C: $LC_ALL" >&2
	exit 1
fi
if [ "$1" = "get-default-sink" ]; then
	echo "alsa_output.default"
	exit 0
fi
cat <<'EOF'
Sink #1
	Name: alsa_output.default
	Description: Built-in Audio
EOF
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LC_ALL", "de_DE.UTF-8")

	devices, err := ListAudioDevices()
	if err != nil {
		t.Fatalf("ListAudioDevices() error = %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "alsa_output.default" || !devices[0].Active {
		t.Fatalf("ListAudioDevices() = %+v, want 1 active device", devices)
	}
}
