package ui

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Region is a part of the frame a plugin can add a pane to.
type Region string

const (
	RegionVisualizer Region = "visualizer" // the full-width band above the columns
	RegionSettings   Region = "settings"   // the right-hand column
)

// PaneBuiltin is the reserved pane name: the active visualizer in the
// visualizer region, and the SRC/VOL/EQ/SHF/RPT/SPD column in settings.
const PaneBuiltin = "builtin"

// PaneSpec is one pane of a region.
type PaneSpec struct {
	Region Region
	Pane   string
	Weight int
	// Position places the pane before or after the builtin content.
	Position string // "before" | "after"
	// Vis names the visualizer this pane shows: a built-in name, a plugin
	// visualizer, or empty for the pane's own render (the builtin content, or
	// the plugin's render function).
	Vis string
}

// PaneSet is the runtime layout: the panes each region renders. Plugins add and
// remove their panes at runtime; the config is not involved.
type PaneSet struct {
	mu            sync.Mutex
	panes         []PaneSpec
	hidden        map[Region]bool
	builtinWeight map[Region]int
	gen           atomic.Uint64
	visCustom     atomic.Bool
	settingsCust  atomic.Bool
}

// Generation returns a monotonic counter bumped on every PaneSet mutation.
func (s *PaneSet) Generation() uint64 {
	if s == nil {
		return 0
	}
	return s.gen.Load()
}

// IsBuiltinOnly reports without locking whether region renders only its builtin pane.
func (s *PaneSet) IsBuiltinOnly(region Region) bool {
	if s == nil {
		return true
	}
	switch region {
	case RegionVisualizer:
		return !s.visCustom.Load()
	case RegionSettings:
		return !s.settingsCust.Load()
	default:
		return true
	}
}

func (s *PaneSet) syncFlagsLocked() {
	visCustom := false
	settingsCust := false
	for _, p := range s.panes {
		if p.Region == RegionVisualizer {
			visCustom = true
		} else if p.Region == RegionSettings {
			settingsCust = true
		}
	}
	s.visCustom.Store(visCustom)
	s.settingsCust.Store(settingsCust)
	s.gen.Add(1)
}

func NewPaneSet() *PaneSet {
	return &PaneSet{hidden: make(map[Region]bool), builtinWeight: make(map[Region]int)}
}

// Add registers a pane. A plugin can only add its own pane once per region.
func (s *PaneSet) Add(region, pane string, weight int) error {
	return s.AddAt(region, pane, weight, "after")
}

// AddAt registers a pane before or after the builtin content.
func (s *PaneSet) AddAt(region, pane string, weight int, position string) error {
	if s == nil {
		return fmt.Errorf("layout: no pane set")
	}
	if region != string(RegionVisualizer) && region != string(RegionSettings) {
		return fmt.Errorf("layout: unknown region %q", region)
	}
	if pane == "" || pane == PaneBuiltin {
		return fmt.Errorf("layout: invalid pane %q", pane)
	}
	if weight <= 0 {
		weight = 1
	}
	if position != "before" {
		position = "after"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.panes {
		if p.Region == Region(region) && p.Pane == pane {
			return fmt.Errorf("layout: pane %q already in %s", pane, region)
		}
	}
	s.panes = append(s.panes, PaneSpec{Region: Region(region), Pane: pane, Weight: weight, Position: position})
	s.syncFlagsLocked()
	return nil
}

// Remove drops a pane from a region.
func (s *PaneSet) Remove(region, pane string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.panes[:0]
	for _, p := range s.panes {
		if p.Region == Region(region) && p.Pane == pane {
			continue
		}
		kept = append(kept, p)
	}
	s.panes = kept
	s.syncFlagsLocked()
}

// List returns every pane, in registration order.
func (s *PaneSet) List() []PaneSpec {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PaneSpec(nil), s.panes...)
}

