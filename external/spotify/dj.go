package spotify

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
	librespot "github.com/devgianlu/go-librespot"
	lp "github.com/devgianlu/go-librespot/player"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	narrationpb "github.com/devgianlu/go-librespot/proto/spotify/narration"
	"github.com/devgianlu/go-librespot/spclient"
	"github.com/gopxl/beep/v2"
)

const DJPlaylistID = "spotify:dj"
const DJContextURI = "spotify:playlist:37i9dQZF1EYkqdzj48dyYq"
const djStreamPrefix = "spotify:dj:"

// DJSession owns the resolver and narration metadata for one local DJ run.
// Next is called sequentially, only when the current batch is exhausted.
type DJSession struct {
	mu       sync.Mutex
	resolver *spclient.ContextResolver
	session  *Session
	ctx      context.Context
	cancel   context.CancelFunc
	page     int
	id       string
	entries  map[string]*connectpb.ContextTrack
}

func (p *SpotifyProvider) StartDJ(ctx context.Context) (*DJSession, error) {
	if err := p.ensureSessionContext(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	sess := p.session
	p.mu.Unlock()
	if sess == nil {
		return nil, playlist.ErrNeedsAuth
	}
	ctx, cancel := context.WithCancel(ctx)
	sess.mu.RLock()
	if sess.sess == nil {
		sess.mu.RUnlock()
		cancel()
		return nil, fmt.Errorf("spotify: session closed")
	}
	// DJ is a lexicon session, not the otherwise empty playlist with this URI.
	resolver, err := resolveDJContext(ctx, sess.sess.Spclient())
	sess.mu.RUnlock()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("spotify: start DJ: %w", err)
	}
	dj := &DJSession{resolver: resolver, session: sess, ctx: ctx, cancel: cancel, id: rand.Text(), entries: make(map[string]*connectpb.ContextTrack)}
	p.mu.Lock()
	if p.session != sess || ctx.Err() != nil {
		p.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("spotify: DJ setup cancelled")
	}
	old := p.dj
	p.dj = dj
	p.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return dj, nil
}

func resolveDJContext(ctx context.Context, sp *spclient.Spclient) (*spclient.ContextResolver, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endpoint := "hm://lexicon-session-provider/context-resolve/v2/session?contextUri=" + DJContextURI + "&reason=state_restore"
	spotCtx, err := sp.ContextResolveUrl(requestCtx, endpoint)
	if err != nil {
		return nil, err
	}
	if spotCtx.Loading || len(spotCtx.Pages) == 0 {
		return nil, fmt.Errorf("DJ context has no ready pages")
	}
	for _, page := range spotCtx.Pages {
		if page == nil {
			return nil, fmt.Errorf("DJ context contains a missing page")
		}
	}
	spotCtx.Uri, spotCtx.Url = DJContextURI, endpoint
	return spclient.NewContextResolver(requestCtx, &librespot.NullLogger{}, sp, spotCtx)
}

func (d *DJSession) Close() {
	d.cancel()
	d.mu.Lock()
	d.entries = nil
	d.resolver = nil
	d.mu.Unlock()
}

