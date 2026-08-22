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
	decoder         beep.StreamSeekCloser // raw decoder (for Position/Duration/Seek)
	stream          beep.Streamer         // decoder + optional resample (fed to gapless)
	format          beep.Format
	seekable        bool
	knownDuration   time.Duration // metadata duration hint (0 = unknown); used when decoder.Len()==0
	decodedDuration time.Duration // decoder length captured before background prefetch starts

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

	live bool

	// livePrefetch is set when stream is wrapped in a livePrefetchStreamer
	// (live/non-seekable HTTP sources). close() stops its fill goroutine.
	livePrefetch *livePrefetchStreamer
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
	if tp.livePrefetch != nil {
		tp.livePrefetch.Close()
	}
	if tp.decoder != nil {
		tp.decoder.Close()
	}
	if tp.livePrefetch != nil {
		tp.livePrefetch.Wait()
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
	case *ffmpegPipeStreamer:
		// A duration identifies a finite HTTP source even when the server also
		// sends ICY headers. Its clean EOF must remain an ordinary track end.
		s.live = false
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

func (p *Player) prefetchNetworkPipeline(tp *trackPipeline, enabled bool) *trackPipeline {
	if !enabled {
		return tp
	}
	if n := tp.decoder.Len(); n > 0 {
		tp.decodedDuration = tp.format.SampleRate.D(n)
	}
	prefetch := newLivePrefetchStreamer(tp.stream, p.sr)
	tp.stream = prefetch
	tp.livePrefetch = prefetch
	return tp
}

func (p *Player) decodeFFmpegURLStream(path string) (*ffmpegPipeStreamer, beep.Format, error) {
	decoder, format, err := decodeFFmpegStream(path, p.sr, p.bitDepth)
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
		var s beep.Streamer = decoder
		if format.SampleRate != p.sr {
			s = beep.Resample(p.resampleQuality, format.SampleRate, p.sr, s)
		}
		return &trackPipeline{
			decoder:       decoder,
			stream:        s,
			format:        format,
			seekable:      true, // StreamerFactory returns beep.StreamSeekCloser — Seek() is supported
			knownDuration: dur,
		}, nil
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
		nb, contentLen, err := newNavBuffer(path)
		if err != nil {
			return nil, fmt.Errorf("navidrome buffer: %w", err)
		}

		decoder, format, err := decodeNavFFmpeg(nb, p.sr, p.bitDepth, 0)
		if err != nil {
			nb.Close()
			return nil, fmt.Errorf("decode navidrome: %w", err)
		}
		return &trackPipeline{
			decoder:       decoder,
			stream:        decoder,
			format:        format,
			seekable:      true, // navFFmpegStreamer.Seek() handles seeking without reconnect
			path:          path,
			bytesRead:     &nb.bytesIn,
			contentLength: contentLen,
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
		return p.prefetchNetworkPipeline(&trackPipeline{
			decoder: decoder,
			stream:  decoder,
			format:  format,
			path:    path,
		}, true), nil
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
			return p.prefetchNetworkPipeline(&trackPipeline{
				decoder: decoder,
				stream:  decoder,
				format:  fmt2,
				path:    path,
				live:    src.live,
			}, src.prefetch), nil
		}
		tp.bytesRead = byteCounter
		tp.contentLength = src.contentLength
		tp.path = path
		tp.live = src.live
		return p.prefetchNetworkPipeline(tp, src.prefetch), nil
	}

	// For HTTP streams that need ffmpeg (e.g. AAC+), use the streaming
	// pipe decoder so playback starts immediately instead of buffering
	// the entire (potentially infinite) stream. Feed ffmpeg from the existing
	// reader chain via stdin rather than handing it the URL: this keeps the
	// ICY metadata reader attached so live radio StreamTitle parsing works for
	// ffmpeg-only codecs (AAC, AAC+, Opus, ...).
	if isURL(path) && needsFFmpeg(ext) {
		decoder, format, err := decodeFFmpegPipeStream(rc, p.sr, p.bitDepth, src.live)
		if err != nil {
			rc.Close()
			return nil, fmt.Errorf("decode: %w", err)
		}
		if err := decoder.waitForInitialAudio(ffmpegPipeTimeout); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		return p.prefetchNetworkPipeline(&trackPipeline{
			decoder:       decoder,
			stream:        decoder,
			format:        format,
			path:          path,
			bytesRead:     byteCounter,
			contentLength: src.contentLength,
			live:          src.live,
		}, src.prefetch), nil
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
			return p.prefetchNetworkPipeline(&trackPipeline{
				decoder: decoder,
				stream:  decoder,
				format:  format,
				path:    path,
				live:    src.live,
			}, src.prefetch), nil
		}
		// Native local decoder failed (e.g., IEEE float WAV). Fall back to a
		// streaming ffmpeg process, which handles more formats without buffering
		// the whole decoded track in memory.
		decoder, format, err = decodeFFmpegLocal(path, p.sr, p.bitDepth)
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		return &trackPipeline{
			decoder:  decoder,
			stream:   decoder, // decodeFFmpegLocal outputs at target sample rate
			format:   format,
			seekable: true,
			path:     path,
		}, nil
	}

	// HTTP streams decoded natively read from a non-seekable http.Response.Body.
	seekable := !isURL(path)

	var s beep.Streamer = decoder
	if format.SampleRate != p.sr {
		s = beep.Resample(p.resampleQuality, format.SampleRate, p.sr, s)
	}

	tp := &trackPipeline{
		decoder:       decoder,
		stream:        s,
		format:        format,
		seekable:      seekable,
		path:          path,
		bytesRead:     byteCounter,
		contentLength: src.contentLength,
		live:          src.live,
	}

	return p.prefetchNetworkPipeline(tp, src.prefetch), nil
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
	}, nil
}
