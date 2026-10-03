package luaplugin

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	lua "github.com/yuin/gopher-lua"
)

type timerEntry struct {
	id     int64
	plugin *Plugin
	ticker *time.Ticker
	timer  *time.Timer
	done   chan struct{}
}

type timerManager struct {
	mu     sync.Mutex
	timers map[int64]*timerEntry
	nextID atomic.Int64
}

func newTimerManager() *timerManager {
	return &timerManager{
		timers: make(map[int64]*timerEntry),
	}
}

func (tm *timerManager) add(e *timerEntry) {
	tm.mu.Lock()
	tm.timers[e.id] = e
	tm.mu.Unlock()
}

func (tm *timerManager) cancel(id int64) {
	tm.mu.Lock()
	e, ok := tm.timers[id]
	if ok {
		close(e.done)
		if e.ticker != nil {
			e.ticker.Stop()
		}
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(tm.timers, id)
	}
	tm.mu.Unlock()
}

func (tm *timerManager) stopAll() {
	tm.mu.Lock()
	for id, e := range tm.timers {
		close(e.done)
		if e.ticker != nil {
			e.ticker.Stop()
		}
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(tm.timers, id)
	}
	tm.mu.Unlock()
}

func (tm *timerManager) stopPlugin(p *Plugin) {
	tm.mu.Lock()
	for id, e := range tm.timers {
		if e.plugin != p {
			continue
		}
		close(e.done)
		if e.ticker != nil {
			e.ticker.Stop()
		}
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(tm.timers, id)
	}
	tm.mu.Unlock()
}

func (tm *timerManager) take(id int64) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if _, ok := tm.timers[id]; !ok {
		return false
	}
	delete(tm.timers, id)
	return true
}

func (tm *timerManager) active(id int64) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	_, ok := tm.timers[id]
	return ok
}

// registerTimerAPI adds cliamp.timer.{after,every,cancel} to the cliamp table.
// p is the owning plugin. Each callback goes through m.call.
func (m *Manager) registerTimerAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin) {
	tm := m.timers
	tbl := L.NewTable()

	// cliamp.timer.after(secs, callback) -> id
	L.SetField(tbl, "after", L.NewFunction(func(L *lua.LState) int {
		secs := L.CheckNumber(1)
		fn := L.CheckFunction(2)
		id := tm.nextID.Add(1)
		d := time.Duration(float64(secs) * float64(time.Second))
		t := time.NewTimer(d)
		e := &timerEntry{id: id, plugin: p, timer: t, done: make(chan struct{})}
		tm.add(e)

		go func() {
			select {
			case <-t.C:
				// take runs under p.mu, so a cancel from another callback
				// of this plugin always wins.
				m.call(p, "timer", hookTimeout, 0, func(*lua.LState) (*lua.LFunction, []lua.LValue) {
					if !tm.take(id) {
						return nil, nil
					}
					return fn, nil
				})
			case <-e.done:
			}
		}()

		L.Push(lua.LNumber(id))
		return 1
	}))

	// cliamp.timer.every(secs, callback) -> id
	L.SetField(tbl, "every", L.NewFunction(func(L *lua.LState) int {
		secs := L.CheckNumber(1)
		fn := L.CheckFunction(2)
		id := tm.nextID.Add(1)
		d := time.Duration(float64(secs) * float64(time.Second))
		ticker := time.NewTicker(d)
		e := &timerEntry{id: id, plugin: p, ticker: ticker, done: make(chan struct{})}
		tm.add(e)

		go func() {
			for {
				select {
				case <-ticker.C:
					_, err := m.call(p, "timer", hookTimeout, 0, func(*lua.LState) (*lua.LFunction, []lua.LValue) {
						if !tm.active(id) {
							return nil, nil
						}
						return fn, nil
					})
					if errors.Is(err, errClosed) {
						return
					}
				case <-e.done:
					return
				}
			}
		}()

		L.Push(lua.LNumber(id))
		return 1
	}))

	// cliamp.timer.cancel(id)
	L.SetField(tbl, "cancel", L.NewFunction(func(L *lua.LState) int {
		id := L.CheckInt64(1)
		tm.cancel(id)
		return 0
	}))

	L.SetField(cliamp, "timer", tbl)
}

// registerSleepAPI adds cliamp.sleep(secs) — a blocking sleep.
// Note: this blocks the plugin's Lua VM, so other hooks for the same
// plugin will be queued until the sleep completes. Max 10 seconds.
// The sleep ends early when the time limit of the running callback or of the
// plugin load ends.
func registerSleepAPI(L *lua.LState, cliamp *lua.LTable) {
	L.SetField(cliamp, "sleep", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		if secs <= 0 || secs > 10 {
			return 0
		}
		t := time.NewTimer(time.Duration(secs * float64(time.Second)))
		defer t.Stop()
		var limit <-chan struct{} // nil blocks: no limit outside a callback
		if ctx := L.Context(); ctx != nil {
			limit = ctx.Done()
		}
		select {
		case <-t.C:
		case <-limit:
		}
		return 0
	}))
}
