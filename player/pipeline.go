package player

import (
	"fmt"
	"io"

	"github.com/gopxl/beep/v2"
)

// trackPipeline bundles a decoded track's resources.
type trackPipeline struct {
	decoder  beep.StreamSeekCloser // raw decoder (for Position/Duration/Seek)
	stream   beep.Streamer         // decoder + optional resample (fed to gapless)
	format   beep.Format
	seekable bool
	rc       io.ReadCloser // source file/HTTP body
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

	rc, err := openSource(path, onMeta)
	if err != nil {
		return nil, err
	}

	// For OGG HTTP streams, use the chained decoder so Icecast radio
	// continues across song boundaries instead of stopping at EOS.
	ext := formatExt(path)
	if isURL(path) && ext == ".ogg" {
		return p.buildChainedOggPipeline(rc)
	}

	decoder, format, err := decode(rc, path, p.sr)
	if err != nil {
		// Native decoder failed (e.g., IEEE float WAV). Fall back to ffmpeg,
		// which reads from the path directly and handles more formats.
		// Close rc first — it's partially consumed and ffmpeg doesn't need it.
		rc.Close()
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
		s = beep.Resample(4, format.SampleRate, p.sr, s)
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
func (p *Player) buildChainedOggPipeline(rc io.ReadCloser) (*trackPipeline, error) {
	cs, format, err := newChainedOggStreamer(rc, p.sr)
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
