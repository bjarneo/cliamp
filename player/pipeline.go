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
	rc            io.ReadCloser // source file/HTTP body
	knownDuration time.Duration // metadata duration hint (0 = unknown); used when decoder.Len()==0

	// HTTP stream seek-by-reconnect fields.
	// seekableStream is true when the server returned a Content-Length and we
	// know the total duration, so we can reconnect with a Range header.
	seekableStream bool
	contentLength  int64         // Content-Length from the initial HTTP response
	path           string        // original URL (needed to reconnect for seek)
	streamOffset   time.Duration // playback time offset of the current ranged connection

	// Network byte counter — incremented by countingReader for HTTP streams.
	// nil for local files.
	bytesRead *atomic.Int64
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
	if tp.rc != nil {
		tp.rc.Close()
	}
}

// setKnownDuration stores the metadata duration hint and, for navFFmpegStreamer
// pipelines, converts it to sample frames so Len() and proportional seeking work.
func (tp *trackPipeline) setKnownDuration(d time.Duration) {
	tp.knownDuration = d
	if d > 0 {
		if ns, ok := tp.decoder.(*navFFmpegStreamer); ok && ns.total == 0 {
			ns.total = int(ns.sr.N(d))
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

// buildPipeline opens and decodes a track, returning a ready-to-play pipeline.
// knownDuration is passed in so seek-by-reconnect can be enabled for HTTP streams
// that provide a Content-Length. Call buildPipelineAt to open a ranged stream.
func (p *Player) buildPipeline(path string) (*trackPipeline, error) {
	return p.buildPipelineAt(path, 0, 0)
}

// buildPipelineAt is like buildPipeline but starts the HTTP stream at byteOffset
// (using a Range: bytes=N- header) and records timeOffset as the playback origin.
// For local files byteOffset is ignored; use decoder.Seek instead.
func (p *Player) buildPipelineAt(path string, byteOffset int64, timeOffset time.Duration) (*trackPipeline, error) {
	// Clear stream title on each new pipeline build.
	p.streamTitle.Store("")

	// For HTTP URLs, pass the ICY metadata callback; for local files, nil.
	var onMeta func(string)
	if isURL(path) {
		onMeta = p.setStreamTitle
	}

	// Navidrome/Subsonic tracks: buffer-while-playing via navBuffer + ffmpeg pipe.
	// The navBuffer downloads in the background; ffmpeg reads from it via stdin
	// and starts producing PCM as soon as the first frames arrive — no waiting
	// for the full download. seekable=true routes Seek() through navFFmpegStreamer
	// which repositions the navBuffer and restarts ffmpeg without HTTP reconnect.
	if isURL(path) && isNavidromeURL(path) && byteOffset == 0 {
		nb, contentLen, err := newNavBuffer(path)
		if err != nil {
			return nil, fmt.Errorf("navidrome buffer: %w", err)
		}

		// Derive total sample frames from the metadata duration hint so the
		// seek bar and Len() work correctly. knownDuration is set by the caller
		// (Play/Preload) onto the returned pipeline after buildPipeline returns,
		// so we compute it here from timeOffset which is always 0 on first open.
		// We use p.sr so the frame count matches the output sample rate.
		totalFrames := 0
		_ = timeOffset // unused for navBuffer path (byteOffset == 0 guard above)

		decoder, format, err := decodeNavFFmpeg(nb, p.sr, p.bitDepth, totalFrames)
		if err != nil {
			nb.Close()
			return nil, fmt.Errorf("decode navidrome: %w", err)
		}
		var s beep.Streamer = decoder
		return &trackPipeline{
			decoder:       decoder,
			stream:        s,
			format:        format,
			seekable:      true, // navFFmpegStreamer.Seek() handles seeking without reconnect
			rc:            nb,   // trackPipeline.close() calls nb.Close()
			path:          path,
			bytesRead:     &nb.bytesIn,
			contentLength: contentLen,
		}, nil
	}

	src, err := openSourceAt(path, byteOffset, onMeta)
	if err != nil {
		return nil, err
	}
	rc := src.body

	// Wrap HTTP streams with a counting reader for network stats.
	var byteCounter *atomic.Int64
	if isURL(path) {
		byteCounter = new(atomic.Int64)
		rc = &countingReader{inner: rc, count: byteCounter}
	}

	// Determine format: prefer URL extension, fall back to Content-Type.
	ext := formatExt(path)
	if isURL(path) && ext == ".mp3" && src.contentType != "" {
		if ctExt := extFromContentType(src.contentType); ctExt != "" {
			ext = ctExt
		}
	}

	// For OGG HTTP streams, use the chained decoder so Icecast radio
	// continues across song boundaries instead of stopping at EOS.
	if isURL(path) && ext == ".ogg" {
		tp, err := p.buildChainedOggPipeline(rc, onMeta)
		if err != nil {
			return nil, err
		}
		tp.bytesRead = byteCounter
		tp.contentLength = src.contentLength
		return tp, nil
	}

	// For HTTP streams that need ffmpeg (e.g. AAC+), use the streaming
	// pipe decoder so playback starts immediately instead of buffering
	// the entire (potentially infinite) stream.
	if isURL(path) && needsFFmpeg(ext) {
		rc.Close()
		decoder, format, err := decodeFFmpegStream(path, p.sr, p.bitDepth)
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		return &trackPipeline{
			decoder: decoder,
			stream:  decoder,
			format:  format,
		}, nil
	}

	// For local files that need ffmpeg (e.g. webm, m4a, opus), stream from
	// a pipe so playback starts instantly instead of buffering the entire
	// file to memory. Seeking is supported via ffmpeg -ss restart.
	if !isURL(path) && needsFFmpeg(ext) {
		rc.Close()
		decoder, format, err := decodeFFmpegLocal(path, p.sr, p.bitDepth)
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		return &trackPipeline{
			decoder:  decoder,
			stream:   decoder, // outputs at target sample rate
			format:   format,
			seekable: true,
			path:     path,
		}, nil
	}

	decoder, format, err := decodeWithExt(rc, ext, path, p.sr, p.bitDepth)
	if err != nil {
		rc.Close()
		// If the format already required ffmpeg (e.g., .m4a), decode() already
		// tried it — don't invoke ffmpeg a second time.
		if needsFFmpeg(ext) {
			return nil, fmt.Errorf("decode: %w", err)
		}
		// Native decoder failed (e.g., IEEE float WAV). Fall back to ffmpeg,
		// which reads from the path directly and handles more formats.
		decoder, format, err = decodeFFmpeg(path, p.sr, p.bitDepth)
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		// pcmStreamer is fully buffered in memory — always seekable, no rc to manage.
		return &trackPipeline{
			decoder:  decoder,
			stream:   decoder, // decodeFFmpeg outputs at target sample rate
			format:   format,
			seekable: true,
		}, nil
	}

	// HTTP streams decoded natively read from a non-seekable http.Response.Body.
	// FFmpeg-decoded streams are fully buffered in memory and therefore seekable.
	_, isPCM := decoder.(*pcmStreamer)
	seekable := !isURL(path) || isPCM

	// Native decoders (mp3, vorbis, flac, wav) wrap rc internally and their
	// Close() already closes the underlying reader. Set rc to nil so
	// trackPipeline.close() doesn't double-close the file descriptor.
	// FFmpeg decoders (reached via needsFFmpeg) read via the path argument;
	// rc is unused but still needs cleanup, so keep it set for that path.
	pipelineRC := rc
	if !isPCM {
		pipelineRC = nil
	}

	var s beep.Streamer = decoder
	if format.SampleRate != p.sr {
		s = beep.Resample(p.resampleQuality, format.SampleRate, p.sr, s)
	}

	tp := &trackPipeline{
		decoder:      decoder,
		stream:       s,
		format:       format,
		seekable:     seekable,
		rc:           pipelineRC,
		path:         path,
		streamOffset: timeOffset,
		bytesRead:    byteCounter,
	}

	// Mark HTTP streams with a known Content-Length as seek-by-reconnect capable.
	// We need contentLength > 0 to compute byte offsets; knownDuration is checked
	// later in Seek() when it is set on the pipeline.
	if isURL(path) && !seekable && src.contentLength > 0 {
		tp.seekableStream = true
		tp.contentLength = src.contentLength
	}

	return tp, nil
}

// buildChainedOggPipeline creates a pipeline with a chainedOggStreamer for
// Icecast OGG/Vorbis radio streams that re-initializes the decoder at each
// logical bitstream boundary.
func (p *Player) buildChainedOggPipeline(rc io.ReadCloser, onMeta func(string)) (*trackPipeline, error) {
	cs, format, err := newChainedOggStreamer(rc, p.sr, p.resampleQuality, onMeta)
	if err != nil {
		rc.Close()
		return nil, fmt.Errorf("decode chained ogg: %w", err)
	}

	return &trackPipeline{
		decoder:  cs,
		stream:   cs, // already resampled internally if needed
		format:   format,
		seekable: false,
		rc:       nil, // chainedOggStreamer owns the lifecycle
	}, nil
}
