// player/audio_device_macos.go — macOS Core Audio output device enumeration & selection.

//go:build darwin && !ios

package player

/*
#cgo LDFLAGS: -framework CoreAudio -framework CoreFoundation
#include <CoreAudio/CoreAudio.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

static AudioDeviceID caDefaultOutput() {
	AudioObjectPropertyAddress addr = {
		kAudioHardwarePropertyDefaultOutputDevice,
		kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain,
	};
	AudioDeviceID id = kAudioObjectUnknown;
	UInt32 sz = sizeof(id);
	AudioObjectGetPropertyData(kAudioObjectSystemObject, &addr, 0, NULL, &sz, &id);
	return id;
}

static int caSetDefaultOutput(AudioDeviceID id) {
	AudioObjectPropertyAddress addr = {
		kAudioHardwarePropertyDefaultOutputDevice,
		kAudioObjectPropertyScopeGlobal,
		kAudioObjectPropertyElementMain,
	};
	return AudioObjectSetPropertyData(kAudioObjectSystemObject, &addr, 0, NULL, sizeof(id), &id) == noErr ? 0 : -1;
}

static int caOutputChannels(AudioDeviceID id) {
	AudioObjectPropertyAddress addr = {
		kAudioDevicePropertyStreamConfiguration,
		kAudioObjectPropertyScopeOutput,
		kAudioObjectPropertyElementMain,
	};
	UInt32 sz = 0;
	if (AudioObjectGetPropertyDataSize(id, &addr, 0, NULL, &sz) != noErr || sz < sizeof(UInt32)) return 0;
	AudioBufferList *bl = (AudioBufferList *)malloc(sz);
	if (bl == NULL) return 0;
	if (AudioObjectGetPropertyData(id, &addr, 0, NULL, &sz, bl) != noErr || sz < sizeof(UInt32)) {
		free(bl);
		return 0;
	}
	int ch = 0;
	for (UInt32 i = 0; i < bl->mNumberBuffers; i++) ch += bl->mBuffers[i].mNumberChannels;
	free(bl);
	return ch;
}

static char* caStr(CFStringRef s) {
	if (s == NULL) return NULL;
	CFIndex len = CFStringGetMaximumSizeForEncoding(CFStringGetLength(s), kCFStringEncodingUTF8) + 1;
	char *buf = (char*)malloc((size_t)len);
	if (buf == NULL) {
		CFRelease(s);
		return NULL;
	}
	if (!CFStringGetCString(s, buf, len, kCFStringEncodingUTF8)) {
		free(buf);
		CFRelease(s);
		return NULL;
	}
	CFRelease(s);
	return buf;
}

static char* caDevName(AudioDeviceID id) {
	AudioObjectPropertyAddress a = {kAudioObjectPropertyName, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain};
	CFStringRef s = NULL; UInt32 sz = sizeof(s);
	if (AudioObjectGetPropertyData(id, &a, 0, NULL, &sz, &s) != noErr) return NULL;
	return caStr(s);
}

static char* caDevUID(AudioDeviceID id) {
	AudioObjectPropertyAddress a = {kAudioDevicePropertyDeviceUID, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain};
	CFStringRef s = NULL; UInt32 sz = sizeof(s);
	if (AudioObjectGetPropertyData(id, &a, 0, NULL, &sz, &s) != noErr) return NULL;
	return caStr(s);
}

typedef struct { AudioDeviceID id; char *name; char *uid; } CADev;

static int caListOutputs(CADev **out, int *count) {
	AudioObjectPropertyAddress a = {kAudioHardwarePropertyDevices, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain};
	UInt32 sz = 0;
	*out = NULL;
	*count = 0;
	if (AudioObjectGetPropertyDataSize(kAudioObjectSystemObject, &a, 0, NULL, &sz) != noErr) {
		return -1;
	}
	if (sz < sizeof(AudioDeviceID)) {
		return 0;
	}
	AudioDeviceID *ids = (AudioDeviceID*)malloc(sz);
	if (ids == NULL) {
		return -1;
	}
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &sz, ids) != noErr) {
		free(ids);
		return -1;
	}
	int n = (int)(sz / sizeof(AudioDeviceID));
	int oc = 0;
	for (int i = 0; i < n; i++) {
		if (caOutputChannels(ids[i]) > 0) oc++;
	}
	if (oc == 0) {
		free(ids);
		return 0;
	}
	CADev *devs = (CADev*)calloc((size_t)oc, sizeof(CADev));
	if (devs == NULL) {
		free(ids);
		return -1;
	}
	int j = 0;
	for (int i = 0; i < n && j < oc; i++) {
		if (caOutputChannels(ids[i]) <= 0) continue;
		devs[j].id = ids[i];
		devs[j].name = caDevName(ids[i]);
		devs[j].uid = caDevUID(ids[i]);
		j++;
	}
	free(ids);
	*out = devs;
	*count = j;
	return 0;
}

static void caFreeDevs(CADev *devs, int count) {
	if (devs == NULL) return;
	for (int i = 0; i < count; i++) {
		free(devs[i].name);
		free(devs[i].uid);
	}
	free(devs);
}
*/
import "C"
import (
	"fmt"
	"strings"
	"unsafe"
)

// ListAudioDevices enumerates Core Audio output devices.
func ListAudioDevices() ([]AudioDevice, error) {
	var cdevs *C.CADev
	var count C.int
	if C.caListOutputs(&cdevs, &count) != 0 {
		return nil, fmt.Errorf("Core Audio: failed to enumerate devices")
	}
	defer C.caFreeDevs(cdevs, count)
	if count <= 0 || cdevs == nil {
		return nil, nil
	}

	defaultID := C.caDefaultOutput()
	slice := unsafe.Slice(cdevs, int(count))
	devices := make([]AudioDevice, int(count))
	for i, d := range slice {
		devices[i] = AudioDevice{
			Index:       int(d.id),
			Name:        C.GoString(d.uid),
			Description: C.GoString(d.name),
			Active:      d.id == defaultID,
		}
	}
	return devices, nil
}

// PrepareAudioDevice temporarily changes the macOS system default output
// device so that the audio engine (oto/Core Audio) picks it up during init.
// Returns a cleanup function that restores the original default.
func PrepareAudioDevice(device string) func() {
	devices, err := ListAudioDevices()
	if err != nil {
		return func() {}
	}

	var targetID C.AudioDeviceID
	found := false
	for _, d := range devices {
		if strings.EqualFold(d.Name, device) || strings.EqualFold(d.Description, device) {
			targetID = C.AudioDeviceID(d.Index)
			found = true
			break
		}
	}
	if !found {
		return func() {}
	}

	savedID := C.caDefaultOutput()
	if C.caSetDefaultOutput(targetID) != 0 {
		return func() {}
	}
	return func() { C.caSetDefaultOutput(savedID) }
}

// SwitchAudioDevice changes the macOS system default output.
// Note: the running audio stream keeps its original device; the change
// takes full effect on the next app restart.
func SwitchAudioDevice(deviceName string) error {
	devices, err := ListAudioDevices()
	if err != nil {
		return err
	}
	for _, d := range devices {
		if strings.EqualFold(d.Name, deviceName) || strings.EqualFold(d.Description, deviceName) {
			if C.caSetDefaultOutput(C.AudioDeviceID(d.Index)) != 0 {
				return fmt.Errorf("Core Audio: failed to set output to %q", deviceName)
			}
			return nil
		}
	}
	return fmt.Errorf("device %q not found", deviceName)
}
