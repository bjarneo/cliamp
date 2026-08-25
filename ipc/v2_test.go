package ipc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestV2JSONEnvelope(t *testing.T) {
	request := V2Request{
		ID:        json.RawMessage(`"request-1"`),
		Method:    "call",
		Operation: "runtime.snapshot",
		Params:    json.RawMessage(`{"detail":"full"}`),
		JobID:     "job-1",
		Topics:    []string{"runtime.state"},
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	var decoded V2Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != protocolVersion2 || string(decoded.ID) != `"request-1"` || decoded.Operation != "runtime.snapshot" || decoded.JobID != "job-1" {
		t.Fatalf("decoded request = %#v", decoded)
	}

	response, err := json.Marshal(V2Response{
		ID:       decoded.ID,
		OK:       true,
		Snapshot: &RuntimeSnapshot{State: "playing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["version"]) != "2" || string(raw["id"]) != `"request-1"` {
		t.Fatalf("response envelope = %s", response)
	}
}

func TestOperationRegistryValidation(t *testing.T) {
	registry := DefaultOperationRegistry()
	tests := []struct {
		name   string
		params string
		want   string
	}{
		{name: "valid", params: `{"value":1.5,"index":0,"batch":[{"path":"song.flac"}]}`},
		{name: "string number", params: `{"value":"1"}`, want: V2ErrorCodeInvalidParams},
		{name: "fractional index", params: `{"index":1.5}`, want: V2ErrorCodeInvalidParams},
		{name: "string index", params: `{"index":"1"}`, want: V2ErrorCodeInvalidParams},
		{name: "negative index", params: `{"index":-1}`, want: V2ErrorCodeInvalidParams},
		{name: "malformed batch", params: `{"batch":{}}`, want: V2ErrorCodeInvalidParams},
		{name: "invalid JSON", params: `{"value":NaN}`, want: V2ErrorCodeInvalidParams},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := registry.Validate("queue.enqueue", json.RawMessage(test.params))
			if got := errorCode(err); got != test.want {
				t.Fatalf("Validate() error code = %q, want %q", got, test.want)
			}
		})
	}
	if got := errorCode(registry.Validate("runtime.missing", nil)); got != V2ErrorCodeUnknownOperation {
		t.Fatalf("unknown operation code = %q", got)
	}
}

func TestJobStoreLifecycleCancelAndExpiry(t *testing.T) {
	store := NewJobStore(WithJobTTL(5 * time.Millisecond))
	job, err := store.Create("provider.search")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := store.Start(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("job context was not canceled")
	}

	got, ok := store.Get(job.ID)
	if !ok || got.State != JobCanceled || got.Error == nil || got.Error.Code != V2ErrorCodeCanceled {
		t.Fatalf("canceled job = %#v, present=%v", got, ok)
	}
	select {
	case event := <-store.Events():
		if event.Type != "job.canceled" || event.Job.ID != job.ID || event.Job.State != JobCanceled {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("missing terminal job event")
	}

	time.Sleep(10 * time.Millisecond)
	if _, ok := store.Get(job.ID); ok {
		t.Fatal("terminal job did not expire")
	}
}

func TestJobStoreSnapshotResult(t *testing.T) {
	store := NewJobStore()
	job, err := store.Create("runtime.snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SucceedSnapshot(job.ID, RuntimeSnapshot{State: "playing"}); err != nil {
		t.Fatal(err)
	}
	got, ok := store.Get(job.ID)
	if !ok || got.State != JobSucceeded || got.Snapshot == nil || got.Snapshot.State != "playing" {
		t.Fatalf("snapshot job = %#v, present=%v", got, ok)
	}
}

func TestJobStorePreservesFailureDetail(t *testing.T) {
	store := NewJobStore()
	job, err := store.Create("provider.search")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Fail(job.ID, V2Error{Code: V2ErrorCodeInternal, Message: V2MessageInternal, Detail: "provider timed out"}); err != nil {
		t.Fatal(err)
	}
	completed, ok := store.Get(job.ID)
	if !ok || completed.Error == nil || completed.Error.Detail != "provider timed out" {
		t.Fatalf("job = %#v", completed)
	}
}

func TestServerRoutesV2(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	server.SetV2Dispatcher(V2DispatcherFunc(func(ctx context.Context, request V2Request) (V2Result, *V2Error) {
		if request.Method != "state.get" {
			t.Fatalf("method = %q", request.Method)
		}
		return V2Result{Snapshot: &RuntimeSnapshot{State: "playing"}}, nil
	}))

	response := sendV2Request(t, sock, V2Request{
		ID:     json.RawMessage(`42`),
		Method: "state.get",
	})
	if !response.OK || response.Version != protocolVersion2 || string(response.ID) != "42" || response.Snapshot == nil || response.Snapshot.State != "playing" {
		t.Fatalf("v2 response = %#v", response)
	}

	unsupported := sendRawV2Request(t, sock, []byte(`{"version":7,"id":"wrong-version"}`))
	if unsupported.OK || string(unsupported.ID) != `"wrong-version"` || errorCode(unsupported.Error) != V2ErrorCodeInvalidVersion {
		t.Fatalf("unsupported response = %#v", unsupported)
	}

}

func TestServerCanonicalizesMethodOperation(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	server.SetV2Dispatcher(V2DispatcherFunc(func(_ context.Context, request V2Request) (V2Result, *V2Error) {
		if request.Method != "play" || request.Operation != "play" {
			t.Fatalf("request = %#v", request)
		}
		return V2Result{}, nil
	}))

	response := sendV2Request(t, sock, V2Request{ID: json.RawMessage(`"play"`), Method: "play"})
	if !response.OK {
		t.Fatalf("response = %#v", response)
	}
}

func TestServerRoutesRuntimeSnapshotAliasToV2State(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	server.SetV2Dispatcher(V2DispatcherFunc(func(_ context.Context, request V2Request) (V2Result, *V2Error) {
		if request.Method != "state.get" || request.Operation != "" {
			t.Fatalf("request = %#v", request)
		}
		return V2Result{Snapshot: &RuntimeSnapshot{State: "paused"}}, nil
	}))

	response := sendV2Request(t, sock, V2Request{ID: json.RawMessage(`"snapshot"`), Method: "operation.submit", Operation: "runtime.snapshot"})
	if !response.OK || response.Job != nil || response.Snapshot == nil || response.Snapshot.State != "paused" {
		t.Fatalf("response = %#v", response)
	}
}

func TestServerCloseIsIdempotent(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestV2SubscribeAcknowledgesThenStreamsEvents(t *testing.T) {
	broker := NewBroker()
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServerWithBroker(sock, broker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	conn, err := dialSocket(sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request, err := json.Marshal(V2Request{
		ID:     json.RawMessage(`"subscription-1"`),
		Method: "subscribe",
		Topics: []string{"runtime.state"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		t.Fatal(err)
	}

	scanner := newFrameScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("read acknowledgement: %v", scanner.Err())
	}
	var acknowledgement V2Response
	if err := json.Unmarshal(scanner.Bytes(), &acknowledgement); err != nil {
		t.Fatal(err)
	}
	if !acknowledgement.OK || acknowledgement.Version != protocolVersion2 || string(acknowledgement.ID) != `"subscription-1"` {
		t.Fatalf("acknowledgement = %#v", acknowledgement)
	}

	if err := broker.Publish("runtime.state", json.RawMessage(`{"state":"playing"}`), false); err != nil {
		t.Fatal(err)
	}
	if !scanner.Scan() {
		t.Fatalf("read event: %v", scanner.Err())
	}
	var event Event
	if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "runtime.state" {
		t.Fatalf("event = %#v", event)
	}
}

func sendV2Request(t *testing.T, sock string, request V2Request) V2Response {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return sendRawV2Request(t, sock, data)
}

func sendRawV2Request(t *testing.T, sock string, data []byte) V2Response {
	t.Helper()
	conn, err := dialSocket(sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
	scanner := newFrameScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("read V2 response: %v", scanner.Err())
	}
	var response V2Response
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func errorCode(err *V2Error) string {
	if err == nil {
		return ""
	}
	return err.Code
}
