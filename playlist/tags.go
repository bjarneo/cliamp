package playlist

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dhowden/tag"

	"github.com/bjarneo/cliamp/internal/appdir"
)

const (
	albumArtCacheDir      = "album-art"
	albumArtCacheMaxBytes = 100 << 20
)

var supportedPictureExts = map[string]bool{
	"jpg":  true,
	"jpeg": true,
	"png":  true,
	"gif":  true,
	"webp": true,
	"bmp":  true,
	"tiff": true,
}

// RefreshEmbeddedMetadata returns track with embedded local-file lyrics and
// album art populated from its current Path. Existing non-embedded fields are
// preserved so saved playlist metadata and provider fields remain stable.
func RefreshEmbeddedMetadata(track Track) Track {
	if track.Path == "" || track.Stream || IsURL(track.Path) || strings.HasPrefix(track.Path, "ssh://") {
		return track
	}

	fresh, ok := readTagsWithOptions(track.Path, true)
	if !ok {
		return track
	}
	track.EmbeddedLyrics = fresh.EmbeddedLyrics
	track.AlbumArtURL = fresh.AlbumArtURL
	return track
}

// readTags reads embedded metadata (ID3v2, Vorbis comments, MP4 atoms, WAV
// INFO lists) from a local audio file and returns a Track. Falls back to filename parsing if
// tag reading fails or the tags contain no title.
func readTags(path string) Track {
	t, _ := readTagsWithOptions(path, false)
	return t
}

func readTagsWithOptions(path string, cacheArt bool) (Track, bool) {
	f, err := os.Open(path)
	if err != nil {
		return TrackFromFilename(path), false
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil || m == nil {
		if t, ok := readWAVInfo(f, path); ok {
			return t, true
		}
		return TrackFromFilename(path), true
	}

	t := Track{
		Path:           path,
		EmbeddedLyrics: sanitizeTag(strings.TrimSpace(m.Lyrics())),
	}
	if cacheArt {
		t.AlbumArtURL = cacheAlbumArt(m.Picture())
	}
	if strings.TrimSpace(m.Title()) == "" {
		fallback := TrackFromFilename(path)
		fallback.EmbeddedLyrics = t.EmbeddedLyrics
		fallback.AlbumArtURL = t.AlbumArtURL
		return fallback, true
	}

	t.Title = sanitizeTag(strings.TrimSpace(m.Title()))
	t.Artist = sanitizeTag(strings.TrimSpace(m.Artist()))
	t.Album = sanitizeTag(strings.TrimSpace(m.Album()))
	t.Genre = sanitizeTag(strings.TrimSpace(m.Genre()))
	t.Year = m.Year()
	trackNum, _ := m.Track()
	t.TrackNumber = trackNum
	return t, true
}

func cacheAlbumArt(picture *tag.Picture) string {
	if picture == nil || len(picture.Data) == 0 {
		return ""
	}
	dir, err := appdir.DataDir()
	if err != nil {
		return ""
	}
	artDir := filepath.Join(dir, albumArtCacheDir)
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		return ""
	}

	sum := sha256.Sum256(picture.Data)
	ext := normalizedPictureExt(picture)
	path := filepath.Join(artDir, hex.EncodeToString(sum[:])+"."+ext)
	if _, err := os.Stat(path); err == nil {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		cleanupAlbumArtCache(artDir, albumArtCacheMaxBytes, path)
		return fileURL(path)
	}
	if err := os.WriteFile(path, picture.Data, 0o644); err != nil {
		return ""
	}
	cleanupAlbumArtCache(artDir, albumArtCacheMaxBytes, path)
	return fileURL(path)
}

func normalizedPictureExt(picture *tag.Picture) string {
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(picture.Ext), "."))
	if ext == "jpeg" {
		return "jpg"
	}
	if supportedPictureExts[ext] {
		return ext
	}
	switch strings.ToLower(strings.TrimSpace(picture.MIMEType)) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/bmp":
		return "bmp"
	case "image/tiff":
		return "tiff"
	default:
		return "jpg"
	}
}

func cleanupAlbumArtCache(dir string, maxBytes int64, keepPath string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	type cachedFile struct {
		path    string
		size    int64
		modTime time.Time
	}
	files := make([]cachedFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		size := info.Size()
		total += size
		files = append(files, cachedFile{path: path, size: size, modTime: info.ModTime()})
	}
	if total <= maxBytes {
		return
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})
	for _, file := range files {
		if total <= maxBytes {
			return
		}
		if file.path == keepPath {
			continue
		}
		if err := os.Remove(file.path); err == nil {
			total -= file.size
		}
	}
}

func fileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashed := filepath.ToSlash(abs)
	// Windows drive-letter paths (C:/...) have no leading slash; prepend one so
	// url.URL renders the RFC 8089 form file:///C:/... instead of file://C:/...
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

// TrackFromFilename creates a Track by parsing "Artist - Title" from the
// filename, or using the bare filename as the title.
func TrackFromFilename(path string) Track {
	base := filepath.Base(path)
	name := sanitizeTag(strings.TrimSuffix(base, filepath.Ext(base)))
	parts := strings.SplitN(name, " - ", 2)
	if len(parts) == 2 {
		return Track{Path: path, Artist: strings.TrimSpace(parts[0]), Title: strings.TrimSpace(parts[1])}
	}
	return Track{Path: path, Title: name}
}
