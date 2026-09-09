//go:build ignore

// Command worldmap_gen regenerates data.go and raster.bin.gz from the
// world-atlas 110m country boundaries, the same dataset cliamp.stream draws
// its listener globe from.
//
// Usage: go generate ./internal/worldmap
//
// The atlas is fetched from the jsDelivr CDN unless -atlas points at a local
// copy. Every country polygon is rasterised onto a half-degree grid (720x360
// cells, one byte per cell holding the country's index) and written gzipped
// to raster.bin.gz. data.go receives the index-to-code table and one marker
// position per country: the area-weighted centre of its largest landmass,
// snapped onto that landmass so the marker never floats in the sea. Countries
// too small for the 110m atlas (Singapore, Malta, Hong Kong, ...) get their
// marker from tzdata's zone.tab instead, so every ISO code still has a spot.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"log"
	"maps"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
)

const (
	atlasURL = "https://cdn.jsdelivr.net/npm/world-atlas@2.0.2/countries-110m.json"
	zoneTab  = "/usr/share/zoneinfo/zone.tab"
	// resolution is the raster cell size in degrees.
	resolution = 0.5
)

// numericCodes maps the ISO 3166-1 numeric ids used by world-atlas to alpha-2
// codes. It covers exactly the countries present in the 110m atlas.
var numericCodes = map[string]string{
	"004": "AF", "008": "AL", "010": "AQ", "012": "DZ", "024": "AO", "031": "AZ", "032": "AR", "036": "AU",
	"040": "AT", "044": "BS", "050": "BD", "051": "AM", "056": "BE", "064": "BT", "068": "BO", "070": "BA",
	"072": "BW", "076": "BR", "084": "BZ", "090": "SB", "096": "BN", "100": "BG", "104": "MM", "108": "BI",
	"112": "BY", "116": "KH", "120": "CM", "124": "CA", "140": "CF", "144": "LK", "148": "TD", "152": "CL",
	"156": "CN", "158": "TW", "170": "CO", "178": "CG", "180": "CD", "188": "CR", "191": "HR", "192": "CU",
	"196": "CY", "203": "CZ", "204": "BJ", "208": "DK", "214": "DO", "218": "EC", "222": "SV", "226": "GQ",
	"231": "ET", "232": "ER", "233": "EE", "238": "FK", "242": "FJ", "246": "FI", "250": "FR", "260": "TF",
	"262": "DJ", "266": "GA", "268": "GE", "270": "GM", "275": "PS", "276": "DE", "288": "GH", "300": "GR",
	"304": "GL", "320": "GT", "324": "GN", "328": "GY", "332": "HT", "340": "HN", "348": "HU", "352": "IS",
	"356": "IN", "360": "ID", "364": "IR", "368": "IQ", "372": "IE", "376": "IL", "380": "IT", "384": "CI",
	"388": "JM", "392": "JP", "398": "KZ", "400": "JO", "404": "KE", "408": "KP", "410": "KR", "414": "KW",
	"417": "KG", "418": "LA", "422": "LB", "426": "LS", "428": "LV", "430": "LR", "434": "LY", "440": "LT",
	"442": "LU", "450": "MG", "454": "MW", "458": "MY", "466": "ML", "478": "MR", "484": "MX", "496": "MN",
	"498": "MD", "499": "ME", "504": "MA", "508": "MZ", "512": "OM", "516": "NA", "524": "NP", "528": "NL",
	"540": "NC", "548": "VU", "554": "NZ", "558": "NI", "562": "NE", "566": "NG", "578": "NO", "586": "PK",
	"591": "PA", "598": "PG", "600": "PY", "604": "PE", "608": "PH", "616": "PL", "620": "PT", "624": "GW",
	"626": "TL", "630": "PR", "634": "QA", "642": "RO", "643": "RU", "646": "RW", "682": "SA", "686": "SN",
	"688": "RS", "694": "SL", "703": "SK", "705": "SI", "706": "SO", "710": "ZA", "716": "ZW", "724": "ES",
	"728": "SS", "729": "SD", "732": "EH", "740": "SR", "748": "SZ", "752": "SE", "756": "CH", "760": "SY",
	"762": "TJ", "764": "TH", "768": "TG", "780": "TT", "784": "AE", "788": "TN", "792": "TR", "795": "TM",
	"800": "UG", "804": "UA", "807": "MK", "818": "EG", "826": "GB", "834": "TZ", "840": "US", "854": "BF",
	"858": "UY", "860": "UZ", "862": "VE", "704": "VN", "887": "YE", "894": "ZM",
}

