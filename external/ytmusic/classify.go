package ytmusic

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"cliamp/internal/appdir"

	"google.golang.org/api/youtube/v3"
)

// musicCategoryID is the YouTube video category for Music.
const musicCategoryID = "10"

// classificationCache maps playlist ID → true if the playlist is music.
type classificationCache struct {
	Music map[string]bool `json:"music"` // playlist ID → is music
}

// classificationCachePath returns the path to the classification cache file.
func classificationCachePath() string {
	dir, err := appdir.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ytmusic_classification.json")
}

// loadClassification loads cached playlist classifications from disk.
func loadClassification() map[string]bool {
	data, err := os.ReadFile(classificationCachePath())
	if err != nil {
		return nil
	}
	var cache classificationCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	return cache.Music
}

// saveClassification writes playlist classifications to disk.
func saveClassification(music map[string]bool) {
	cache := classificationCache{Music: music}
	data, _ := json.MarshalIndent(cache, "", "  ")
	path := classificationCachePath()
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, data, 0o600)
}

// classifyPlaylists determines which playlists contain music content by
// sampling one video from each and checking its category.
// Returns a map of playlist ID → true (music) / false (not music).
// Results are cached to disk to avoid repeated API calls.
func classifyPlaylists(ctx context.Context, svc *youtube.Service, playlists []playlistEntry, existing map[string]bool) map[string]bool {
	cached := existing
	if cached == nil {
		cached = loadClassification()
	}
	if cached == nil {
		cached = make(map[string]bool)
	}

	// Find playlists that need classification.
	var toClassify []playlistEntry
	for _, pl := range playlists {
		if _, ok := cached[pl.ID]; !ok {
			toClassify = append(toClassify, pl)
		}
	}

	if len(toClassify) == 0 {
		return cached
	}

	// Sample one video ID from each playlist (parallel, max 10 concurrent).
	type sampleResult struct {
		playlistID string
		videoID    string
	}
	sampleCh := make(chan sampleResult, len(toClassify))
	sem := make(chan struct{}, 10) // concurrency limit
	var wg sync.WaitGroup

	for _, pl := range toClassify {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			resp, err := svc.PlaylistItems.List([]string{"contentDetails"}).
				PlaylistId(pl.ID).
				MaxResults(1).
				Context(ctx).
				Do()
			if err != nil || len(resp.Items) == 0 {
				sampleCh <- sampleResult{playlistID: pl.ID}
				return
			}
			sampleCh <- sampleResult{
				playlistID: pl.ID,
				videoID:    resp.Items[0].ContentDetails.VideoId,
			}
		})
	}

	wg.Wait()
	close(sampleCh)

	// Collect video IDs for batch category lookup.
	videoToPlaylist := make(map[string]string) // videoID → playlistID
	var videoIDs []string
	for s := range sampleCh {
		if s.videoID != "" {
			videoToPlaylist[s.videoID] = s.playlistID
			videoIDs = append(videoIDs, s.videoID)
		} else {
			// No video found — default to non-music.
			cached[s.playlistID] = false
		}
	}

	// Batch fetch video categories.
	for i := 0; i < len(videoIDs); i += youtubeAPIBatchSize {
		end := min(i+youtubeAPIBatchSize, len(videoIDs))
		batch := videoIDs[i:end]

		vResp, err := svc.Videos.List([]string{"snippet"}).
			Id(batch...).
			Context(ctx).
			Do()
		if err != nil {
			// On error, default unclassified to non-music.
			for _, vid := range batch {
				if plID, ok := videoToPlaylist[vid]; ok {
					cached[plID] = false
				}
			}
			continue
		}

		for _, v := range vResp.Items {
			plID := videoToPlaylist[v.Id]
			cached[plID] = (v.Snippet.CategoryId == musicCategoryID)
		}
	}

	// Mark any remaining unclassified as non-music.
	for _, pl := range toClassify {
		if _, ok := cached[pl.ID]; !ok {
			cached[pl.ID] = false
		}
	}

	saveClassification(cached)
	return cached
}

// playlistEntry is a minimal playlist descriptor for classification.
type playlistEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
}

// classifyWithTimeout runs classification with a timeout.
func classifyWithTimeout(svc *youtube.Service, playlists []playlistEntry, timeout time.Duration, existing map[string]bool) map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return classifyPlaylists(ctx, svc, playlists, existing)
}
