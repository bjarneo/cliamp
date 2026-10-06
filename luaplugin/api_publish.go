package luaplugin

import (
	"encoding/json"
	"errors"

	lua "github.com/yuin/gopher-lua"
)

// registerPublishAPI attaches :publish() to the plugin object.
// p:publish(topic, payload, {retain=true}) publishes only inside the
// immutable namespace derived from the installed plugin name.
func (m *Manager) registerPublishAPI(L *lua.LState, obj *lua.LTable, p *Plugin) {
	L.SetField(obj, "publish", L.NewFunction(func(L *lua.LState) int {
		topic := L.CheckString(2)
		payload := luaToGo(L.Get(3))
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
				err = publisher.Publish(fullTopic, data, retain)
			}
		}
		return pushResult(L, err)
	}))
}
