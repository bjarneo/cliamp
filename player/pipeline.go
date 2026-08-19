package player

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/gopxl/beep/v2"
)

// trackPipeline bundles a decoded track's resources.
type trackPipeline struct {
	decoder       beep.StreamSeekCloser // raw decoder (for Position/Duration/Seek)
	stream        beep.Streamer         // decoder + optional resample (fed to gapless)
	format        beep.Format
	seekable      bool
	knownDuration time.Duration // metadata duration hint (0 = unknown); used when decoder.Len()==0

	contentLength int64         // Content-Length from the initial HTTP response
	path          string        // original local path or URL
	streamOffset  time.Duration // playback origin for yt-dlp seek-by-restart

	// yt-dlp seek-by-restart: when true, seeking restarts yt-dlp with --download-sections.
	ytdlSeek bool

	// Network byte counter — incremented by countingReader for HTTP streams.
	// nil for local files.
	bytesRead *atomic.Int64

	// gaplessToken identifies this pipeline while it is registered as the
	// pending gapless stream. Delayed transition callbacks use it to avoid
	// clobbering a newer manual selection.
	gaplessToken uint64

	// verifiedSourceRate is the track's own native sample rate, independently
	// confirmed rather than assumed — the source-side analogue of
	// alsaDevice.verifiedRate, and for the same reason: a decode path that
	// transcodes through ffmpeg without probing the source first (radio,
	// HLS, yt-dlp, or a local file whose probe failed) reports PCM at
	// whatever rate ffmpeg was asked for, which says nothing about whether
	// that matches what the source was actually encoded at — format.SampleRate
	// stays correct for its own purpose (the rate frames actually arrive at
	// from this decoder) but must not be trusted as a claim about the
	// source. Zero means unverified. Only ever used for reporting
	// (BitPerfectStatus), never for pipeline/decode decisions.
	verifiedSourceRate int

	// verifiedSourceBits is the source's own bit depth in bits (e.g. 16, 24),
	// independently confirmed rather than assumed — the same distinction as
	// verifiedSourceRate above, but for bit depth: format.Precision is
	// always 4 bytes (32-bit float) for an FFmpeg-decoded source in
	// bit-perfect mode (ALAC, M4A, AAC, Opus, WMA, WebM), regardless of the
	// source's real depth, since that's the intermediate bit-perfect mode
	// decodes everything through to avoid truncating a 24-bit source — so
	// reporting it as-is would claim a 16-bit AAC or 24-bit ALAC file is
	// 32-bit. Zero means unverified. Only a native decoder (wav/flac/ogg/mp3
	// — decodeWithExt's non-FFmpeg branches, and chained OGG) reports its
	// own container's real precision, no FFmpeg intermediary involved.
	verifiedSourceBits int
}

// countingReader wraps an io.ReadCloser and atomically counts bytes read.
type countingReader struct {
	inner io.ReadCloser
	count *atomic.Int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.inner.Read(p)
	cr.count.Add(int64(n))
	return n, err
}

func (cr *countingReader) Close() error {
	return cr.inner.Close()
}

// close releases the pipeline's resources.
func (tp *trackPipeline) close() {
	if tp.decoder != nil {
		tp.decoder.Close()
	}
}

// interrupt unblocks a pipe decoder without waiting for its process. It is
// safe to call before speaker.Lock; close reaps the interrupted process later.
func (tp *trackPipeline) interrupt() {
	if decoder, ok := tp.decoder.(interface{ interrupt() }); ok {
		decoder.interrupt()
	}
}

// setKnownDuration stores the metadata duration hint and fills missing frame
// counts on streaming ffmpeg decoders so Len() and seeking keep working.
func (tp *trackPipeline) setKnownDuration(d time.Duration) {
	tp.knownDuration = d
	if d <= 0 {
		return
	}
	switch s := tp.decoder.(type) {
	case *navFFmpegStreamer:
		if s.total == 0 {
			s.total = int(s.sr.N(d))
		}
	case *localFFmpegStreamer:
		if s.total == 0 {
			s.total = int(s.sr.N(d))
		}
	}
}

// closePipelines closes one or more pipelines that are no longer in use.
func closePipelines(ps ...*trackPipeline) {
	for _, tp := range ps {
		if tp != nil {
			tp.close()
		}
	}
}

// decodeRateFor picks the sample rate a local-file ffmpeg decode should
// target. In bit-perfect mode it probes the file's own rate via ffprobe so
// ffmpeg performs no resampling; otherwise (or if probing fails) it is the
// device's current output rate. verified reports whether the returned rate
// is the file's confirmed native rate (a successful probe) as opposed to a
// fallback guess — callers use it to set trackPipeline.verifiedSourceRate.
// bits is the file's own bit depth if ffprobe could confirm one (only
// populated for a lossless codec — ALAC, in practice, since FLAC/WAV/OGG/MP3
// never reach here, see needsFFmpeg) — callers use it to set
// trackPipeline.verifiedSourceBits directly (already in bits, not bytes).
func (p *Player) decodeRateFor(path string) (rate beep.SampleRate, verified bool, bits int) {
	if p.bitPerfect {
		if native := probeNativeRate(path); native > 0 {
			return beep.SampleRate(native), true, probeNativeBits(path)
		}
	}
	return p.outRate(), false, 0
}

