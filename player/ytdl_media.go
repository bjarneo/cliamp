package player

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

// ytdlMediaExpiryMargin is how long before its expire time a resolved media
// URL is treated as stale. A seek near the deadline resolves a fresh URL
// rather than starting a read that the server may cut off.
const ytdlMediaExpiryMargin = time.Minute

// errNoDirectYTDLMedia reports that yt-dlp picked a format ffmpeg cannot open
// by itself: HLS, or separate video and audio URLs.
var errNoDirectYTDLMedia = errors.New("no direct http format")

// ytdlMedia is the direct media URL that yt-dlp resolved for a page, with the
// request headers yt-dlp would send. ffmpeg opens it with -ss before -i, so it
// requests the byte range of the offset instead of downloading up to it.
type ytdlMedia struct {
	url       string
	userAgent string
	headers   string        // other request headers as "Key: Value\r\n" lines
	expires   time.Time     // zero when the URL carries no expire parameter
	duration  time.Duration // zero when yt-dlp reports none
}

// usable reports whether m can still be opened at now.
func (m *ytdlMedia) usable(now time.Time) bool {
	return m != nil && (m.expires.IsZero() || now.Before(m.expires.Add(-ytdlMediaExpiryMargin)))
}

// inputArgs returns the ffmpeg input arguments that open m at startSec.
func (m *ytdlMedia) inputArgs(startSec int) []string {
	var args []string
	if m.userAgent != "" {
		args = append(args, "-user_agent", m.userAgent)
	}
	if m.headers != "" {
		args = append(args, "-headers", m.headers)
	}
	// Reconnect a read that drops mid-track. The ffmpeg stage of the yt-dlp
	// pipe never needed this because yt-dlp owned the download.
	args = append(args, "-reconnect", "1", "-reconnect_on_network_error", "1", "-reconnect_delay_max", "5")
	if startSec > 0 {
		args = append(args, "-ss", strconv.Itoa(startSec))
	}
	return append(args, "-i", m.url)
}

// resolveYTDLMedia asks yt-dlp for the direct URL and request headers of the
// format that the yt-dlp pipe downloads for pageURL.
func resolveYTDLMedia(pageURL string) (*ytdlMedia, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ytdlPipeTimeout)
	defer cancel()
	args := []string{
		"-f", ytdlAudioFormat,
		"--no-playlist",
		"--quiet",
		"--no-warnings",
		"--socket-timeout", "15",
		"--print", "duration",
		"--print", "urls",
		"--print", "%(http_headers)j",
	}
	args = appendYTDLCookieArgs(args, pageURL)
	// "--" stops yt-dlp parsing pageURL as a flag, as in probeYTDLDuration.
	args = append(args, "--", pageURL)
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	cmd.WaitDelay = 3 * time.Second
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("yt-dlp: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("yt-dlp: %w", err)
	}
	return parseYTDLMedia(string(out))
}

// parseYTDLMedia reads the output of `--print duration --print urls --print
// %(http_headers)j`: the duration in seconds ("NA" when unknown), one URL
// line, then the headers as a JSON object. A format with separate video and
// audio prints more than one URL and is not direct.
func parseYTDLMedia(out string) (*ytdlMedia, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		return nil, errNoDirectYTDLMedia
	}
	var duration time.Duration
	if secs, err := strconv.ParseFloat(strings.TrimSpace(lines[0]), 64); err == nil && secs > 0 {
		duration = time.Duration(secs * float64(time.Second))
	}
	lines = lines[1:]
	u, err := url.Parse(strings.TrimSpace(lines[0]))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errNoDirectYTDLMedia
	}
	if strings.Contains(u.Path, ".m3u8") {
		return nil, errNoDirectYTDLMedia
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(lines[1]), &headers); err != nil {
		return nil, fmt.Errorf("yt-dlp headers: %w", err)
	}

	m := &ytdlMedia{url: u.String(), duration: duration}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := headers[k]
		// A CR or LF would split the header block that ffmpeg sends.
		if strings.ContainsAny(k+v, "\r\n") {
			continue
		}
		if strings.EqualFold(k, "User-Agent") {
			m.userAgent = v
			continue
		}
		b.WriteString(k + ": " + v + "\r\n")
	}
	m.headers = b.String()
	if exp, err := strconv.ParseInt(u.Query().Get("expire"), 10, 64); err == nil && exp > 0 {
		m.expires = time.Unix(exp, 0)
	}
	return m, nil
}