// unnumbered assigns codes to the atlas features that carry no ISO id. The
// two breakaway regions are folded into the state the directory files their
// listeners under.
var unnumbered = map[string]string{
	"Kosovo":     "XK",
	"Somaliland": "SO",
	"N. Cyprus":  "CY",
}

type topology struct {
	Transform *struct {
		Scale     [2]float64 `json:"scale"`
		Translate [2]float64 `json:"translate"`
	} `json:"transform"`
	Arcs    [][][2]float64 `json:"arcs"`
	Objects map[string]struct {
		Geometries []geometry `json:"geometries"`
	} `json:"objects"`
}

type geometry struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Properties struct {
		Name string `json:"name"`
	} `json:"properties"`
	Arcs json.RawMessage `json:"arcs"`
}

type point struct{ lon, lat float64 }

// polygon is one outer ring plus any holes, all in degrees.
type polygon [][]point

type raster struct {
	w, h  int
	cells []byte
}

func (r *raster) at(row, col int) byte { return r.cells[row*r.w+col] }

func main() {
	atlasPath := flag.String("atlas", "", "local copy of countries-110m.json (default: download)")
	zonePath := flag.String("zone", zoneTab, "tzdata zone.tab for countries missing from the atlas")
	flag.Parse()

	topo, err := loadAtlas(*atlasPath)
	if err != nil {
		log.Fatal(err)
	}
	countries, err := decodeCountries(topo)
	if err != nil {
		log.Fatal(err)
	}

	codes := slices.Sorted(maps.Keys(countries))
	ids := make(map[string]byte, len(codes))
	for i, code := range codes {
		ids[code] = byte(i + 1)
	}

	grid := &raster{w: int(math.Round(360 / resolution)), h: int(math.Round(180 / resolution))}
	grid.cells = make([]byte, grid.w*grid.h)
	centroids := make(map[string]point, len(codes))
	for _, code := range codes {
		id := ids[code]
		var best point
		bestWeight := -1.0
		for _, poly := range countries[code] {
			c, weight := rasterize(grid, poly, id)
			if weight > bestWeight {
				best, bestWeight = c, weight
			}
		}
		if bestWeight > 0 {
			// Round first so the position written out is the one checked.
			best = point{lat: round2(best.lat), lon: round2(best.lon)}
			centroids[code] = snap(grid, best, id)
		}
	}

	fallback, err := zoneCentroids(*zonePath)
	if err != nil {
		log.Fatal(err)
	}
	for code, c := range fallback {
		if _, ok := centroids[code]; !ok {
			centroids[code] = c
		}
	}

	if err := writeRaster("raster.bin.gz", grid); err != nil {
		log.Fatal(err)
	}
	if err := writeData("data.go", grid, codes, centroids); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %dx%d raster with %d countries and %d markers\n", grid.w, grid.h, len(codes), len(centroids))
}

func loadAtlas(path string) (*topology, error) {
	var body io.ReadCloser
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		body = f
	} else {
		resp, err := http.Get(atlasURL)
		if err != nil {
			return nil, fmt.Errorf("fetch atlas: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("fetch atlas: HTTP %d", resp.StatusCode)
		}
		body = resp.Body
	}
	defer body.Close()
	var topo topology
	if err := json.NewDecoder(body).Decode(&topo); err != nil {
		return nil, fmt.Errorf("decode atlas: %w", err)
	}
	if topo.Transform == nil {
		return nil, fmt.Errorf("decode atlas: unquantized topology is not supported")
	}
	return &topo, nil
}

// decodeCountries expands the topology's shared arcs into one polygon list
// per alpha-2 code.
func decodeCountries(topo *topology) (map[string][]polygon, error) {
	arcs := make([][]point, len(topo.Arcs))
	for i, arc := range topo.Arcs {
		pts := make([]point, len(arc))
		var x, y float64
		for j, d := range arc {
			x += d[0]
			y += d[1]
			pts[j] = point{
				lon: x*topo.Transform.Scale[0] + topo.Transform.Translate[0],
				lat: y*topo.Transform.Scale[1] + topo.Transform.Translate[1],
			}
		}
		arcs[i] = pts
	}
	ring := func(indices []int) []point {
		var out []point
		for _, idx := range indices {
			if idx >= 0 {
				out = append(out, arcs[idx]...)
				continue
			}
			arc := slices.Clone(arcs[^idx])
			slices.Reverse(arc)
			out = append(out, arc...)
		}
		return out
	}

	countries := make(map[string][]polygon)
	for _, g := range topo.Objects["countries"].Geometries {
		code, ok := numericCodes[g.ID]
		if !ok {
			code, ok = unnumbered[g.Properties.Name]
		}
		if !ok {
			return nil, fmt.Errorf("no country code for %q (id %q)", g.Properties.Name, g.ID)
		}
		var rings [][][]int
		switch g.Type {
		case "Polygon":
			var single [][]int
			if err := json.Unmarshal(g.Arcs, &single); err != nil {
				return nil, err
			}
			rings = [][][]int{single}
		case "MultiPolygon":
			if err := json.Unmarshal(g.Arcs, &rings); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported geometry %q for %s", g.Type, code)
		}
		for _, polyRings := range rings {
			var poly polygon
			for _, r := range polyRings {
				poly = append(poly, ring(r))
			}
			countries[code] = append(countries[code], poly)
		}
	}
	return countries, nil
}

