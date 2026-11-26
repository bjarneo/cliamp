package luaplugin

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const (
	// maxTimersPerPlugin bounds concurrent timers owned by one plugin.
	maxTimersPerPlugin = 32

	// minEveryInterval prevents tight repeating timer loops.
	minEveryInterval = 10 * time.Millisecond

	// maxTimerDuration caps timer intervals to avoid duration overflow.
	maxTimerDuration = 24 * time.Hour
)

var errTimerLimit = errors.New("per-plugin timer limit reached")

type timerEntry struct {
	id     int64
	plugin *Plugin
	ticker *time.Ticker
	timer  *time.Timer
	done   chan struct{}
}

type timerManager struct {
	mu      sync.Mutex
	timers  map[int64]*timerEntry
	perPlug map[*Plugin]int
	closed  bool
	nextID  atomic.Int64
	wg      sync.WaitGroup
}

func newTimerManager() *timerManager {
	return &timerManager{
		timers:  make(map[int64]*timerEntry),
		perPlug: make(map[*Plugin]int),
	}
}

func (tm *timerManager) decPlugLocked(p *Plugin) {
	tm.perPlug[p]--
	if tm.perPlug[p] <= 0 {
		delete(tm.perPlug, p)
	}
}

func (tm *timerManager) removeLocked(id int64, e *timerEntry) {
	close(e.done)
	if e.ticker != nil {
		e.ticker.Stop()
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	delete(tm.timers, id)
	tm.decPlugLocked(e.plugin)
}

func (tm *timerManager) add(e *timerEntry) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.closed {
		return errClosed
	}
	if tm.perPlug[e.plugin] >= maxTimersPerPlugin {
		return errTimerLimit
	}
	tm.timers[e.id] = e
	tm.perPlug[e.plugin]++
	tm.wg.Add(1)
	return nil
}

// cancel stops id only when p owns it.
func (tm *timerManager) cancel(p *Plugin, id int64) {
	tm.mu.Lock()
	if e, ok := tm.timers[id]; ok && e.plugin == p {
		tm.removeLocked(id, e)
	}
	tm.mu.Unlock()
}

// stopAll stops every active timer and waits for all timer goroutines to exit.
func (tm *timerManager) stopAll() {
	tm.mu.Lock()
	tm.closed = true
	for id, e := range tm.timers {
		tm.removeLocked(id, e)
	}
	tm.mu.Unlock()
	tm.wg.Wait()
}

func (tm *timerManager) stopPlugin(p *Plugin) {
	tm.mu.Lock()
	for id, e := range tm.timers {
		if e.plugin != p {
			continue
		}
		tm.removeLocked(id, e)
	}
	tm.mu.Unlock()
}

func (tm *timerManager) take(id int64) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	e, ok := tm.timers[id]
	if !ok {
		return false
	}
	delete(tm.timers, id)
	tm.decPlugLocked(e.plugin)
	return true
}

func (tm *timerManager) active(id int64) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	_, ok := tm.timers[id]
	return ok
}

// parseTimerDuration validates a positive finite interval in seconds and clamps
// it to the allowed bounds.
func parseTimerDuration(secs float64, every bool) (time.Duration, bool) {
	if math.IsNaN(secs) || math.IsInf(secs, 0) || secs <= 0 {
		return 0, false
	}
	if secs >= maxTimerDuration.Seconds() {
		return maxTimerDuration, true
	}
	d := time.Duration(secs * float64(time.Second))
	if every {
		if d < minEveryInterval {
			d = minEveryInterval
		}
	} else if d < time.Nanosecond {
		d = time.Nanosecond
	}
	return d, true
}

// registerTimerAPI adds cliamp.timer.{after,every,cancel} to the cliamp table.
// p is the owning plugin. Each callback goes through m.call.
func (m *Manager) registerTimerAPI(L *lua.LState, cliamp *lua.LTable, p *Plugin) {
	tm := m.timers
	tbl := L.NewTable()

	// cliamp.timer.after(secs, callback) -> id
	L.SetField(tbl, "after", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		fn := L.CheckFunction(2)
		d, ok := parseTimerDuration(secs, false)
		if !ok {
			L.ArgError(1, "interval must be a finite positive number")
			return 0
		}
		id := tm.nextID.Add(1)
		t := time.NewTimer(d)
		e := &timerEntry{id: id, plugin: p, timer: t, done: make(chan struct{})}
		if err := tm.add(e); err != nil {
			t.Stop()
			return pushErr(L, err.Error())
		}

		go func() {
			defer tm.wg.Done()
			select {
			case <-t.C:
				// take runs under p.mu so a cancel from another callback wins.
				m.call(p, "timer", hookTimeout, 0, func(*lua.LState) (*lua.LFunction, []lua.LValue) {
					if !tm.take(id) {
						return nil, nil
					}
					return fn, nil
				})
				// Release the timer slot even if m.call returned early.
				tm.take(id)
			case <-e.done:
			}
		}()

		L.Push(lua.LNumber(id))
		return 1
	}))

	// cliamp.timer.every(secs, callback) -> id
	L.SetField(tbl, "every", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		fn := L.CheckFunction(2)
		d, ok := parseTimerDuration(secs, true)
		if !ok {
			L.ArgError(1, "interval must be a finite positive number")
			return 0
		}
		id := tm.nextID.Add(1)
		ticker := time.NewTicker(d)
		e := &timerEntry{id: id, plugin: p, ticker: ticker, done: make(chan struct{})}
		if err := tm.add(e); err != nil {
			ticker.Stop()
			return pushErr(L, err.Error())
		}

		go func() {
			defer tm.wg.Done()
			defer tm.cancel(p, id)
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
		tm.cancel(p, id)
		return 0
	}))

	L.SetField(cliamp, "timer", tbl)
}

// registerSleepAPI adds cliamp.sleep(secs), a bounded blocking sleep up to 10s.
func registerSleepAPI(L *lua.LState, cliamp *lua.LTable) {
	L.SetField(cliamp, "sleep", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		if math.IsNaN(secs) || secs <= 0 || secs > 10 {
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
