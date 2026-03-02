package player

import (
	"fmt"
	"io"
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

// closePipelines closes one or more pipelines that are no longer in use.
func closePipelines(ps ...*trackPipeline) {
	for _, tp := range ps {
		if tp != nil {
			tp.close()
		}
	}
}

// buildPipeline opens and decodes a track, returning a ready-to-play pipeline.
func (p *Player) buildPipeline(path string) (*trackPipeline, error) {
	// Clear stream title on each new pipeline build.
	p.streamTitle.Store("")

	// For HTTP URLs, pass the ICY metadata callback; for local files, nil.
	var onMeta func(string)
	if isURL(path) {
		onMeta = p.setStreamTitle
	}

	src, err := openSource(path, onMeta)
	if err != nil {
		return nil, err
	}
	rc := src.body

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
		return p.buildChainedOggPipeline(rc, onMeta)
	}

	// For HTTP streams that need ffmpeg (e.g. AAC+), use the streaming
	// pipe decoder so playback starts immediately instead of buffering
	// the entire (potentially infinite) stream.
	if isURL(path) && needsFFmpeg(ext) {
		rc.Close()
		decoder, format, err := decodeFFmpegStream(path, p.sr)
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		return &trackPipeline{
			decoder: decoder,
			stream:  decoder,
			format:  format,
		}, nil
	}

	decoder, format, err := decodeWithExt(rc, ext, path, p.sr)
	if err != nil {
		rc.Close()
		// If the format already required ffmpeg (e.g., .m4a), decode() already
		// tried it — don't invoke ffmpeg a second time.
		if needsFFmpeg(ext) {
			return nil, fmt.Errorf("decode: %w", err)
		}
		// Native decoder failed (e.g., IEEE float WAV). Fall back to ffmpeg,
		// which reads from the path directly and handles more formats.
		decoder, format, err = decodeFFmpeg(path, p.sr)
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

	return &trackPipeline{
		decoder:  decoder,
		stream:   s,
		format:   format,
		seekable: seekable,
		rc:       pipelineRC,
	}, nil
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
