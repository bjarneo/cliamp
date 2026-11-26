package main

// ipc_client.go holds the V2 IPC client of the cliamp CLI. The subcommands,
// cliamp open and cliamp remote send their requests through it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/bjarneo/cliamp/ipc"
)

// cliRequestID is the V2 request ID of every CLI request.
const cliRequestID = `"cliamp"`

const (
	// ipcWait bounds how long a CLI command waits for the result of its job.
	// A job that never finishes then cannot hang a script, such as a Waybar
	// module that runs cliamp next.
	ipcWait = 30 * time.Second
	// ipcLoadWait bounds operations that load or resolve a whole list, as a
	// yt-dlp playlist can take minutes to resolve.
	ipcLoadWait = 5 * time.Minute
)

// userIPCError renders ipc.ErrNotRunning as the wording users see. The ipc
// package returns a bare sentinel, so all CLI copy stays in the command layer.
func userIPCError(err error) error {
	if errors.Is(err, ipc.ErrNotRunning) {
		return fmt.Errorf("cliamp is not running (no socket at %s)", ipc.DefaultSocketPath())
	}
	return err
}

// dialV2 opens a persistent version 2 IPC connection to the running cliamp.
func dialV2() (*ipc.V2Client, error) {
	client, err := ipc.DialV2(ipc.DefaultSocketPath())
	if err != nil {
		return nil, userIPCError(err)
	}
	return client, nil
}

// sendV2On sends one request with the CLI request ID on an open client.
func sendV2On(client *ipc.V2Client, request ipc.V2Request) (ipc.V2Response, error) {
	request.ID = json.RawMessage(cliRequestID)
	response, err := client.Send(request)
	if err != nil {
		return ipc.V2Response{}, userIPCError(err)
	}
	return response, nil
}

// sendV2 sends one request with the CLI request ID to the running cliamp.
func sendV2(request ipc.V2Request) (ipc.V2Response, error) {
	request.ID = json.RawMessage(cliRequestID)
	response, err := ipc.SendV2(ipc.DefaultSocketPath(), request)
	if err != nil {
		return ipc.V2Response{}, userIPCError(err)
	}
	return response, nil
}

// ipcSend submits operation and waits up to ipcWait for its result.
func ipcSend(operation string, params ipc.Request) (ipc.Response, error) {
	return ipcSendWithin(operation, params, ipcWait)
}

// ipcSendWithin submits operation and waits up to timeout for its result.
// The job keeps running in cliamp after the timeout. Plugin commands can
// run for minutes, for example for yt-dlp downloads.
func ipcSendWithin(operation string, params ipc.Request, timeout time.Duration) (ipc.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	response, err := ipcSendWithContext(ctx, operation, params)
	if errors.Is(err, context.DeadlineExceeded) {
		return response, fmt.Errorf("%s did not finish within %s", operation, timeout)
	}
	return response, err
}

func ipcSendWithContext(ctx context.Context, operation string, params ipc.Request) (ipc.Response, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return ipc.Response{}, fmt.Errorf("marshal %s parameters: %w", operation, err)
	}
	client, err := dialV2()
	if err != nil {
		return ipc.Response{}, err
	}
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()
	response, err := sendV2On(client, ipc.V2Request{
		Method:    "operation.submit",
		Operation: operation,
		Params:    raw,
	})
	if err != nil {
		return ipc.Response{}, err
	}
	if err := v2ResponseError(response); err != nil {
		return ipc.Response{}, err
	}
	if response.Job == nil {
		return ipc.Response{}, fmt.Errorf("%s returned no job", operation)
	}
	response, err = waitForV2JobWithClient(ctx, &client, response.Job.ID)
	if err != nil {
		return ipc.Response{}, err
	}
	if response.Job == nil {
		return ipc.Response{}, fmt.Errorf("%s completed without a job", operation)
	}
	var result ipc.Response
	if err := json.Unmarshal(response.Job.Result, &result); err != nil {
		return ipc.Response{}, fmt.Errorf("decode %s result: %w", operation, err)
	}
	if !result.OK {
		return result, fmt.Errorf("%s", result.Error)
	}
	return result, nil
}

// waitForV2Job polls the job until it ends or ctx is done.
func waitForV2Job(ctx context.Context, jobID string) (ipc.V2Response, error) {
	client, err := dialV2()
	if err != nil {
		return ipc.V2Response{}, err
	}
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()
	return waitForV2JobWithClient(ctx, &client, jobID)
}

// isConnResetOrEOF reports whether the peer closed or reset the connection.
func isConnResetOrEOF(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "closed by the remote host")
}