func (d *DJSession) Next(ctx context.Context) ([]playlist.Track, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	stop := context.AfterFunc(d.ctx, cancel)
	defer stop()
	defer cancel()
	items, err := d.resolver.Page(ctx, d.page)
	if err != nil {
		return nil, fmt.Errorf("spotify: next DJ batch: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("spotify: DJ returned an empty batch")
	}
	entries := make(map[string]*connectpb.ContextTrack)
	narrated := false
	var tracks []playlist.Track
	for i, item := range items {
		if item == nil {
			continue
		}
		narrated = narrated || item.Metadata["narration.intro.ssml"] != "" || item.Metadata["narration.outro.ssml"] != ""
		uri := item.Uri
		if uri == "" && len(item.Gid) == 16 {
			uri = librespot.SpotifyIdFromGid(librespot.SpotifyIdTypeTrack, item.Gid).Uri()
		}
		id, err := librespot.SpotifyIdFromUri(uri)
		if err != nil || id.Type() != librespot.SpotifyIdTypeTrack {
			return nil, fmt.Errorf("spotify: DJ returned an unsupported track")
		}
		path := fmt.Sprintf("%s%s:%d:%d", djStreamPrefix, d.id, d.page, i)
		entries[path] = &connectpb.ContextTrack{Uri: id.Uri(), Metadata: item.Metadata}
		title := item.Metadata["title"]
		if title == "" {
			title = "DJ · " + id.Uri()
		}
		duration, _ := strconv.Atoi(item.Metadata["duration"])
		track := playlist.Track{Path: path, Title: title, Artist: item.Metadata["artist_name"], Album: item.Metadata["album_title"], DurationSecs: duration / 1000, ProviderMeta: map[string]string{"spotify.dj": "true"}}
		if item.Metadata["title"] == "" {
			d.session.mu.RLock()
			if d.session.sess != nil {
				fillContextTrackMetadata(ctx, d.session.sess.Spclient(), *id, &track)
			}
			d.session.mu.RUnlock()
		}
		tracks = append(tracks, track)
	}
	if len(tracks) == 0 {
		return nil, fmt.Errorf("spotify: DJ returned no playable tracks")
	}
	if !narrated {
		return nil, fmt.Errorf("spotify: DJ batch has no narration; refusing music-only substitute")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.entries = entries
	d.page++
	return tracks, nil
}

func (p *SpotifyProvider) newDJStreamer(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	p.mu.Lock()
	dj := p.dj
	p.mu.Unlock()
	if dj == nil {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: DJ session ended; select DJ again")
	}
	dj.mu.Lock()
	item := dj.entries[uri]
	dj.mu.Unlock()
	if item == nil {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: DJ batch expired; select DJ again")
	}
	ctx, cancel := context.WithTimeout(dj.ctx, 30*time.Second)
	defer cancel()
	id, err := librespot.SpotifyIdFromUri(item.Uri)
	if err != nil {
		return nil, beep.Format{}, 0, err
	}
	stream, streamCancel, err := dj.session.NewStream(ctx, *id, p.bitrate, 0)
	if err != nil {
		return nil, beep.Format{}, 0, fmt.Errorf("spotify: DJ music: %w", err)
	}
	var intro, outro librespot.AudioSource
	success := false
	defer func() {
		if !success {
			streamCancel()
			closeDJSource(stream.Source)
			if intro != nil {
				closeDJSource(intro)
			}
			if outro != nil {
				closeDJSource(outro)
			}
		}
	}()
	intro, err = dj.narration(ctx, item.Metadata, "narration.intro")
	if err != nil {
		return nil, beep.Format{}, 0, err
	}
	outro, err = dj.narration(ctx, item.Metadata, "narration.outro")
	if err != nil {
		return nil, beep.Format{}, 0, err
	}
	stream.Source = lp.NewNarratedSource(&djNarrationLogger{}, intro, stream.Source, outro)
	s := newSpotifyStreamer(stream, streamCancel)
	s.closeNarration = func() { closeDJSource(intro); closeDJSource(outro) }
	success = true
	return s, s.Format(), s.Duration(), nil
}

func (d *DJSession) narration(ctx context.Context, metadata map[string]string, prefix string) (librespot.AudioSource, error) {
	ssml := metadata[prefix+".ssml"]
	if ssml == "" {
		return nil, nil
	}
	voice := narrationpb.ResolveRequest_VOICE1
	if v, ok := narrationpb.ResolveRequest_TtsVoice_value[metadata[prefix+".voice"]]; ok {
		voice = narrationpb.ResolveRequest_TtsVoice(v)
	}
	tts := narrationpb.ResolveRequest_SONANTIC_FAST
	if v, ok := narrationpb.ResolveRequest_TtsProvider_value[metadata[prefix+".tts_provider"]]; ok {
		tts = narrationpb.ResolveRequest_TtsProvider(v)
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	d.session.mu.RLock()
	defer d.session.mu.RUnlock()
	if d.session.sess == nil {
		return nil, fmt.Errorf("spotify: DJ session closed")
	}
	audioURL, err := d.session.sess.Spclient().NarrationUrl(ctx, spclient.NewNarrationRequest(ssml, voice, tts, lp.SampleRate))
	if err != nil {
		return nil, fmt.Errorf("spotify: DJ narration: %w", err)
	}
	loudness, peak, ok := lp.NarrationLoudness(metadata, prefix)
	if !ok {
		loudness, peak = -16, -3
	}
	source, err := lp.NewNarrationSource(ctx, &librespot.NullLogger{}, http.DefaultClient, audioURL, loudness, peak, 0)
	if err != nil {
		return nil, fmt.Errorf("spotify: DJ narration: %w", err)
	}
	return source, nil
}

// IsDJURI recognises DJ entries and generated stream paths before ordinary playlist handling.
func IsDJURI(uri string) bool {
	return uri == DJPlaylistID || uri == DJContextURI || "spotify:playlist:"+uri == DJContextURI || strings.HasPrefix(uri, djStreamPrefix)
}

func closeDJSource(source librespot.AudioSource) {
	if closer, ok := source.(io.Closer); ok {
		_ = closer.Close()
	}
}

// go-librespot skips a clip that fails during decoding. Surface its warning.
type djNarrationLogger struct {
	librespot.NullLogger
	err error
}

func (l *djNarrationLogger) WithError(err error) librespot.Logger {
	return &djNarrationLogger{err: err}
}
func (l *djNarrationLogger) Warnf(format string, args ...interface{}) {
	applog.UserWarn("spotify: DJ %s: %v", fmt.Sprintf(format, args...), l.err)
}
