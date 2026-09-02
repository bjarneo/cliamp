package player

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/player/openmpt"
)

// openmptExts are the tracker module extensions decoded via libopenmpt
// (player/openmpt), derived from openmpt.SupportedExtensions. Listed
// separately from SupportedExts so decodeWithExt can route them before
// falling into the mp3/wav/flac/ogg switch below — and kept as a static
// map (built once here, not on every lookup) for the same reason
// SupportedExts itself is static: resolve.go and the file browser need to
// answer "is this extension playable?" without loading libopenmpt just to
// find out.
var openmptExts = func() map[string]bool {
	m := make(map[string]bool, len(openmpt.SupportedExtensions))
	for _, ext := range openmpt.SupportedExtensions {
		m["."+ext] = true
	}
	return m
}()

func init() {
	for ext := range openmptExts {
		SupportedExts[ext] = true
	}
}

// decodeOpenmpt decodes a tracker module file via libopenmpt — .mod, .s3m,
// .xm, .it, and the rest of openmpt.SupportedExtensions. libopenmpt
// renders directly at the requested sample rate, so no separate
// resampling stage is needed.
func decodeOpenmpt(rc io.ReadCloser, path string, sr beep.SampleRate) (beep.StreamSeekCloser, beep.Format, error) {
	defer rc.Close()

	if !openmpt.Available() {
		return nil, beep.Format{}, fmt.Errorf("libopenmpt is required to play %s — install: %s (%w)", path, openmptInstallHint(), openmpt.ErrUnavailable)
	}

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, beep.Format{}, fmt.Errorf("read module: %w", err)
	}

	mod, err := openmpt.Open(data)
	if err != nil {
		return nil, beep.Format{}, err
	}

	format := beep.Format{SampleRate: sr, NumChannels: 2, Precision: 2}
	length := int(mod.DurationSeconds() * float64(sr))
	return &openmptStreamer{mod: mod, sampleRate: int(sr), length: length}, format, nil
}

// openmptInstallHint returns a platform-specific install command
// suggestion, mirroring ffmpegInstallHint in ytdl.go. Windows has no
// well-known package-manager entry for a bare shared library, so it
// points at a direct download instead of guessing at a winget package.
func openmptInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install libopenmpt"
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			return "sudo apt install libopenmpt0"
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			return "sudo pacman -S libopenmpt"
		}
		return "see https://lib.openmpt.org/libopenmpt/"
	case "windows":
		return "download libopenmpt.dll from https://lib.openmpt.org/libopenmpt/ and place it next to cliamp.exe"
	default:
		return "see https://lib.openmpt.org/libopenmpt/"
	}
}

// openmptStreamer adapts an *openmpt.Module to beep.StreamSeekCloser.
type openmptStreamer struct {
	mod        *openmpt.Module
	sampleRate int
	buf        []float32
	pos        int
	length     int // total frames; 0 if libopenmpt couldn't estimate a duration
	drained    bool
}

func (s *openmptStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	if s.drained {
		return 0, false
	}

	need := len(samples) * 2
	if cap(s.buf) < need {
		s.buf = make([]float32, need)
	}
	buf := s.buf[:need]

	got := s.mod.ReadInterleavedFloatStereo(s.sampleRate, buf)
	if got == 0 {
		s.drained = true
		return 0, false
	}

	for i := 0; i < got; i++ {
		samples[i][0] = float64(buf[i*2])
		samples[i][1] = float64(buf[i*2+1])
	}
	s.pos += got
	return got, true
}

func (s *openmptStreamer) Err() error { return nil }

func (s *openmptStreamer) Len() int { return s.length }

func (s *openmptStreamer) Position() int { return s.pos }

func (s *openmptStreamer) Seek(p int) error {
	if p < 0 {
		return errors.New("openmpt: negative seek position")
	}
	actual := s.mod.SetPositionSeconds(float64(p) / float64(s.sampleRate))
	s.pos = int(actual * float64(s.sampleRate))
	s.drained = false
	return nil
}

func (s *openmptStreamer) Close() error {
	s.mod.Close()
	return nil
}