// RegionPanes returns the panes of one region: the ones registered before the
// builtin, the builtin itself unless it is hidden, and the ones registered
// after it.
func (s *PaneSet) RegionPanes(region Region) []PaneSpec {
	if s == nil {
		return []PaneSpec{{Region: region, Pane: PaneBuiltin, Weight: 1}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var before, after []PaneSpec
	for _, p := range s.panes {
		if p.Region != region {
			continue
		}
		if p.Position == "before" {
			before = append(before, p)
			continue
		}
		after = append(after, p)
	}
	winWeight := s.builtinWeight[region]
	if winWeight <= 0 {
		winWeight = 1
	}
	panes := before
	if !s.hidden[region] {
		panes = append(panes, PaneSpec{Region: region, Pane: PaneBuiltin, Weight: winWeight})
	}
	panes = append(panes, after...)
	if len(panes) == 0 {
		return []PaneSpec{{Region: region, Pane: PaneBuiltin, Weight: 1}}
	}
	return panes
}

// SetBuiltin hides or shows the builtin content of a region. Hiding it lets a
// plugin keep the region for its own panes only.
func (s *PaneSet) SetBuiltin(region string, enabled bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hidden[Region(region)] = !enabled
	s.syncFlagsLocked()
}

// SetVis assigns a visualizer to a pane. An empty name restores the pane's own
// render.
func (s *PaneSet) SetVis(region, pane, vis string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.panes {
		if s.panes[i].Region == Region(region) && s.panes[i].Pane == pane {
			s.panes[i].Vis = vis
			s.syncFlagsLocked()
			return
		}
	}
}

// SetWeight changes the weight of an existing pane.
func (s *PaneSet) SetWeight(region, pane string, weight int) {
	if s == nil || weight <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if pane == PaneBuiltin {
		s.builtinWeight[Region(region)] = weight
		s.syncFlagsLocked()
		return
	}
	for i := range s.panes {
		if s.panes[i].Region == Region(region) && s.panes[i].Pane == pane {
			s.panes[i].Weight = weight
			s.syncFlagsLocked()
			return
		}
	}
}

// SplitWeights divides total into len(weights) parts by weight, keeping at
// least one cell each and giving the remainder to the first panes.
func SplitWeights(total int, weights []int) []int {
	if len(weights) == 0 {
		return nil
	}
	if total < len(weights) {
		total = len(weights)
	}
	sum := 0
	for _, w := range weights {
		if w > 0 {
			sum += w
		}
	}
	if sum == 0 {
		sum = len(weights)
	}
	out := make([]int, len(weights))
	// Guarantee one cell per pane first, then share the rest by weight. The
	// other order can overflow the region when a heavy weight takes the whole
	// row and the remainder is negative.
	remaining := total - len(weights)
	if remaining < 0 {
		remaining = 0
	}
	used := 0
	for i, w := range weights {
		if w <= 0 {
			w = 1
		}
		extra := 0
		if sum > 0 {
			extra = remaining * w / sum
		}
		out[i] = 1 + extra
		used += extra
	}
	// Hand the rounding remainder to the heaviest pane, never past the total.
	if left := remaining - used; left > 0 {
		best := 0
		for i, w := range weights {
			if w > weights[best] {
				best = i
			}
		}
		out[best] += left
	}
	return out
}

// AddPane implements the plugin layout provider.
func (s *PaneSet) AddPane(region, pane string, weight int) error {
	return s.Add(region, pane, weight)
}

// RemovePane implements the plugin layout provider.
func (s *PaneSet) RemovePane(region, pane string) { s.Remove(region, pane) }

// Panes returns every registered pane.
func (s *PaneSet) Panes() []PaneSpec { return s.List() }

// AddPaneAt implements the plugin layout provider, with a position.
func (s *PaneSet) AddPaneAt(region, pane string, weight int, position string) error {
	return s.AddAt(region, pane, weight, position)
}

// Builtin implements the plugin layout provider.
func (s *PaneSet) Builtin(region string, enabled bool) { s.SetBuiltin(region, enabled) }

// Weight implements the plugin layout provider.
func (s *PaneSet) Weight(region, pane string, weight int) { s.SetWeight(region, pane, weight) }