// rasterize scan-fills one polygon (even-odd across all its rings) into the
// grid and returns the area-weighted centre of the cells it covered along
// with their total weight. Latitude is averaged directly and longitude
// circularly: a true spherical mean would pull countries that span many
// time zones towards the pole.
func rasterize(grid *raster, poly polygon, id byte) (point, float64) {
	minLat, maxLat := 90.0, -90.0
	for _, r := range poly {
		for _, p := range r {
			minLat, maxLat = min(minLat, p.lat), max(maxLat, p.lat)
		}
	}
	poly = unwrapSeam(poly)
	var sx, sy, slat, weight float64
	var xs []float64
	for row := range grid.h {
		lat := -90 + (float64(row)+0.5)*resolution
		if lat < minLat || lat > maxLat {
			continue
		}
		xs = xs[:0]
		for _, r := range poly {
			for i := range r {
				a, b := r[i], r[(i+1)%len(r)]
				if (a.lat <= lat) == (b.lat <= lat) {
					continue
				}
				xs = append(xs, a.lon+(lat-a.lat)*(b.lon-a.lon)/(b.lat-a.lat))
			}
		}
		slices.Sort(xs)
		w := math.Cos(lat * math.Pi / 180)
		for i := 0; i+1 < len(xs); i += 2 {
			start := int(math.Ceil((xs[i]+180)/resolution - 0.5))
			end := int(math.Ceil((xs[i+1]+180)/resolution-0.5)) - 1
			for c := start; c <= min(end, start+grid.w-1); c++ {
				col := ((c % grid.w) + grid.w) % grid.w // unwrapped rings run past 180E
				grid.cells[row*grid.w+col] = id
				lon := (-180 + (float64(col)+0.5)*resolution) * math.Pi / 180
				sx += w * math.Cos(lon)
				sy += w * math.Sin(lon)
				slat += w * lat
				weight += w
			}
		}
	}
	if weight == 0 {
		return point{}, 0
	}
	return point{lat: slat / weight, lon: math.Atan2(sy, sx) * 180 / math.Pi}, weight
}

// unwrapSeam rewrites rings the atlas clipped at the antimeridian, where the
// outline jumps straight from 180E to 180W with no edge along the seam.
// Shifting the western part by 360 degrees closes the ring again so the scan
// fill sees one shape instead of two open halves; Antarctica, which really
// does wrap the whole way round, is left alone.
func unwrapSeam(poly polygon) polygon {
	out := make(polygon, len(poly))
	for i, r := range poly {
		out[i] = r
		crosses := false
		for j := range r {
			if math.Abs(r[j].lon-r[(j+1)%len(r)].lon) > 180 {
				crosses = true
				break
			}
		}
		if !crosses {
			continue
		}
		shifted := make([]point, len(r))
		minLon, maxLon := math.Inf(1), math.Inf(-1)
		for j, p := range r {
			if p.lon < 0 {
				p.lon += 360
			}
			shifted[j] = p
			minLon, maxLon = min(minLon, p.lon), max(maxLon, p.lon)
		}
		if maxLon-minLon < 180 {
			out[i] = shifted
		}
	}
	return out
}

