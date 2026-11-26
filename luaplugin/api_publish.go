package luaplugin

import (
	"encoding/json"
	"errors"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// maxRetainedTopicsPerPlugin caps distinct retained topics per plugin so one
// plugin cannot exhaust the IPC broker's global retained topic cap.
const maxRetainedTopicsPerPlugin = 32

// registerPublishAPI attaches :publish() to the plugin object.
// p:publish(topic, payload, {retain=true}) publishes only inside the
// immutable namespace derived from the installed plugin name.
func (m *Manager) registerPublishAPI(L *lua.LState, obj *lua.LTable, p *Plugin) {
	retained := make(map[string]struct{})
	L.SetField(obj, "publish", L.NewFunction(func(L *lua.LState) int {
		topic := L.CheckString(2)
		payload, err := luaToGoCtx(callContext(L), L.Get(3))
		if err != nil {
			return pushErr(L, err.Error())
		}
		retain := false
		if options, ok := L.Get(4).(*lua.LTable); ok {
			retain = lua.LVAsBool(options.RawGetString("retain"))
		}
		data, err := json.Marshal(payload)
		if err == nil {
			m.mu.RLock()
			publisher := m.publisher
			m.mu.RUnlock()
			if publisher == nil {
				err = errors.New("plugin event publisher is unavailable")
			} else if p.namespaceErr != nil {
				err = p.namespaceErr
			} else {
				fullTopic := "plugin." + p.namespace + "." + topic
				if retain {
					if _, exists := retained[fullTopic]; !exists && len(retained) >= maxRetainedTopicsPerPlugin {
						err = fmt.Errorf("plugin retained topic limit reached: max %d", maxRetainedTopicsPerPlugin)
					}
				}
				if err == nil {
					err = publisher.Publish(fullTopic, data, retain)
					if err == nil && retain {
						retained[fullTopic] = struct{}{}
					}
				}
			}
		}
		return pushResult(L, err)
	}))
}
