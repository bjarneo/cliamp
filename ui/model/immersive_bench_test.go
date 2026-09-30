package model

import (
	"fmt"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// benchModel is an immersive frame over a large library (3,500 browse rows,
// 500 queued tracks), the size where per-frame costs become visible.
func benchModel(b *testing.B, sec immSection, mode immCanvasMode) *Model {
	m := immersiveModel(b)
	m.width, m.height = 190, 50
	m.recomputeLayout()
	m.mouse = &mouseState{}
	m.immMouse = &immMouseGeom{}
	for i := 0; i < 2000; i++ {
		m.immersive.albums = append(m.immersive.albums, provider.AlbumInfo{ID: fmt.Sprint(i), Name: fmt.Sprintf("Album %d", i), Artist: "Artist"})
	}
	for i := 0; i < 1000; i++ {
		m.immersive.artists = append(m.immersive.artists, provider.ArtistInfo{ID: fmt.Sprint(i), Name: fmt.Sprintf("Artist %d", i)})
	}
	for i := 0; i < 500; i++ {
		m.immersive.lists = append(m.immersive.lists, playlist.PlaylistInfo{ID: fmt.Sprint(i), Name: fmt.Sprintf("List %d", i)})
	}
	var tr []playlist.Track
	for i := 0; i < 500; i++ {
		tr = append(tr, playlist.Track{Title: fmt.Sprintf("T%d", i), Artist: "A", Path: fmt.Sprint(i)})
	}
	m.playlist.Replace(tr)
	m.immersive.section = sec
	m.immersive.mode = mode
	return m
}

func BenchmarkImmersiveViewAlbumsList(b *testing.B) {
	m := benchModel(b, immSecAlbums, immCanvasList)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkImmersiveViewAlbumsGrid(b *testing.B) {
	m := benchModel(b, immSecAlbums, immCanvasGrid)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
