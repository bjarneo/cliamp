package model

import (
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// helpCacheKey holds every input that affects the cached command help line.
type helpCacheKey struct {
	mode           commandMode
	width          int
	themeIdx       int
	styleGen       uint64
	focus          focusArea
	plCursor       int
	plPtr          *playlist.Playlist
	playlistRev    uint64
	providerName   string
	providerCount  int
	canBrowse      bool
	hasFavStore    bool
	hasRadioFavs   bool
	loadedPlaylist string
	heightExpanded bool
	simplified     bool
	hideHelpBar    bool
	hideSettings   bool
	fullVis        bool
	pureVis        bool
	showMetadata   bool
	playing        bool
	paused         bool
	layoutTier     layoutTier
}

type helpRenderCache struct {
	key   helpCacheKey
	value string
	valid bool
}

// playlistCacheKey holds every input that affects the cached playlist body.
type playlistCacheKey struct {
	plPtr            *playlist.Playlist
	playlistRev      uint64
	currentIdx       int
	isPlaying        bool
	playbackDetached bool
	plCursor         int
	plScroll         int
	focus            focusArea
	showHeaders      bool
	panelWidth       int
	budget           int
	themeIdx         int
	styleGen         uint64
	favGen           uint64
	favSetLen        int
	radioFavRev      uint64
	hasRadioFavs     bool
	savedPlaylist    bool
	hasPlayed        bool
	queueLen         int
}

type playlistRenderCache struct {
	key   playlistCacheKey
	value string
	valid bool
}

// settingsCacheKey holds every input that affects the cached settings column.
type settingsCacheKey struct {
	rows          int
	width         int
	focus         focusArea
	volume        float64
	volumeMin     float64
	mono          bool
	speed         float64
	eqBands       [eqBandCount]float64
	eqPreset      string
	eqCursor      int
	shuffle       bool
	repeat        playlist.RepeatMode
	provPillIdx   int
	providerCount int
	providerName  string
	networkText   string
	showMetadata  bool
	plPtr         *playlist.Playlist
	plCursor      int
	playlistRev   uint64
	playingActive bool
	playingPath   string
	isPlaying     bool
	streamTitle   string
	themeIdx      int
	styleGen      uint64
}

type settingsRenderCache struct {
	key   settingsCacheKey
	value string
	valid bool
}

// titleCacheKey holds every input that affects the sanitized terminal title.
type titleCacheKey struct {
	width       int
	trackPath   string
	title       string
	artist      string
	stream      bool
	status      uint8
	streamTitle string
	introActive bool
	introOffset int
}

type titleRenderCache struct {
	key   titleCacheKey
	value string
	valid bool
}

type bodyCacheKey struct {
	left          string
	right         string
	playlistWidth int
	settingsWidth int
	rows          int
}

type bodyRenderCache struct {
	key   bodyCacheKey
	value string
	valid bool
}

// renderCaches groups the pointer-backed render caches so New allocates them
// together.
type renderCaches struct {
	help     helpRenderCache
	playlist playlistRenderCache
	settings settingsRenderCache
	title    titleRenderCache
	body     bodyRenderCache
}

type visAnalysisEntry struct {
	spec  ui.VisAnalysisSpec
	bands []float64
}

// visTickScratch stores per-tick visualizer analysis results without a map
// allocation in the common case of at most four specs per tick.
type visTickScratch struct {
	entries [4]visAnalysisEntry
	count   int
	extra   map[ui.VisAnalysisSpec][]float64
}

func (s *visTickScratch) reset() {
	s.count = 0
	if len(s.extra) > 0 {
		clear(s.extra)
	}
}

func (s *visTickScratch) get(spec ui.VisAnalysisSpec) ([]float64, bool) {
	for i := range s.count {
		if s.entries[i].spec == spec {
			return s.entries[i].bands, true
		}
	}
	if len(s.extra) > 0 {
		bands, ok := s.extra[spec]
		return bands, ok
	}
	return nil, false
}

func (s *visTickScratch) put(spec ui.VisAnalysisSpec, bands []float64) {
	if s.count < len(s.entries) {
		s.entries[s.count] = visAnalysisEntry{spec: spec, bands: bands}
		s.count++
		return
	}
	if s.extra == nil {
		s.extra = make(map[ui.VisAnalysisSpec][]float64)
	}
	s.extra[spec] = bands
}

func helpModeCacheable(mode commandMode) bool {
	switch mode {
	case commandModeProvider,
		commandModeSearchOverlay,
		commandModeNetSearch,
		commandModeNavBrowser,
		commandModeLyrics,
		commandModePlaylistManager,
		commandModeFileBrowser:
		return false
	default:
		return true
	}
}

func (m Model) helpCacheKey(mode commandMode, width int) helpCacheKey {
	var plRev uint64
	if m.playlist != nil {
		plRev = m.playlist.Revision()
	}
	return helpCacheKey{
		mode:           mode,
		width:          width,
		themeIdx:       m.themeIdx,
		styleGen:       styleGen.Load(),
		focus:          m.focus,
		plCursor:       m.plCursor,
		plPtr:          m.playlist,
		playlistRev:    plRev,
		providerName:   providerName(m.provider),
		providerCount:  len(m.providers),
		canBrowse:      providerSupportsBrowse(m.provider),
		hasFavStore:    m.favStore != nil,
		hasRadioFavs:   m.radioFavorites != nil,
		loadedPlaylist: m.loadedPlaylist,
		heightExpanded: m.heightExpanded,
		simplified:     m.simplified,
		hideHelpBar:    m.hideHelpBar,
		hideSettings:   m.hideSettings,
		fullVis:        m.fullVis,
		pureVis:        m.pureVis,
		showMetadata:   m.showMetadata,
		playing:        m.isPlaying(),
		paused:         m.isPaused(),
		layoutTier:     m.layout.tier,
	}
}
