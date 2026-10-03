package ui

// LuaVisHost runs the Lua visualizers. *luaplugin.Manager implements it.
// The Visualizer calls it on the UI goroutine, so no method may wait for a
// plugin.
type LuaVisHost interface {
	RenderVis(name string, bands [DefaultSpectrumBands]float64, rows, cols int, frame uint64) string
	InitVis(name string, rows, cols int)
	DestroyVis(name string)
}

// RegisterLuaVisualizers adds Lua visualizer names so they can be cycled
// through with the v key and selected with ModeByName. host renders the
// active Lua visualizer, and it runs init and destroy when a Lua mode is
// selected and deselected. A second registration replaces the first.
func (v *Visualizer) RegisterLuaVisualizers(names []string, host LuaVisHost) {
	// The active Lua mode leaves the old plugin and enters the new one, so
	// the old plugin gets its destroy and the new one its init.
	activeLua := v.activeModeSet && v.activeMode >= VisCount
	if activeLua {
		if driver, ok := v.luaDriverCache[int(v.activeMode-VisCount)]; ok {
			driver.OnLeave(v)
		}
	}
	v.luaVisNames = names
	v.luaHost = host
	clear(v.luaDriverCache)
	if activeLua {
		if driver := v.driverFor(v.activeMode); driver != nil {
			driver.OnEnter(v)
		}
	}
}

// luaModeDriver draws a Lua visualizer. It runs the init of the plugin
// before the first render after the mode is selected, when the size is
// known, and the destroy when the mode is left after an init.
type luaModeDriver struct {
	spectrumDriverBase
	index       int
	initPending bool // the mode was selected, and init has not run
	initialized bool // init ran, so the mode needs a destroy when it is left
}

// name returns the plugin name of the driver, or false when the host or the
// name is missing.
func (d *luaModeDriver) name(v *Visualizer) (string, bool) {
	if v == nil || d.index < 0 || d.index >= len(v.luaVisNames) || v.luaHost == nil {
		return "", false
	}
	return v.luaVisNames[d.index], true
}

func (d *luaModeDriver) Render(v *Visualizer) string {
	name, ok := d.name(v)
	if !ok {
		return ""
	}
	if d.initPending {
		d.initPending = false
		d.initialized = true
		v.luaHost.InitVis(name, v.Rows, v.columns())
	}
	return v.luaHost.RenderVis(name, luaBands(v.SmoothedBands()), v.Rows, v.columns(), v.frame)
}

func (d *luaModeDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
}

func (d *luaModeDriver) OnEnter(*Visualizer) {
	d.initPending = true
}

func (d *luaModeDriver) OnLeave(v *Visualizer) {
	d.initPending = false
	if !d.initialized {
		return
	}
	d.initialized = false
	if name, ok := d.name(v); ok {
		v.luaHost.DestroyVis(name)
	}
}

func luaBands(src []float64) [DefaultSpectrumBands]float64 {
	var bands [DefaultSpectrumBands]float64
	copy(bands[:], src)
	return bands
}
