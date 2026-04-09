package ipc

import (
	"testing"
	"time"
)

func TestDispatchSeekSendsIPCSeekMsgWithDuration(t *testing.T) {
	var sent any
	s := &Server{
		disp: DispatcherFunc(func(msg any) {
			sent = msg
		}),
	}

	resp := s.dispatch(Request{Cmd: "seek", Value: 12.25})
	if !resp.OK {
		t.Fatalf("dispatch() response = %#v, want OK", resp)
	}

	got, ok := sent.(SeekMsg)
	if !ok {
		t.Fatalf("dispatch() sent %T, want ipc.SeekMsg", sent)
	}
	want := SeekMsg{Offset: 12250 * time.Millisecond}
	if got != want {
		t.Fatalf("dispatch() sent %#v, want %#v", got, want)
	}
}
