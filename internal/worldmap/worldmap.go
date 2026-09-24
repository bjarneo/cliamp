// Package worldmap embeds a coarse political map of the world: which country
// owns each half-degree cell of the globe, plus one marker position per ISO
// 3166-1 alpha-2 code. Both are generated from the world-atlas 110m dataset
// (see worldmap_gen.go), the same boundaries cliamp.stream draws its listener
// globe from, so the terminal globe and the website agree on what is land.
package worldmap

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"math"
	"slices"
	"sync"
)

//go:generate go run worldmap_gen.go

//go:embed raster.bin.gz
var rasterGZ []byte

// cellDegrees is the raster resolution in degrees per cell.
const cellDegrees = 360.0 / rasterWidth

// Map answers "which country is at this coordinate" from the embedded raster.
type Map struct {
	cells []byte
}

// Load decodes the embedded raster. Repeated calls share one decoded copy.
func Load() (*Map, error) { return load() }

var load = sync.OnceValues(func() (*Map, error) {
	zr, err := gzip.NewReader(bytes.NewReader(rasterGZ))
	if err != nil {
		return nil, fmt.Errorf("worldmap: open raster: %w", err)
	}
	cells, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("worldmap: read raster: %w", err)
	}
	if len(cells) != rasterWidth*rasterHeight {
		return nil, fmt.Errorf("worldmap: raster is %d bytes, want %d", len(cells), rasterWidth*rasterHeight)
	}
	return &Map{cells: cells}, nil
})

// IDAt returns the index of the country at a coordinate in degrees, or 0 for
// sea. Longitudes outside [-180, 180) wrap. Indexes are only meaningful for
// this build; turn them into codes with Code.
func (m *Map) IDAt(lat, lon float64) uint8 {
	if math.IsNaN(lat) || math.IsNaN(lon) {
		return 0
	}
	row := min(max(int(math.Floor((lat+90)/cellDegrees)), 0), rasterHeight-1)
	col := int(math.Floor((lon + 180) / cellDegrees))
	col = ((col % rasterWidth) + rasterWidth) % rasterWidth
	return m.cells[row*rasterWidth+col]
}

// Code resolves a raster index to its alpha-2 code; 0 and unknown indexes
// give "".
func Code(id uint8) string {
	if int(id) >= len(codes) {
		return ""
	}
	return codes[id]
}

// ID resolves an alpha-2 code to its raster index, or 0 when the country is
// not drawn on the map.
func ID(code string) uint8 {
	if i := slices.Index(codes[:], code); code != "" && i > 0 {
		return uint8(i)
	}
	return 0
}

// Centroid returns where to put a marker for a country: the centre of its
// largest landmass, or for countries too small to be drawn, the mean of their
// tzdata zone coordinates. ok is false for unknown codes.
func Centroid(code string) (lat, lon float64, ok bool) {
	c, ok := centroids[code]
	if !ok {
		return 0, 0, false
	}
	return float64(c[0]), float64(c[1]), true
}
