// Package authurl passes the URL of an interactive provider sign-in to the
// TUI, which shows it when the launched browser does not reach the user, for
// example in a container or on a headless machine.
package authurl

import (
	"sync/atomic"

	"github.com/bjarneo/cliamp/applog"
)

// Observer holds the callback that receives sign-in URLs. The zero value has
// no callback and is ready to use. Do not copy an Observer after first use.
type Observer struct {
	fn atomic.Pointer[func(string)]
}

// Set registers fn to receive each sign-in URL. Pass nil to remove it.
func (o *Observer) Set(fn func(string)) {
	if fn == nil {
		o.fn.Store(nil)
		return
	}
	o.fn.Store(&fn)
}

// Notify logs the sign-in URL of provider and passes the URL to the callback.
func (o *Observer) Notify(provider, url string) {
	applog.Info("%s: sign-in URL: %s", provider, url)
	if p := o.fn.Load(); p != nil {
		(*p)(url)
	}
}