// verifiedRate is trackPipeline.verifiedSourceRate's value for a decode that
// reported sr with verified confidence (a real probe or a native container
// header) — 0 (unverified) otherwise. Collects the same "0 unless verified"
// rule used at every trackPipeline construction site into one place.
func verifiedRate(verified bool, sr beep.SampleRate) int {
	if !verified {
		return 0
	}
	return int(sr)
}

// verifiedBits is trackPipeline.verifiedSourceBits's value for a decode
// whose precisionBytes (beep.Format.Precision) is a genuine, decoder-intrinsic
// property of the source's own container — never an FFmpeg decode's target
// width — 0 (unverified) otherwise. See verifiedSourceBits's doc comment.
func verifiedBits(verified bool, precisionBytes int) int {
	if !verified {
		return 0
	}
	return precisionBytes * 8
}

// resampleTarget returns the rate a stream already decoded at native should be
// resampled to. In bit-perfect mode this is native itself — a no-op — so
// cliamp never resamples a locally-decoded stream; otherwise it is the
// device's current output rate.
func (p *Player) resampleTarget(native beep.SampleRate) beep.SampleRate {
	if p.bitPerfect {
		return native
	}
	return p.outRate()
}

// wrapResample wraps s in beep.Resample when native and target differ. Shared
// by decode-time pipelines (which resample to resampleTarget) and alignOutput
// (which resamples to whatever rate the device actually ended up at) — the
// two callers pick different targets for different reasons, but the wrapping
// itself is identical.
func (p *Player) wrapResample(native, target beep.SampleRate, s beep.Streamer) beep.Streamer {
	if native == target {
		return s
	}
	return beep.Resample(p.resampleQuality, native, target, s)
}

func (p *Player) decodeFFmpegURLStream(path string) (*ffmpegPipeStreamer, beep.Format, error) {
	decoder, format, err := decodeFFmpegStream(path, p.outRate(), p.bitDepth)
	if err != nil {
		return nil, beep.Format{}, err
	}
	if err := decoder.waitForInitialAudio(ffmpegPipeTimeout); err != nil {
		return nil, beep.Format{}, err
	}
	return decoder, format, nil
}

