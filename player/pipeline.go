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

	// download is the buffered HTTP download behind the pipeline, nil for
	// other sources. applyReplayGain reads the stream's tags from it.
	download *navBuffer

	// livePrefetch is set when stream is wrapped in a livePrefetchStreamer
	// (non-seekable HTTP sources and yt-dlp pages). close() stops its fill
	// goroutine.
	livePrefetch *livePrefetchStreamer
}

// positionAndDuration is the clock of the pipeline. The caller holds the
// speaker lock, so the audio goroutine cannot move the decoder during the
// read. A live prefetch reports the audio that the speaker took from it, and
// the duration that the decoder had before the prefetch started when no
// metadata duration is known.
func (tp *trackPipeline) positionAndDuration() (time.Duration, time.Duration) {
	if tp.livePrefetch != nil {
		dur := tp.knownDuration
		if dur <= 0 {
			dur = tp.decodedDuration
		}
		return tp.livePrefetch.Position() + tp.streamOffset, dur
	}
	pos := tp.format.SampleRate.D(tp.decoder.Position()) + tp.streamOffset
	if n := tp.decoder.Len(); n > 0 {
		return pos, tp.format.SampleRate.D(n)
	}
	return pos, tp.knownDuration
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
// counts on seekable ffmpeg decoders so Len() and seeking keep working.
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

// bufferedPipeline decodes a progressive navBuffer download through ffmpeg.
// ffmpeg reads from the buffer through stdin and produces PCM as soon as the
// first frames arrive, so playback does not wait for the full download.
// navFFmpegStreamer.Seek restarts ffmpeg from the buffered header with a time
// offset, so a seek needs no HTTP reconnect.
func (p *Player) bufferedPipeline(path string, nb *navBuffer, contentLen int64) (*trackPipeline, error) {
	decoder, format, err := decodeNavFFmpeg(nb, p.sr, p.bitDepth)
	if err != nil {
		nb.Close()
		return nil, fmt.Errorf("decode source: %w", err)
	}
	return &trackPipeline{
		decoder:       decoder,
		stream:        decoder,
		format:        format,
		seekable:      true,
		path:          path,
		bytesRead:     &nb.bytesIn,
		contentLength: contentLen,
		download:      nb,
	}, nil
}

// ffmpegURLPipeline lets ffmpeg open path by URL and waits for the first PCM.
// The pipeline cannot seek. live marks an ICY radio response, and prefetch
// keeps network decoding off the speaker callback.
func (p *Player) ffmpegURLPipeline(path string, live, prefetch bool) (*trackPipeline, error) {
	decoder, format, err := decodeFFmpegStream(path, p.sr, p.bitDepth)
	if err != nil {
		return nil, err
	}
	if err := decoder.waitForInitialAudio(ffmpegPipeTimeout); err != nil {
		return nil, err
	}
	return p.prefetchNetworkPipeline(&trackPipeline{
		decoder: decoder,
		stream:  decoder,
		format:  format,
		path:    path,
		live:    live,
	}, prefetch), nil
}

// localFFmpegPipeline streams a local file through ffmpeg, so playback starts
// at once instead of after the whole file is decoded to memory. A seek
// restarts ffmpeg with -ss.
func (p *Player) localFFmpegPipeline(path string) (*trackPipeline, error) {
	decoder, format, err := decodeFFmpegLocal(path, p.sr, p.bitDepth)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &trackPipeline{
		decoder:  decoder,
		stream:   decoder, // decodeFFmpegLocal outputs at the target sample rate
		format:   format,
		seekable: true,
		path:     path,
	}, nil
}

// buildSource opens path on the pipeline that its source needs. It is the one
// place that routes a path. A page URL that the yt-dlp matcher claims plays
// through the yt-dlp | ffmpeg chain, which starts at 0. Any other path goes
// through buildPipeline and starts at offset when its decoder can seek.
// knownDuration is the metadata duration (use 0 if unknown). With
// probeDuration, a yt-dlp source with no known duration asks yt-dlp for one.
func (p *Player) buildSource(path string, knownDuration, offset time.Duration, probeDuration bool) (*trackPipeline, error) {
	if p.isYTDLURL(path) {
		return p.buildYTDLSource(path, knownDuration, probeDuration)
	}
	tp, err := p.buildPipeline(path, knownDuration)
	if err != nil {
		return nil, fmt.Errorf("play at %v: %w", offset, err)
	}
	p.applyReplayGain(tp)
	tp.setKnownDuration(knownDuration)
	if offset > 0 && tp.seekable {
		if sample := relativeSeekSample(tp, offset); sample > 0 {
			// Ignored deliberately: a failed seek should start the track from
			// the beginning, not refuse to play it.
			_ = tp.decoder.Seek(sample)
		}
	}
	return tp, nil
}

// buildPipeline opens and decodes a track, returning a ready-to-play pipeline.
// bufferedPipeline, ffmpegURLPipeline and localFFmpegPipeline build the
// routes that more than one source type shares. knownDuration is the metadata
// duration (use 0 if unknown).
func (p *Player) buildPipeline(path string, knownDuration time.Duration) (*trackPipeline, error) {
	// Clear stream title on each new pipeline build.
	p.streamTitle.Store("")

	// Custom URI schemes (e.g., spotify:track:xxx) are handled by a
	// registered StreamerFactory, bypassing normal file/HTTP decoding.
	if factory := p.matchCustomURI(path); factory != nil {
		decoder, format, dur, err := factory(path)
		if err != nil {
			return nil, fmt.Errorf("custom streamer: %w", err)
		}
		s := resampleWithHeadroom(p.resampleQuality, format.SampleRate, p.sr, decoder)
		return &trackPipeline{
			decoder:       decoder,
			stream:        s,
			format:        format,
			seekable:      true, // StreamerFactory returns beep.StreamSeekCloser — Seek() is supported
			knownDuration: dur,
		}, nil
	}

	// Custom URIs with a registered SourceResolver (e.g. tidal://track/123)
	// resolve to their actual bytes at play time, so short-lived signed URLs
	// are always fresh. Segment lists get a concatenating navBuffer; direct
	// URLs fall through to the normal HTTP handling below. A Buffered URL
	// takes the buffered route there without a URL matcher.
	buffered := false
	if resolver := p.matchSourceResolver(path); resolver != nil {
		src, err := resolver(path)
		if err != nil {
			return nil, fmt.Errorf("resolve source: %w", err)
		}
		if len(src.Segments) > 0 {
			nb, contentLen, err := newNavBufferSegments(src.Segments)
			if err != nil {
				return nil, fmt.Errorf("segment buffer: %w", err)
			}
			return p.bufferedPipeline(path, nb, contentLen)
		}
		if src.URL == "" {
			return nil, fmt.Errorf("resolve source: empty result for %s", path)
		}
		path = src.URL
		buffered = src.Buffered
	}

	remote := isURL(path)

	// Buffered HTTP tracks (e.g. Subsonic streams) download in the background
	// while they play.
	if remote && (buffered || p.isBufferedURL(path)) {
		nb, contentLen, err := newNavBuffer(path)
		if err != nil {
			return nil, fmt.Errorf("buffer source: %w", err)
		}
		return p.bufferedPipeline(path, nb, contentLen)
	}

	ext := formatExt(path)

	// HLS playlists must be opened by ffmpeg directly from the URL so it can
	// resolve relative chunklist/segment URIs and follow the live segment
	// window. Feeding the playlist bytes via stdin (the needsFFmpeg path below)
	// would strip the base URL and break relative segment resolution.
	if remote && isHLS(ext) {
		tp, err := p.ffmpegURLPipeline(path, false, true)
		if err != nil {
			return nil, fmt.Errorf("open hls: %w", err)
		}
		return tp, nil
	}

	// For HTTP URLs, pass the ICY metadata callback; for local files, nil.
	var onMeta func(string)
	if remote {
		onMeta = p.setStreamTitle
	}
	src, err := openSource(path, onMeta)
	if err != nil {
		return nil, fmt.Errorf("open source: %w", err)
	}

	// A finite HTTP source is a file, not a broadcast, so it belongs on the
	// buffered pipeline where it can be seeked. The response itself is the only
	// reliable signal: podcast CDNs rewrite enclosure URLs per request, with
	// tracking prefixes and signed parameters, so a URL cannot be recognized
	// from one play to the next.
	//
	// The headers have arrived but no audio has been read, so closing here
	// costs a connection setup and nothing more.
	if remote && !src.live && src.contentLength > 0 && ffmpegAvailable() {
		_ = src.body.Close()
		nb, contentLen, err := newNavBuffer(path)
		if err != nil {
			return nil, fmt.Errorf("buffer source: %w", err)
		}
		return p.bufferedPipeline(path, nb, contentLen)
	}

	rc := src.body

	// Wrap HTTP streams with a counting reader for network stats.
	var byteCounter *atomic.Int64
	if remote {
		byteCounter = new(atomic.Int64)
		rc = &countingReader{inner: rc, count: byteCounter}
	}

	// Determine format: prefer URL extension, fall back to Content-Type.
	if remote && ext == ".mp3" && src.contentType != "" {
		if ctExt := extFromContentType(src.contentType); ctExt != "" {
			ext = ctExt
		}
	}

	// For OGG HTTP streams, use the chained decoder so Icecast radio
	// continues across song boundaries instead of stopping at EOS.
	// If Vorbis init fails (e.g. OggFLAC or OggOpus), fall back to ffmpeg.
	if remote && ext == ".ogg" {
		tp, err := p.buildChainedOggPipeline(rc, onMeta)
		if err != nil {
			// buildChainedOggPipeline has closed rc.
			tp, err := p.ffmpegURLPipeline(path, src.live, src.prefetch)
			if err != nil {
				return nil, fmt.Errorf("decode: %w", err)
			}
			return tp, nil
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
	if remote && needsFFmpeg(ext) {
		// A duration identifies a finite HTTP source even when the server also
		// sends ICY headers. Its clean EOF must remain an ordinary track end.
		// The decoder gets this before the prefetch starts to read it.
		decoder, format, err := decodeFFmpegPipeStream(rc, p.sr, p.bitDepth, src.live && knownDuration <= 0)
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

	if needsFFmpeg(ext) {
		rc.Close()
		// SSH streams with ffmpeg-required formats cannot be decoded: ffmpeg
		// expects a local file path or HTTP URL, not ssh:// pipes.
		if isSSH(path) {
			return nil, fmt.Errorf("SSH streaming does not support %s format (requires ffmpeg)", ext)
		}
		return p.localFFmpegPipeline(path)
	}

	decoder, format, err := decodeWithExt(rc, ext)
	if err != nil {
		rc.Close()
		if remote {
			tp, err := p.ffmpegURLPipeline(path, src.live, src.prefetch)
			if err != nil {
				return nil, fmt.Errorf("decode: %w", err)
			}
			return tp, nil
		}
		// Native local decoder failed (e.g., IEEE float WAV). Fall back to a
		// streaming ffmpeg process, which handles more formats without buffering
		// the whole decoded track in memory.
		return p.localFFmpegPipeline(path)
	}

	// HTTP streams decoded natively read from a non-seekable http.Response.Body.
	seekable := !remote

	s := resampleWithHeadroom(p.resampleQuality, format.SampleRate, p.sr, decoder)

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