// directSeekPage reports whether a seek may read the direct media URL of
// pageURL. Only YouTube and YouTube Music pages qualify. Other yt-dlp sites
// keep the pipe: Mixcloud, for one, limits seeking on its own player, and the
// pipe discards input up to the target as its docs describe.
func directSeekPage(pageURL string) bool {
	return playlist.IsYouTubeURL(pageURL) || playlist.IsYouTubeMusicURL(pageURL)
}

// buildYTDLSeekPipeline restarts the yt-dlp page of cur at startSec. When the
// direct media URL is known, or can be resolved, ffmpeg reads it from the
// offset: a later seek then costs one ffmpeg start instead of a full yt-dlp
// run plus a download from the start of the track up to the offset. It falls
// back to the yt-dlp pipe when the page has no direct format or the direct
// read fails. A kept URL that fails, for example after it expired, is resolved
// again on the next seek. A URL that fails right after it was resolved stops
// the direct reads for this track, so a seek never pays for a resolve, a
// failed read and the pipe each time.
func (p *Player) buildYTDLSeekPipeline(cur *trackPipeline, startSec int) (*trackPipeline, error) {
	media, noDirect := cur.ytdlMedia, cur.ytdlNoDirect || !directSeekPage(cur.path)
	fresh := false
	if !noDirect && !media.usable(time.Now()) {
		var err error
		media, err = resolveYTDLMedia(cur.path)
		noDirect = errors.Is(err, errNoDirectYTDLMedia)
		fresh = media != nil
	}
	if media != nil {
		if tp, err := p.buildYTDLDirectPipeline(cur.path, media, startSec); err == nil {
			return tp, nil
		}
		noDirect = fresh
	}
	tp, err := p.buildYTDLPipeline(cur.path, startSec)
	if err != nil {
		return nil, err
	}
	tp.ytdlNoDirect = noDirect
	return tp, nil
}

// buildYTDLDirectSource starts a YouTube page at 0 from its direct media URL,
// so the pipeline holds the URL from the start and the first seek needs no
// yt-dlp run. It costs the same single yt-dlp run as the pipe, and that run
// also reports the duration, so no separate probe runs. It falls back to the
// pipe as buildYTDLSeekPipeline does.
func (p *Player) buildYTDLDirectSource(pageURL string, knownDuration time.Duration, probe bool) (*trackPipeline, error) {
	p.streamTitle.Store("")
	media, err := resolveYTDLMedia(pageURL)
	if media == nil {
		tp, pipeErr := p.buildYTDLPipeSource(pageURL, knownDuration, probe)
		if pipeErr != nil {
			return nil, pipeErr
		}
		tp.ytdlNoDirect = errors.Is(err, errNoDirectYTDLMedia)
		return tp, nil
	}
	tp, err := p.buildYTDLDirectPipeline(pageURL, media, 0)
	if err != nil {
		tp, err = p.buildYTDLPipeSource(pageURL, knownDuration, probe && media.duration == 0)
		if err != nil {
			return nil, err
		}
		tp.ytdlNoDirect = true
	}
	// The pipe fallback may already hold a probed duration.
	tp.knownDuration = cmp.Or(tp.knownDuration, knownDuration, media.duration)
	return tp, nil
}

// buildYTDLDirectPipeline starts ffmpeg on the direct media URL of pageURL at
// startSec and waits for its first audio, as buildYTDLPipeline does for the
// pipe.
func (p *Player) buildYTDLDirectPipeline(pageURL string, media *ytdlMedia, startSec int) (*trackPipeline, error) {
	if err := requireFFmpeg(); err != nil {
		return nil, err
	}
	fp, format, err := startFFmpegPipeArgs(media.inputArgs(startSec), nil, p.sr, p.bitDepth)
	if err != nil {
		return nil, err
	}
	decoder := &ffmpegPipeStreamer{ffmpegPipe: fp}
	if err := decoder.waitForInitialAudio(ytdlPipeTimeout); err != nil {
		_ = decoder.Close()
		return nil, err
	}
	return p.prefetchNetworkPipeline(&trackPipeline{
		decoder:      decoder,
		stream:       decoder,
		format:       format,
		seekable:     false,
		path:         pageURL,
		ytdlSeek:     true,
		ytdlMedia:    media,
		streamOffset: time.Duration(startSec) * time.Second,
	}, true), nil
}
