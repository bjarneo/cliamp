package luaplugin

import (
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
// p is the owning plugin whose mutex protects all LState calls.
func registerTimerAPI(L *lua.LState, cliamp *lua.LTable, tm *timerManager, p *Plugin) {
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
				p.mu.Lock()
				if !tm.take(id) {
					p.mu.Unlock()
					return
				}
				_ = L.CallByParam(lua.P{
					Fn:      fn,
					NRet:    0,
					Protect: true,
				})
				p.mu.Unlock()
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
					p.mu.Lock()
					if !tm.active(id) {
						p.mu.Unlock()
						return
					}
					_ = L.CallByParam(lua.P{
						Fn:      fn,
						NRet:    0,
						Protect: true,
					})
					p.mu.Unlock()
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
func registerSleepAPI(L *lua.LState, cliamp *lua.LTable) {
	L.SetField(cliamp, "sleep", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		if secs > 0 && secs <= 10 {
			time.Sleep(time.Duration(secs * float64(time.Second)))
		}
		return 0
	}))
}
