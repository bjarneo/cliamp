// player/hwparams_linux.go — reads ALSA's own kernel-level view of an open
// PCM substream's negotiated hardware parameters, to see past ALSA's
// "plughw:" conversion layer (or any similar wrapper) to the rate the
// physical device is actually running at.
//
// snd_pcm_hw_params_get_rate() on a plughw-opened handle only ever reports
// back the logical (requested) rate: the plug layer accepts and silently
// converts a request the real hardware can't honor, and still reports
// success — exactly the failure mode bit-perfect mode exists to catch. So
// that API alone cannot prove exactness for anything but a raw "hw:" device,
// which has no conversion capability to hide behind.
// /proc/asound/cardN/pcmDp/subS/hw_params reflects the kernel's own
// negotiated state for the underlying hardware substream instead — a
// conversion layer sits between the app and this state, not inside it, so
// it can't hide from it.

//go:build linux && cgo

package player

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// alsaDeviceIndex extracts the device number from an ALSA device string
// ("hw:2,0" -> 0, "hw:CARD=PCH,DEV=1" -> 1). Defaults to 0 when no device
// field is present ("hw:2" -> 0), matching ALSA's own convention.
func alsaDeviceIndex(device string) int {
	rest, found := strings.CutPrefix(device, "hw:")
	if !found {
		rest, found = strings.CutPrefix(device, "plughw:")
	}
	if !found {
		return 0
	}
	_, field, found := strings.Cut(rest, ",")
	if !found {
		return 0
	}
	field = strings.TrimPrefix(field, "DEV=")
	n, err := strconv.Atoi(field)
	if err != nil {
		return 0
	}
	return n
}

// realALSARate reads the kernel's live negotiated rate for the playback
// substream at cardIdx/devIdx, subdevice 0 (true for every device this
// feature has been tested against; multi-subdevice hardware is rare for
// consumer/prosumer playback and simply falls through to ok=false below).
// Returns ok=false if the substream isn't open, the file can't be read, or
// the line can't be parsed — callers must fall back gracefully, never treat
// this as fatal.
func realALSARate(cardIdx, devIdx int) (rate int, ok bool) {
	return realALSARateFile(hwParamsPath(cardIdx, devIdx))
}

// hwParamsPath is the /proc path for a playback substream's negotiated
// kernel state, shared by realALSARate and realALSAFormatAndChannels — see
// realALSARate's doc comment for the subdevice-0 assumption.
func hwParamsPath(cardIdx, devIdx int) string {
	return fmt.Sprintf("/proc/asound/card%d/pcm%dp/sub0/hw_params", cardIdx, devIdx)
}

// realALSARateFile parses the "rate:" line out of an ALSA hw_params file
// (either the real /proc path or, in tests, a stand-in). Split out from
// realALSARate so the parsing itself is testable without a real /proc/asound
// layout.
func realALSARateFile(path string) (rate int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		rest, found := strings.CutPrefix(scanner.Text(), "rate:")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0, false
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// realALSAFormatAndChannels reads the kernel's live negotiated sample format
// and channel count for the same substream realALSARate reads the rate
// from — part of the same hw_params commit, so once realALSARateSettled has
// confirmed the rate, these are stable too and need no separate retry loop.
// A conversion layer (plughw:, a sound server) can silently convert the
// sample format or channel count exactly as it can the rate — accepting a
// request the hardware can't actually honor and reporting success anyway —
// so configureALSA's exactness check must confirm all three, not just the
// rate this file was originally written to check.
func realALSAFormatAndChannels(cardIdx, devIdx int) (format string, channels int, ok bool) {
	return realALSAFormatAndChannelsFile(hwParamsPath(cardIdx, devIdx))
}

// realALSAFormatAndChannelsFile is realALSAFormatAndChannels split out for
// testing, mirroring realALSARateFile/realALSARate.
func realALSAFormatAndChannelsFile(path string) (format string, channels int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if rest, found := strings.CutPrefix(line, "format:"); found {
			format = strings.TrimSpace(rest)
			continue
		}
		if rest, found := strings.CutPrefix(line, "channels:"); found {
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				continue
			}
			if n, err := strconv.Atoi(fields[0]); err == nil {
				channels = n
			}
		}
	}
	// Neither field is ever legitimately empty/zero in a real hw_params
	// file, so this doubles as "was the line present and parseable".
	if format == "" || channels == 0 {
		return "", 0, false
	}
	return format, channels, true
}

// realALSARateSettled retries realALSARate until two consecutive readings
// agree, since the kernel's negotiated state can briefly lag a request that
// was just applied — e.g. a USB audio device's clock taking a moment to
// actually relock during a rapid reopen. Bounded so a device that never
// stabilizes (or an environment without this /proc layout) still returns
// promptly with whatever it last saw.
func realALSARateSettled(cardIdx, devIdx int) (rate int, ok bool) {
	const attempts = 10
	const interval = 20 * time.Millisecond

	prev, prevOK := 0, false
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(interval)
		}
		cur, curOK := realALSARate(cardIdx, devIdx)
		if curOK && prevOK && cur == prev {
			return cur, true
		}
		prev, prevOK = cur, curOK
	}
	return prev, prevOK
}