// sendV2JobPoll sends job.get on client and redials once after EOF or reset.
func sendV2JobPoll(client **ipc.V2Client, jobID string) (ipc.V2Response, error) {
	req := ipc.V2Request{Method: "job.get", JobID: jobID}
	if *client != nil {
		response, err := sendV2On(*client, req)
		if err == nil || !isConnResetOrEOF(err) {
			return response, err
		}
		_ = (*client).Close()
		*client = nil
	}
	fresh, err := dialV2()
	if err != nil {
		return ipc.V2Response{}, err
	}
	*client = fresh
	return sendV2On(fresh, req)
}

// waitForV2JobWithClient polls jobID on a persistent client until it ends or ctx ends.
func waitForV2JobWithClient(ctx context.Context, client **ipc.V2Client, jobID string) (ipc.V2Response, error) {
	delay := 5 * time.Millisecond
	const maxDelay = 100 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	for {
		response, err := sendV2JobPoll(client, jobID)
		if err != nil {
			if ctx.Err() != nil {
				return ipc.V2Response{}, ctx.Err()
			}
			return ipc.V2Response{}, err
		}
		if err := v2ResponseError(response); err != nil {
			return ipc.V2Response{}, err
		}
		if response.Job != nil {
			switch response.Job.State {
			case ipc.JobSucceeded:
				return response, nil
			case ipc.JobFailed, ipc.JobCanceled:
				if response.Job.Error != nil {
					if response.Job.Error.Detail != "" {
						return ipc.V2Response{}, fmt.Errorf("job %s (%s): %s (%s)", response.Job.State, response.Job.Error.Code, response.Job.Error.Message, response.Job.Error.Detail)
					}
					return ipc.V2Response{}, fmt.Errorf("job %s (%s): %s", response.Job.State, response.Job.Error.Code, response.Job.Error.Message)
				}
				return ipc.V2Response{}, fmt.Errorf("job %s", response.Job.State)
			}
		}
		timer.Reset(delay)
		select {
		case <-ctx.Done():
			return ipc.V2Response{}, ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, maxDelay)
	}
}

func ipcState() (ipc.RuntimeSnapshot, error) {
	response, err := sendV2(ipc.V2Request{Method: "state.get"})
	if err != nil {
		return ipc.RuntimeSnapshot{}, err
	}
	if err := v2ResponseError(response); err != nil {
		return ipc.RuntimeSnapshot{}, err
	}
	if response.Snapshot == nil {
		return ipc.RuntimeSnapshot{}, fmt.Errorf("state response has no snapshot")
	}
	return *response.Snapshot, nil
}

// stateResult converts a snapshot to the Response shape that cliamp status
// --json prints.
func stateResult(snapshot ipc.RuntimeSnapshot) ipc.Response {
	return ipc.Response{
		OK:         true,
		State:      snapshot.State,
		Track:      snapshot.Track,
		Position:   snapshot.Position,
		Duration:   snapshot.Duration,
		Volume:     snapshot.Volume,
		Playlist:   snapshot.Playlist,
		Index:      snapshot.Index,
		Total:      snapshot.Total,
		Visualizer: snapshot.Visualizer,
		Shuffle:    snapshot.Shuffle,
		Repeat:     snapshot.Repeat,
		Mono:       snapshot.Mono,
		Speed:      snapshot.Speed,
		EQPreset:   snapshot.EQPreset,
		Theme:      snapshot.Theme,
		EQBands:    snapshot.EQBands,
	}
}

// statusJSON is what cliamp status --json prints. It always holds the
// position, the volume and the index, because 0 is a real value of each.
// Its fields hide the omitempty fields of the same names in Response.
type statusJSON struct {
	ipc.Response
	Position float64 `json:"position"`
	Volume   float64 `json:"volume"`
	Index    int     `json:"index"`
}

func newStatusJSON(resp ipc.Response) statusJSON {
	return statusJSON{Response: resp, Position: resp.Position, Volume: resp.Volume, Index: resp.Index}
}

func printV2Response(response ipc.V2Response) error {
	if err := v2ResponseError(response); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}

func v2ResponseError(response ipc.V2Response) error {
	if response.OK {
		return nil
	}
	if response.Error == nil {
		return fmt.Errorf("remote operation failed")
	}
	if response.Error.Detail != "" {
		return fmt.Errorf("remote operation failed (%s): %s (%s)", response.Error.Code, response.Error.Message, response.Error.Detail)
	}
	return fmt.Errorf("remote operation failed (%s): %s", response.Error.Code, response.Error.Message)
}