// buildPipeline opens and decodes a track, returning a ready-to-play pipeline.
func (p *Player) buildPipeline(path string) (*trackPipeline, error) {
	// Clear stream title on each new pipeline build.
	p.streamTitle.Store("")

	// Custom URI schemes (e.g., spotify:track:xxx) are handled by a
	// registered StreamerFactory, bypassing normal file/HTTP decoding.
	if factory := p.matchCustomURI(path); factory != nil {
		decoder, format, dur, err := factory(path)
		if err != nil {
			return nil, fmt.Errorf("custom streamer: %w", err)
		}
		tp := &trackPipeline{
			decoder:       decoder,
			format:        format,
			seekable:      true, // StreamerFactory returns beep.StreamSeekCloser — Seek() is supported
			knownDuration: dur,
		}
		tp.stream = p.wrapResample(format.SampleRate, p.resampleTarget(format.SampleRate), decoder)
		return tp, nil
	}

	// For HTTP URLs, pass the ICY metadata callback; for local files, nil.
	var onMeta func(string)
	if isURL(path) {
		onMeta = p.setStreamTitle
	}

	// Buffered HTTP tracks (e.g. Subsonic streams): buffer-while-playing via
	// navBuffer + ffmpeg pipe. The navBuffer downloads in the background; ffmpeg
	// reads from it via stdin and starts producing PCM as soon as the first
	// frames arrive — no waiting for the full download. seekable=true routes
	// Seek() through navFFmpegStreamer, which restarts FFmpeg from the buffered
	// header with a time offset and no HTTP reconnect.
	if isURL(path) && p.isBufferedURL(path) {
		// Bit-perfect mode: ffprobe can read a container's sample rate and bit
		// depth straight off an HTTP URL, same as it does for local files —
		// bits_per_raw_sample is only populated for a lossless codec (FLAC,
		// ALAC, WAV), so a transcoded/lossy Navidrome stream correctly probes
		// as unverified rather than as whatever width ffmpeg decodes to below.
		// Kick both off in parallel with opening the buffer instead of
		// blocking on them — a slow/unresponsive server would otherwise delay
		// stream start, which is exactly what navBuffer exists to avoid.
		var nativeRateCh, nativeBitsCh <-chan int
		if p.bitPerfect {
			nativeRateCh = probeNativeRateAsync(path)
			nativeBitsCh = probeNativeBitsAsync(path)
		}

		nb, contentLen, err := newNavBuffer(path)
		if err != nil {
			return nil, fmt.Errorf("navidrome buffer: %w", err)
		}

		// Both probes usually resolve well before newNavBuffer returns (they
		// only need the container header, not the file), but cap the total
		// wait so a hung probe can't stall playback — sharing one deadline
		// keeps that cap at ~2s combined rather than 2s per probe.
		decodeRate := p.outRate()
		verifiedRateOK := false
		sourceBits := 0
		deadline := time.Now().Add(2 * time.Second)
		if nativeRateCh != nil {
			select {
			case native := <-nativeRateCh:
				if native > 0 {
					decodeRate = beep.SampleRate(native)
					verifiedRateOK = true
				}
			case <-time.After(time.Until(deadline)):
			}
		}
		if nativeBitsCh != nil {
			select {
			case bits := <-nativeBitsCh:
				sourceBits = bits
			case <-time.After(time.Until(deadline)):
			}
		}

		decoder, format, err := decodeNavFFmpeg(nb, decodeRate, p.bitDepth, 0)
		if err != nil {
			nb.Close()
			return nil, fmt.Errorf("decode navidrome: %w", err)
		}
		return &trackPipeline{
			decoder:            decoder,
			stream:             decoder,
			format:             format,
			verifiedSourceRate: verifiedRate(verifiedRateOK, format.SampleRate),
			verifiedSourceBits: sourceBits,
			seekable:           true, // navFFmpegStreamer.Seek() handles seeking without reconnect
			path:               path,
			bytesRead:          &nb.bytesIn,
			contentLength:      contentLen,
		}, nil
	}

	ext := formatExt(path)

	// HLS playlists must be opened by ffmpeg directly from the URL so it can
	// resolve relative chunklist/segment URIs and follow the live segment
	// window. Feeding the playlist bytes via stdin (the needsFFmpeg path below)
	// would strip the base URL and break relative segment resolution.
	if isURL(path) && isHLS(ext) {
		decoder, format, err := p.decodeFFmpegURLStream(path)
		if err != nil {
			return nil, fmt.Errorf("open hls: %w", err)
		}
		return &trackPipeline{
			decoder: decoder,
			stream:  decoder,
			format:  format,
			path:    path,
		}, nil
	}

	src, err := openSource(path, onMeta)
	if err != nil {
		return nil, fmt.Errorf("open source: %w", err)
	}
	rc := src.body

	// Wrap HTTP streams with a counting reader for network stats.
	var byteCounter *atomic.Int64
	if isURL(path) {
		byteCounter = new(atomic.Int64)
		rc = &countingReader{inner: rc, count: byteCounter}
	}

	// Determine format: prefer URL extension, fall back to Content-Type.
	if isURL(path) && ext == ".mp3" && src.contentType != "" {
		if ctExt := extFromContentType(src.contentType); ctExt != "" {
			ext = ctExt
		}
	}

	// For OGG HTTP streams, use the chained decoder so Icecast radio
	// continues across song boundaries instead of stopping at EOS.
	// If Vorbis init fails (e.g. OggFLAC or OggOpus), fall back to ffmpeg.
	if isURL(path) && ext == ".ogg" {
		tp, err := p.buildChainedOggPipeline(rc, onMeta)
		if err != nil {
			rc.Close()
			decoder, fmt2, err2 := p.decodeFFmpegURLStream(path)
			if err2 != nil {
				return nil, fmt.Errorf("decode: %w", err2)
			}
			return &trackPipeline{
				decoder: decoder,
				stream:  decoder,
				format:  fmt2,
			}, nil
		}
		tp.bytesRead = byteCounter
		tp.contentLength = src.contentLength
		return tp, nil
	}

	// For HTTP streams that need ffmpeg (e.g. AAC+), use the streaming
	// pipe decoder so playback starts immediately instead of buffering
	// the entire (potentially infinite) stream. Feed ffmpeg from the existing
	// reader chain via stdin rather than handing it the URL: this keeps the
	// ICY metadata reader attached so live radio StreamTitle parsing works for
	// ffmpeg-only codecs (AAC, AAC+, Opus, ...).
	if isURL(path) && needsFFmpeg(ext) {
		decoder, format, err := decodeFFmpegPipeStream(rc, p.outRate(), p.bitDepth)
		if err != nil {
			rc.Close()
			return nil, fmt.Errorf("decode: %w", err)
		}
		if err := decoder.waitForInitialAudio(ffmpegPipeTimeout); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		return &trackPipeline{
			decoder:       decoder,
			stream:        decoder,
			format:        format,
			path:          path,
			bytesRead:     byteCounter,
			contentLength: src.contentLength,
		}, nil
	}

	// SSH streams with ffmpeg-required formats cannot be decoded: ffmpeg
	// expects a local file path or HTTP URL, not ssh:// pipes.
	if isSSH(path) && needsFFmpeg(ext) {
		rc.Close()
		return nil, fmt.Errorf("SSH streaming does not support %s format (requires ffmpeg)", ext)
	}

	// For local files that need ffmpeg (e.g. webm, m4a, opus), stream from
	// a pipe so playback starts instantly instead of buffering the entire
	// file to memory. Seeking is supported via ffmpeg -ss restart.
	if !isURL(path) && needsFFmpeg(ext) {
		rc.Close()
		rate, verified, bits := p.decodeRateFor(path)
		decoder, format, err := decodeFFmpegLocal(path, rate, p.bitDepth)
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		return &trackPipeline{
			decoder:            decoder,
			stream:             decoder, // outputs at target sample rate
			format:             format,
			verifiedSourceRate: verifiedRate(verified, format.SampleRate),
			verifiedSourceBits: bits,
			seekable:           true,
			path:               path,
		}, nil
	}

	decoder, format, err := decodeWithExt(rc, ext, path, p.outRate(), p.bitDepth)
	if err != nil {
		rc.Close()
		// If the format already required ffmpeg (e.g., .m4a), decodeWithExt already
		// tried it — don't invoke ffmpeg a second time.
		if needsFFmpeg(ext) {
			return nil, fmt.Errorf("decode: %w", err)
		}
		if isURL(path) {
			decoder, format, err := p.decodeFFmpegURLStream(path)
			if err != nil {
				return nil, fmt.Errorf("decode: %w", err)
			}
			return &trackPipeline{
				decoder: decoder,
				stream:  decoder,
				format:  format,
				path:    path,
			}, nil
		}
		// Native local decoder failed (e.g., IEEE float WAV). Fall back to a
		// streaming ffmpeg process, which handles more formats without buffering
		// the whole decoded track in memory.
		rate, verified, bits := p.decodeRateFor(path)
		decoder, format, err = decodeFFmpegLocal(path, rate, p.bitDepth)
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		return &trackPipeline{
			decoder:            decoder,
			stream:             decoder, // decodeFFmpegLocal outputs at target sample rate
			format:             format,
			verifiedSourceRate: verifiedRate(verified, format.SampleRate),
			verifiedSourceBits: bits,
			seekable:           true,
			path:               path,
		}, nil
	}

	// HTTP streams decoded natively read from a non-seekable http.Response.Body.
	seekable := !isURL(path)

	tp := &trackPipeline{
		decoder:       decoder,
		format:        format,
		seekable:      seekable,
		path:          path,
		bytesRead:     byteCounter,
		contentLength: src.contentLength,
	}
	tp.stream = p.wrapResample(format.SampleRate, p.resampleTarget(format.SampleRate), decoder)
	// decodeWithExt's native branches (wav/flac/vorbis/mp3) read the rate and
	// bit depth straight out of the container header — no transcoding, so no
	// guess involved. needsFFmpeg(ext) is always false by construction here
	// (the two branches above already handle every ffmpeg-needing extension,
	// for both URL and local paths), but the guard costs nothing and keeps
	// this correct even if that ever changes.
	verifiedNative := !needsFFmpeg(ext)
	tp.verifiedSourceRate = verifiedRate(verifiedNative, format.SampleRate)
	tp.verifiedSourceBits = verifiedBits(verifiedNative, format.Precision)

	return tp, nil
}

// buildChainedOggPipeline creates a pipeline with a chainedOggStreamer for
// Icecast OGG/Vorbis radio streams that re-initializes the decoder at each
// logical bitstream boundary.
func (p *Player) buildChainedOggPipeline(rc io.ReadCloser, onMeta func(string)) (*trackPipeline, error) {
	cs, format, err := newChainedOggStreamer(rc, p.outRate(), p.resampleQuality, onMeta)
	if err != nil {
		rc.Close()
		return nil, fmt.Errorf("decode chained ogg: %w", err)
	}

	return &trackPipeline{
		decoder: cs,
		stream:  cs, // already resampled internally if needed
		format:  format,
		// The oggvorbis reader reports the current segment's own header
		// rate directly, no transcoding involved — genuinely verified, same
		// as decodeWithExt's native branches. Precision is a fixed decode
		// convention (Vorbis, like MP3, is lossy and has no real bit-depth
		// field), but it's decoder-intrinsic and never varies with an
		// unrelated config value the way an FFmpeg decode's target width
		// does, so it's trustworthy for display the same way.
		verifiedSourceRate: int(format.SampleRate),
		verifiedSourceBits: format.Precision * 8,
		seekable:           false,
	}, nil
}