// snap moves c onto the nearest cell owned by id, searching outwards in
// rings, so a concave country's centre (Norway, Chile, Croatia) still lands on
// its own soil.
func snap(grid *raster, c point, id byte) point {
	row, col := grid.cell(c)
	if grid.at(row, col) == id {
		return c
	}
	for radius := 1; radius < grid.h/2; radius++ {
		bestD := math.Inf(1)
		var best point
		for dr := -radius; dr <= radius; dr++ {
			for dc := -radius; dc <= radius; dc++ {
				if max(abs(dr), abs(dc)) != radius {
					continue
				}
				r, cc := row+dr, ((col+dc)%grid.w+grid.w)%grid.w
				if r < 0 || r >= grid.h || grid.at(r, cc) != id {
					continue
				}
				p := grid.center(r, cc)
				d := math.Hypot(p.lat-c.lat, (p.lon-c.lon)*math.Cos(c.lat*math.Pi/180))
				if d < bestD {
					bestD, best = d, p
				}
			}
		}
		if bestD < math.Inf(1) {
			return best
		}
	}
	return c
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (r *raster) cell(p point) (row, col int) {
	row = min(max(int((p.lat+90)/resolution), 0), r.h-1)
	col = ((int((p.lon+180)/resolution) % r.w) + r.w) % r.w
	return row, col
}

func (r *raster) center(row, col int) point {
	return point{lat: -90 + (float64(row)+0.5)*resolution, lon: -180 + (float64(col)+0.5)*resolution}
}

// zoneCentroids averages each country's zone.tab coordinates, giving a marker
// position for countries the 110m atlas leaves out.
func zoneCentroids(path string) (map[string]point, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	type acc struct{ x, y, lat, n float64 }
	sums := make(map[string]*acc)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		lat, lon, err := parseCoordinates(fields[1])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		a := sums[fields[0]]
		if a == nil {
			a = &acc{}
			sums[fields[0]] = a
		}
		a.x += math.Cos(lon * math.Pi / 180)
		a.y += math.Sin(lon * math.Pi / 180)
		a.lat += lat
		a.n++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]point, len(sums))
	for code, a := range sums {
		out[code] = point{lat: a.lat / a.n, lon: math.Atan2(a.y, a.x) * 180 / math.Pi}
	}
	return out, nil
}

// parseCoordinates reads zone.tab's ISO 6709 form: ±DDMM±DDDMM or
// ±DDMMSS±DDDMMSS.
func parseCoordinates(s string) (lat, lon float64, err error) {
	split := strings.LastIndexAny(s, "+-")
	if split <= 0 {
		return 0, 0, fmt.Errorf("bad coordinates %q", s)
	}
	lat, err = parseDMS(s[:split], 2)
	if err != nil {
		return 0, 0, err
	}
	lon, err = parseDMS(s[split:], 3)
	return lat, lon, err
}

func parseDMS(s string, degDigits int) (float64, error) {
	if len(s) < 1+degDigits+2 {
		return 0, fmt.Errorf("bad coordinate %q", s)
	}
	sign := 1.0
	if s[0] == '-' {
		sign = -1
	}
	digits := s[1:]
	deg, err := strconv.Atoi(digits[:degDigits])
	if err != nil {
		return 0, fmt.Errorf("bad coordinate %q", s)
	}
	minutes, err := strconv.Atoi(digits[degDigits : degDigits+2])
	if err != nil {
		return 0, fmt.Errorf("bad coordinate %q", s)
	}
	seconds := 0
	if len(digits) >= degDigits+4 {
		if seconds, err = strconv.Atoi(digits[degDigits+2 : degDigits+4]); err != nil {
			return 0, fmt.Errorf("bad coordinate %q", s)
		}
	}
	return sign * (float64(deg) + float64(minutes)/60 + float64(seconds)/3600), nil
}

func writeRaster(path string, grid *raster) error {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := zw.Write(grid.cells); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func writeData(path string, grid *raster, codes []string, centroids map[string]point) error {
	var b strings.Builder
	b.WriteString("// Code generated from world-atlas countries-110m and tzdata zone.tab; DO NOT EDIT.\n")
	b.WriteString("// Regenerate with: go generate ./internal/worldmap\n\n")
	b.WriteString("package worldmap\n\n")
	fmt.Fprintf(&b, "// Raster dimensions of raster.bin.gz: one byte per %g-degree cell,\n", resolution)
	b.WriteString("// rows from 90S to 90N, columns from 180W to 180E.\n")
	fmt.Fprintf(&b, "const (\n\trasterWidth  = %d\n\trasterHeight = %d\n)\n\n", grid.w, grid.h)
	b.WriteString("// codes maps a raster cell value to its ISO 3166-1 alpha-2 code. Zero is sea.\n")
	b.WriteString("var codes = [...]string{\n\t\"\",\n")
	for _, code := range codes {
		fmt.Fprintf(&b, "\t%q,\n", code)
	}
	b.WriteString("}\n\n")
	b.WriteString("// centroids holds each country's marker position as {latitude, longitude}\n")
	b.WriteString("// in degrees: the centre of its largest landmass, or the mean of its\n")
	b.WriteString("// tzdata zone coordinates for countries the 110m atlas omits.\n")
	b.WriteString("var centroids = map[string][2]float32{\n")
	for _, code := range slices.Sorted(maps.Keys(centroids)) {
		c := centroids[code]
		fmt.Fprintf(&b, "\t%q: {%.2f, %.2f},\n", code, c.lat, c.lon)
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("format %s: %w", path, err)
	}
	return os.WriteFile(path, src, 0o644)
}
