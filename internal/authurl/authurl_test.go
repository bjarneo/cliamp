package authurl

import (
	"slices"
	"sync"
	"testing"
)

func TestObserver(t *testing.T) {
	tests := []struct {
		name string
		set  bool // register the callback before Notify
		drop bool // then remove it with Set(nil)
		want []string
	}{
		{name: "no callback"},
		{name: "callback", set: true, want: []string{"https://example.com/a"}},
		{name: "removed callback", set: true, drop: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var o Observer
			var got []string
			if tt.set {
				o.Set(func(u string) { got = append(got, u) })
			}
			if tt.drop {
				o.Set(nil)
			}

			o.Notify("test", "https://example.com/a")
			if !slices.Equal(got, tt.want) {
				t.Errorf("callback got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestObserverConcurrentUse(t *testing.T) {
	var o Observer
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				o.Set(func(string) {})
				o.Notify("test", "https://example.com")
				o.Set(nil)
			}
		})
	}
	wg.Wait()
}
